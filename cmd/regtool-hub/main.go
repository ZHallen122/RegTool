// Command regtool-hub serves the registry mirror list the regtool CLI reads,
// checks every mirror in it on a schedule and exports the results as JSON and
// as Prometheus metrics.
//
// It is optional: the CLI ships the same mirror list embedded in its binary and
// only talks to a hub when REGTOOL_SOURCES_URL points at one. Running a hub
// buys a mirror list that can be updated without a CLI release, and a history
// of which mirrors were reachable from where.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ZHallen122/RegTool/internal/hub"
	"github.com/ZHallen122/RegTool/internal/probe"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"
)

// drainTimeout is how long in-flight requests have to finish after a shutdown
// signal before the process stops waiting for them.
const drainTimeout = 10 * time.Second

// readyTimeout bounds how long /readyz stays unready while the very first check
// runs. Past it the hub is ready anyway: it can already serve the mirror list,
// which is the endpoint the CLI actually depends on, and a load balancer that
// keeps the hub out of rotation because a mirror on the far side of the world
// is slow has made the outage worse rather than better.
const readyTimeout = 30 * time.Second

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		// flag.ErrHelp means -h was asked for; the usage has already been
		// printed and it is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "regtool-hub:", err)
		os.Exit(1)
	}
}

// config is the resolved command line.
type config struct {
	addr          string
	sourcesPath   string
	dbPath        string
	checkInterval time.Duration
	checkTimeout  time.Duration
	retention     time.Duration
	logLevel      string
}

// run is main's body, taking its arguments and its log sink so a test can drive
// it without touching the process.
func run(args []string, logOut io.Writer) error {
	cfg, err := parseFlags(args, logOut)
	if err != nil {
		return err
	}

	level, err := parseLevel(cfg.logLevel)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: level}))

	// A signalled shutdown cancels this context, which stops the checker loop
	// and starts the server's drain.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sourcesJSON, sources, err := loadSources(cfg.sourcesPath)
	if err != nil {
		return err
	}
	targets := hub.TargetsFromSources(sources)

	store, err := hub.OpenStore(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	metrics := hub.NewMetrics()

	checker := hub.NewChecker(hub.CheckerConfig{
		Targets:   targets,
		Store:     store,
		Metrics:   metrics,
		Probe:     probe.Options{Timeout: cfg.checkTimeout},
		Interval:  cfg.checkInterval,
		Retention: cfg.retention,
		Logger:    logger,
	})

	var ready atomic.Bool
	server := hub.NewServer(hub.ServerConfig{
		Addr:    cfg.addr,
		Sources: sourcesJSON,
		Store:   store,
		Metrics: metrics,
		Ready:   ready.Load,
		Logger:  logger,
	})

	logger.Info("starting regtool-hub",
		"addr", cfg.addr,
		"db", cfg.dbPath,
		"mirrors", len(targets),
		"check_interval", cfg.checkInterval.String(),
		"retention", cfg.retention.String(),
	)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		markReadyWhenChecked(groupCtx, checker, &ready, logger)
		return nil
	})
	group.Go(func() error { return checker.Run(groupCtx) })
	group.Go(func() error { return server.ListenAndServe(groupCtx, drainTimeout) })

	if err := group.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("stopped")
	return nil
}

// markReadyWhenChecked flips readiness once the first check has finished, or
// once it has taken long enough that waiting for it is doing more harm than
// good. It returns early on shutdown so the group is not held open.
func markReadyWhenChecked(ctx context.Context, checker *hub.Checker, ready *atomic.Bool, logger *slog.Logger) {
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()

	select {
	case <-checker.Ready():
		ready.Store(true)
		logger.Info("ready")
	case <-timer.C:
		ready.Store(true)
		logger.Warn("ready before the first mirror check finished", "after", readyTimeout.String())
	case <-ctx.Done():
	}
}

