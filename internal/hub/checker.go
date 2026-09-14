package hub

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/ZHallen122/RegTool/internal/probe"
	"github.com/ZHallen122/RegTool/source/structs"
)

// CheckerConfig is everything a [Checker] needs. Store and Metrics are
// required; the rest fall back to a sensible default.
type CheckerConfig struct {
	// Targets are the mirrors to probe. Build them with [TargetsFromSources].
	Targets []probe.Target
	// Store receives every result.
	Store *Store
	// Metrics is updated after every run.
	Metrics *Metrics
	// Probe tunes the underlying [probe.Run]; it is injectable so tests can
	// point a short timeout at a slow httptest server.
	Probe probe.Options
	// Interval is how often a run starts. Defaults to [DefaultInterval].
	Interval time.Duration
	// Retention is how long results are kept. Zero disables pruning.
	Retention time.Duration
	// Logger receives one line per run. Defaults to the discarding logger.
	Logger *slog.Logger
	// Now is the clock, injectable for tests. Defaults to [time.Now].
	Now func() time.Time
}

// DefaultInterval is how often the checker runs when the config does not say.
const DefaultInterval = 5 * time.Minute

// Checker probes every mirror on a fixed interval and records what it finds.
//
// Runs never overlap. The interval bounds when a run may start, not how long
// one takes, so a run that outlives its tick would otherwise pile a second run
// on top of the first, doubling the load on mirrors that are already slow. A
// non-blocking mutex makes the late tick skip instead.
type Checker struct {
	targets   []probe.Target
	store     *Store
	metrics   *Metrics
	probeOpts probe.Options
	interval  time.Duration
	retention time.Duration
	log       *slog.Logger
	now       func() time.Time

	// running is held for the duration of a run and acquired with TryLock, so
	// an overlapping tick is dropped rather than queued.
	running sync.Mutex

	// readyOnce closes ready after the first run finishes, however it went.
	readyOnce sync.Once
	ready     chan struct{}
}

// NewChecker builds a checker from a config, filling in the defaults.
func NewChecker(cfg CheckerConfig) *Checker {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Checker{
		targets:   cfg.Targets,
		store:     cfg.Store,
		metrics:   cfg.Metrics,
		probeOpts: cfg.Probe,
		interval:  cfg.Interval,
		retention: cfg.Retention,
		log:       cfg.Logger,
		now:       cfg.Now,
		ready:     make(chan struct{}),
	}
}

// Ready is closed once the first run has finished, whether it succeeded or
// not. /readyz waits on it.
func (c *Checker) Ready() <-chan struct{} { return c.ready }

// Run checks once immediately and then on every tick, until ctx is cancelled.
// It always returns nil once ctx is done; a failing run is logged and the loop
// carries on, because a mirror or a disk that is unavailable now may well be
// available at the next tick.
func (c *Checker) Run(ctx context.Context) error {
	// The first run happens straight away rather than one interval from now,
	// so a hub that has just started has something to serve.
	c.runOnce(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

// runOnce performs a run and logs the outcome, dropping the run entirely if one
// is already in flight.
func (c *Checker) runOnce(ctx context.Context) {
	results, err := c.CheckOnce(ctx)
	switch {
	case errors.Is(err, ErrCheckInProgress):
		c.log.Warn("skipped a mirror check because the previous one is still running")
	case err != nil && ctx.Err() != nil:
		// Shutdown raced the run; there is nothing wrong to report.
		c.log.Debug("mirror check cancelled", "error", err.Error())
	case err != nil:
		c.log.Error("mirror check failed", "error", err.Error())
	default:
		c.log.Info("mirror check completed", "targets", len(results), "up", countUp(results))
	}
}

// ErrCheckInProgress is returned by [Checker.CheckOnce] when a run is already
// under way. It is a skip, not a failure.
var ErrCheckInProgress = errors.New("a mirror check is already running")

// CheckOnce probes every target once, stores the results, updates the metrics
// and prunes anything past the retention window. It is exported so a test — and
// the startup path — can drive a single run without the loop.
func (c *Checker) CheckOnce(ctx context.Context) ([]Result, error) {
	if !c.running.TryLock() {
		return nil, ErrCheckInProgress
	}
	defer c.running.Unlock()
	// The first run counts as done even when it fails: /readyz answers
	// "checked, and here is what we found", not "every mirror is healthy".
	defer c.readyOnce.Do(func() { close(c.ready) })

	start := c.now()
	probed := probe.Run(ctx, c.targets, c.probeOpts)
	c.metrics.ObserveCheckRun(c.now().Sub(start))

	results := make([]Result, 0, len(probed))
	for _, p := range probed {
		results = append(results, newResult(p, start))
	}
	c.metrics.ObserveResults(results)

	if err := c.store.Insert(ctx, results); err != nil {
		return results, err
	}
	if c.retention > 0 {
		if _, err := c.store.Prune(ctx, start.Add(-c.retention)); err != nil {
			return results, err
		}
	}
	return results, nil
}

// newResult flattens a probe result into the stored shape.
func newResult(p probe.Result, checkedAt time.Time) Result {
	return Result{
		App:        p.App,
		Region:     p.Region,
		URL:        p.URL,
		OK:         p.OK(),
		LatencyMS:  p.Latency.Milliseconds(),
		StatusCode: p.StatusCode,
		// Reason is the short form, which is what a dashboard cell and a
		// JSON consumer want; the long form repeats the URL already in the row.
		Error:     p.Reason(),
		CheckedAt: checkedAt,
	}
}

// countUp is the summary the run log carries.
func countUp(results []Result) int {
	up := 0
	for _, result := range results {
		if result.OK {
			up++
		}
	}
	return up
}

// TargetsFromSources flattens a sources.json into the list of mirrors to probe:
// every URL of every app of every region, in a stable order so the probe
// results — and therefore the stored rows — do not shuffle between runs.
//
// It deliberately does not filter by app name. The hub reports on whatever the
// sources file offers, so a mirror added there starts being checked without a
// hub release.
func TargetsFromSources(sources *structs.RegistrySources) []probe.Target {
	if sources == nil {
		return nil
	}

	regions := make([]string, 0, len(*sources))
	for region := range *sources {
		regions = append(regions, string(region))
	}
	sort.Strings(regions)

	var targets []probe.Target
	for _, region := range regions {
		apps := (*sources)[structs.Region(region)]

		names := make([]string, 0, len(apps))
		for app := range apps {
			names = append(names, app)
		}
		sort.Strings(names)

		for _, app := range names {
			for _, raw := range apps[app] {
				if probe.ProbeURL(raw) == "" {
					continue
				}
				targets = append(targets, probe.Target{App: app, Region: region, URL: raw})
			}
		}
	}
	return targets
}
