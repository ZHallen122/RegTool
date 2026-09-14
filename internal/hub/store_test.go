package hub

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestStore opens a store in a temporary directory and closes it when the
// test ends. A file rather than :memory: on purpose: the DSN, the WAL pragma
// and the Windows path escaping are part of what is under test.
func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// baseTime is a fixed instant so the tests do not depend on the clock.
var baseTime = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

func TestStoreInsertAndLatest(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	older := []Result{
		{App: "npm", Region: "cn", URL: "https://a.example", OK: true, LatencyMS: 10, StatusCode: 200, CheckedAt: baseTime},
		{App: "npm", Region: "us", URL: "https://b.example", OK: false, LatencyMS: 900, StatusCode: 500, Error: "http 500", CheckedAt: baseTime},
	}
	newer := []Result{
		{App: "npm", Region: "cn", URL: "https://a.example", OK: false, LatencyMS: 20, StatusCode: 0, Error: "timed out", CheckedAt: baseTime.Add(time.Minute)},
	}
	if err := store.Insert(ctx, older); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.Insert(ctx, newer); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	latest, err := store.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("Latest returned %d rows, want one per mirror (2): %+v", len(latest), latest)
	}

	// Ordered by app, region, URL, so cn comes before us.
	cn := latest[0]
	if cn.Region != "cn" {
		t.Fatalf("first row is region %q, want the rows ordered by app then region", cn.Region)
	}
	if cn.OK || cn.Error != "timed out" || cn.LatencyMS != 20 {
		t.Errorf("Latest gave the stale cn row %+v, want the newer failing one", cn)
	}
	if !cn.CheckedAt.Equal(baseTime.Add(time.Minute)) {
		t.Errorf("checked_at round tripped as %s, want %s", cn.CheckedAt, baseTime.Add(time.Minute))
	}

	us := latest[1]
	if us.OK || us.StatusCode != 500 || us.Error != "http 500" {
		t.Errorf("us row round tripped as %+v", us)
	}
}

func TestStoreInsertEmptyIsNoOp(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Insert(context.Background(), nil); err != nil {
		t.Fatalf("Insert(nil): %v", err)
	}
	latest, err := store.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 0 {
		t.Errorf("Latest returned %d rows on an empty store", len(latest))
	}
}

func TestStoreHistory(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	var rows []Result
	for i := range 5 {
		rows = append(rows,
			Result{App: "npm", Region: "cn", URL: "https://a.example", OK: true, LatencyMS: int64(i), CheckedAt: baseTime.Add(time.Duration(i) * time.Minute)},
			Result{App: "pip", Region: "us", URL: "https://b.example", OK: true, LatencyMS: int64(i), CheckedAt: baseTime.Add(time.Duration(i) * time.Minute)},
		)
	}
	if err := store.Insert(ctx, rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	t.Run("newest first", func(t *testing.T) {
		history, err := store.History(ctx, "npm", "cn", 0)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(history) != 5 {
			t.Fatalf("History returned %d rows, want 5", len(history))
		}
		for i := 1; i < len(history); i++ {
			if history[i].CheckedAt.After(history[i-1].CheckedAt) {
				t.Fatalf("row %d (%s) is newer than row %d (%s); want newest first",
					i, history[i].CheckedAt, i-1, history[i-1].CheckedAt)
			}
		}
	})

	t.Run("filters", func(t *testing.T) {
		history, err := store.History(ctx, "pip", "", 0)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(history) != 5 {
			t.Fatalf("History(app=pip) returned %d rows, want 5", len(history))
		}
		for _, row := range history {
			if row.App != "pip" {
				t.Fatalf("History(app=pip) returned a %s row", row.App)
			}
		}

		all, err := store.History(ctx, "", "", 0)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(all) != 10 {
			t.Errorf("unfiltered History returned %d rows, want 10", len(all))
		}
	})

	t.Run("limit", func(t *testing.T) {
		history, err := store.History(ctx, "", "", 3)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(history) != 3 {
			t.Errorf("History(limit=3) returned %d rows", len(history))
		}
	})

	t.Run("no match", func(t *testing.T) {
		history, err := store.History(ctx, "cargo", "", 0)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(history) != 0 {
			t.Errorf("History for an unchecked app returned %d rows", len(history))
		}
	})
}

func TestStorePrune(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	rows := []Result{
		{App: "npm", Region: "cn", URL: "https://a.example", CheckedAt: baseTime.Add(-48 * time.Hour)},
		{App: "npm", Region: "cn", URL: "https://a.example", CheckedAt: baseTime.Add(-24 * time.Hour)},
		{App: "npm", Region: "cn", URL: "https://a.example", CheckedAt: baseTime},
	}
	if err := store.Insert(ctx, rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	deleted, err := store.Prune(ctx, baseTime.Add(-36*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Prune deleted %d rows, want 1", deleted)
	}

	remaining, err := store.History(ctx, "", "", 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("%d rows survived the prune, want 2", len(remaining))
	}
	for _, row := range remaining {
		if row.CheckedAt.Before(baseTime.Add(-36 * time.Hour)) {
			t.Errorf("row from %s survived a prune cutting at %s", row.CheckedAt, baseTime.Add(-36*time.Hour))
		}
	}

	// Pruning again finds nothing left to do.
	deleted, err = store.Prune(ctx, baseTime.Add(-36*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("a second Prune deleted %d rows, want 0", deleted)
	}
}

func TestStoreReopenKeepsRows(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hub.db")
	ctx := context.Background()

	first, err := OpenStore(ctx, path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := first.Insert(ctx, []Result{{App: "go", Region: "cn", URL: "https://goproxy.cn", OK: true, CheckedAt: baseTime}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening applies the migrations again, which must be a no-op.
	second, err := OpenStore(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	latest, err := second.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 1 || latest[0].App != "go" {
		t.Errorf("after reopening, Latest gave %+v", latest)
	}
}

func TestDSNEscapesPaths(t *testing.T) {
	t.Parallel()

	// The path is built with the platform's own separator, because that is what
	// filepath.ToSlash has to turn into the forward slash a URI needs. The
	// space must be escaped everywhere.
	got := DSN(filepath.Join("some dir", "hub.db"))
	if want := "file:some%20dir/hub.db?"; !strings.HasPrefix(got, want) {
		t.Errorf("DSN = %q, want it to start with %q", got, want)
	}
	for _, pragma := range []string{"journal_mode(WAL)", "busy_timeout(5000)"} {
		if !strings.Contains(got, pragma) {
			t.Errorf("DSN %q is missing the %s pragma", got, pragma)
		}
	}

	if runtime.GOOS == "windows" {
		// A drive letter's colon is legal in a URI path and must not be
		// escaped, or SQLite is handed a path it cannot open.
		if want := "file:C:/hub.db?"; !strings.HasPrefix(DSN(`C:\hub.db`), want) {
			t.Errorf("DSN(%q) = %q, want it to start with %q", `C:\hub.db`, DSN(`C:\hub.db`), want)
		}
	}
}

// TestStoreOpenInAPathWithASpace is the same concern as TestDSNEscapesPaths,
// but end to end: an escaped DSN is only worth anything if SQLite opens it.
func TestStoreOpenInAPathWithASpace(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "with a space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	store, err := OpenStore(context.Background(), filepath.Join(dir, "hub.db"))
	if err != nil {
		t.Fatalf("OpenStore in a path with a space: %v", err)
	}
	defer store.Close()

	if err := store.Insert(context.Background(), []Result{{App: "npm", Region: "cn", URL: "https://a.example", CheckedAt: baseTime}}); err != nil {
		t.Errorf("Insert: %v", err)
	}
}