// parseFlags resolves the command line, falling back to REGTOOL_HUB_* for any
// flag that was not given so the container image can be configured without an
// entrypoint script.
func parseFlags(args []string, out io.Writer) (config, error) {
	cfg := config{
		addr:          envOr("REGTOOL_HUB_ADDR", ":8080"),
		sourcesPath:   os.Getenv("REGTOOL_HUB_SOURCES"),
		dbPath:        envOr("REGTOOL_HUB_DB", "hub.db"),
		checkInterval: 5 * time.Minute,
		checkTimeout:  5 * time.Second,
		retention:     7 * 24 * time.Hour,
		logLevel:      envOr("REGTOOL_HUB_LOG_LEVEL", "info"),
	}
	for _, env := range []struct {
		name string
		dest *time.Duration
	}{
		{"REGTOOL_HUB_CHECK_INTERVAL", &cfg.checkInterval},
		{"REGTOOL_HUB_CHECK_TIMEOUT", &cfg.checkTimeout},
		{"REGTOOL_HUB_RETENTION", &cfg.retention},
	} {
		raw := os.Getenv(env.name)
		if raw == "" {
			continue
		}
		parsed, err := parseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("invalid %s: %w", env.name, err)
		}
		*env.dest = parsed
	}

	flags := flag.NewFlagSet("regtool-hub", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&cfg.addr, "addr", cfg.addr, "address to listen on")
	flags.StringVar(&cfg.sourcesPath, "sources", cfg.sourcesPath, "path to a sources.json (default: the copy embedded in the binary)")
	flags.StringVar(&cfg.dbPath, "db", cfg.dbPath, "path to the SQLite database of check results")
	flags.Var(durationFlag{&cfg.checkInterval}, "check-interval", "how often every mirror is checked")
	flags.Var(durationFlag{&cfg.checkTimeout}, "check-timeout", "how long a single mirror check may take")
	flags.Var(durationFlag{&cfg.retention}, "retention", "how long check results are kept")
	flags.StringVar(&cfg.logLevel, "log-level", cfg.logLevel, "debug, info, warn or error")
	flags.Usage = func() {
		fmt.Fprintln(out, "regtool-hub serves the regtool mirror list and checks the mirrors in it.")
		fmt.Fprintln(out, "\nUsage:\n  regtool-hub [flags]\n\nFlags:")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if rest := flags.Args(); len(rest) > 0 {
		return config{}, fmt.Errorf("unexpected argument %q", rest[0])
	}
	if cfg.checkInterval <= 0 {
		return config{}, fmt.Errorf("--check-interval must be positive, got %s", cfg.checkInterval)
	}
	if cfg.checkTimeout <= 0 {
		return config{}, fmt.Errorf("--check-timeout must be positive, got %s", cfg.checkTimeout)
	}
	return cfg, nil
}

// durationFlag is a [flag.Value] that also understands a count of days, so
// --retention 7d reads the way an operator expects it to.
type durationFlag struct{ dest *time.Duration }

func (d durationFlag) String() string {
	if d.dest == nil {
		return ""
	}
	return d.dest.String()
}

func (d durationFlag) Set(raw string) error {
	parsed, err := parseDuration(raw)
	if err != nil {
		return err
	}
	*d.dest = parsed
	return nil
}

// parseDuration is [time.ParseDuration] plus a plain day suffix, which it does
// not support because a day is not always 24 hours. Here it always is: a
// retention window is a span, not a calendar range.
func parseDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		count, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
		}
		return time.Duration(count * float64(24*time.Hour)), nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	return parsed, nil
}

// parseLevel resolves a log level name.
func parseLevel(name string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("invalid --log-level %q: want debug, info, warn or error", name)
	}
	return level, nil
}

// loadSources returns the body to serve from /v1/sources and the parsed form
// the check targets are built from.
//
// With no path it uses the list embedded in the binary, re-encoded: that is the
// same content the CLI would fall back to offline, so a hub with no sources
// file of its own is still useful. With a path the file is served verbatim,
// because whoever maintains that file decides what the CLI sees, down to the
// byte.
func loadSources(path string) ([]byte, *structs.RegistrySources, error) {
	if path == "" {
		sources, err := source.GetEmbeddedRegistrySources()
		if err != nil {
			return nil, nil, err
		}
		encoded, err := json.MarshalIndent(sources, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encode the embedded sources: %w", err)
		}
		return append(encoded, '\n'), sources, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read the sources file %s: %w", path, err)
	}
	var sources structs.RegistrySources
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, nil, fmt.Errorf("failed to parse the sources file %s: %w", path, err)
	}
	return raw, &sources, nil
}

// envOr reads an environment variable, falling back to a default.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
