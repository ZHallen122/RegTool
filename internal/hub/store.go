package hub

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	// modernc.org/sqlite is a pure Go SQLite, so the hub still builds and runs
	// with CGO_ENABLED=0 and ships in a distroless image with no libc.
	_ "modernc.org/sqlite"
)

// timeLayout is how checked_at is stored. SQLite has no date type, and
// RFC3339 with a fixed number of fractional digits in UTC sorts
// lexicographically in the same order as the instants it describes, so
// ORDER BY and range comparisons on the text column are correct.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// Limits on how much history one read may ask for. They live here rather than
// with the handler because [Store.History] enforces them itself: a caller that
// forgets to clamp must not be able to pull the whole retention window into
// memory.
const (
	// DefaultHistoryLimit is how many rows are returned when no limit is given.
	DefaultHistoryLimit = 100
	// MaxHistoryLimit caps the limit.
	MaxHistoryLimit = 1000
)

// schema is applied on every open. Every statement is idempotent, so opening an
// existing database is the same operation as creating a new one.
const schema = `
CREATE TABLE IF NOT EXISTS checks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	app         TEXT    NOT NULL,
	region      TEXT    NOT NULL,
	url         TEXT    NOT NULL,
	ok          INTEGER NOT NULL,
	latency_ms  INTEGER NOT NULL,
	status_code INTEGER NOT NULL,
	error       TEXT    NOT NULL DEFAULT '',
	checked_at  TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS checks_app_region_checked_at
	ON checks (app, region, checked_at);
`

// Result is one mirror health check as it is stored and served. It is the wire
// shape of /v1/health and /v1/health/history, which is why the JSON tags spell
// out latency_ms rather than a Go duration: a dashboard wants a number.
type Result struct {
	App        string    `json:"app"`
	Region     string    `json:"region"`
	URL        string    `json:"url"`
	OK         bool      `json:"ok"`
	LatencyMS  int64     `json:"latency_ms"`
	StatusCode int       `json:"status_code"`
	Error      string    `json:"error"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Store is the SQLite-backed history of mirror health checks.
//
// It is safe for concurrent use: every method goes through [sql.DB], which
// pools connections, and the DSN turns on WAL so a read never blocks behind the
// checker's write.
type Store struct {
	db *sql.DB
}

// DSN builds the connection string for a database file. The pragmas matter:
// WAL lets the HTTP handlers read while the checker is writing, and a busy
// timeout turns the "database is locked" error into a short wait.
func DSN(path string) string {
	// The path is a URI path, so it needs forward slashes and percent escaping
	// even on Windows, where a temp directory can contain a space.
	escaped := strings.ReplaceAll(url.PathEscape(filepath.ToSlash(path)), "%2F", "/")
	return "file:" + escaped + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

// OpenStore opens the database at path, creating it if it does not exist, and
// applies the schema.
func OpenStore(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open the check database %s: %w", path, err)
	}

	// One writer at a time. SQLite serialises writes anyway; capping the pool
	// here means the wait happens in Go rather than as a SQLITE_BUSY retry.
	db.SetMaxOpenConns(4)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to reach the check database %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate the check database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close the check database: %w", err)
	}
	return nil
}

// Insert records a batch of results in one transaction, so a reader never sees
// half of a check run.
func (s *Store) Insert(ctx context.Context, results []Result) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin a check insert: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after a commit is a no-op

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO checks (app, region, url, ok, latency_ms, status_code, error, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare the check insert: %w", err)
	}
	defer stmt.Close()

	for _, result := range results {
		_, err := stmt.ExecContext(ctx,
			result.App, result.Region, result.URL, result.OK,
			result.LatencyMS, result.StatusCode, result.Error,
			result.CheckedAt.UTC().Format(timeLayout),
		)
		if err != nil {
			return fmt.Errorf("failed to insert the check for %s/%s: %w", result.App, result.Region, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit the check insert: %w", err)
	}
	return nil
}

// Latest returns the most recent result for every app, region and URL that has
// ever been checked, ordered by app, region and URL so the response is stable.
func (s *Store) Latest(ctx context.Context) ([]Result, error) {
	// The id is monotonic, so the newest row of a group is the one with the
	// largest id; grouping on the id avoids a tie-break when two runs land in
	// the same nanosecond.
	rows, err := s.db.QueryContext(ctx, `
		SELECT app, region, url, ok, latency_ms, status_code, error, checked_at
		FROM checks
		WHERE id IN (SELECT MAX(id) FROM checks GROUP BY app, region, url)
		ORDER BY app, region, url`)
	if err != nil {
		return nil, fmt.Errorf("failed to query the latest checks: %w", err)
	}
	return scanResults(rows)
}

// History returns the newest results first, optionally narrowed to one app
// and/or one region. An empty app or region means "any".
func (s *Store) History(ctx context.Context, app, region string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
	}

	var (
		where []string
		args  []any
	)
	if app != "" {
		where = append(where, "app = ?")
		args = append(args, app)
	}
	if region != "" {
		where = append(where, "region = ?")
		args = append(args, region)
	}

	query := `
		SELECT app, region, url, ok, latency_ms, status_code, error, checked_at
		FROM checks`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// The id breaks ties within a run, whose rows all share a timestamp.
	query += " ORDER BY checked_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query the check history: %w", err)
	}
	return scanResults(rows)
}

// Prune deletes every result older than the given instant and reports how many
// rows went. It is what keeps the database from growing without bound.
func (s *Store) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM checks WHERE checked_at < ?`,
		olderThan.UTC().Format(timeLayout),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to prune the check history: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count the pruned checks: %w", err)
	}
	return deleted, nil
}

// scanResults drains a result set of the standard column list.
func scanResults(rows *sql.Rows) ([]Result, error) {
	defer rows.Close()

	results := []Result{}
	for rows.Next() {
		var (
			result    Result
			checkedAt string
		)
		if err := rows.Scan(
			&result.App, &result.Region, &result.URL, &result.OK,
			&result.LatencyMS, &result.StatusCode, &result.Error, &checkedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan a check row: %w", err)
		}
		parsed, err := time.Parse(timeLayout, checkedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to parse the timestamp %q of a check row: %w", checkedAt, err)
		}
		result.CheckedAt = parsed
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read the check rows: %w", err)
	}
	return results, nil
}
