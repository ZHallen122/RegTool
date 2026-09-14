package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/ZHallen122/RegTool/internal/probe"
	"github.com/ZHallen122/RegTool/source/structs"
)

// mirrors stands up the three interesting kinds of mirror: one that answers,
// one that fails, and one that is not there at all.
type mirrors struct {
	healthy string
	broken  string
	refused string
}

func newMirrors(t *testing.T) mirrors {
	t.Helper()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(healthy.Close)

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)

	// Closing a server immediately gives an address nothing is listening on,
	// which is the connection-refused case without hard-coding a port.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	refused := dead.URL
	dead.Close()

	return mirrors{healthy: healthy.URL, broken: broken.URL, refused: refused}
}

// newTestChecker wires a checker over a temporary store with a short timeout.
func newTestChecker(t *testing.T, targets []probe.Target, retention time.Duration) (*Checker, *Store, *Metrics) {
	t.Helper()

	store := newTestStore(t)
	metrics := NewMetrics()
	checker := NewChecker(CheckerConfig{
		Targets:   targets,
		Store:     store,
		Metrics:   metrics,
		Probe:     probe.Options{Timeout: 2 * time.Second},
		Interval:  time.Hour,
		Retention: retention,
	})
	return checker, store, metrics
}

func TestCheckerCheckOnceStoresEveryOutcome(t *testing.T) {
	t.Parallel()

	m := newMirrors(t)
	targets := []probe.Target{
		{App: "npm", Region: "cn", URL: m.healthy},
		{App: "pip", Region: "cn", URL: m.broken},
		{App: "gem", Region: "cn", URL: m.refused},
	}
	checker, store, metrics := newTestChecker(t, targets, 0)

	results, err := checker.CheckOnce(context.Background())
	if err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}
	if len(results) != len(targets) {
		t.Fatalf("CheckOnce returned %d results, want %d", len(results), len(targets))
	}

	stored, err := store.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	byApp := map[string]Result{}
	for _, row := range stored {
		byApp[row.App] = row
	}
	if len(byApp) != 3 {
		t.Fatalf("the store holds %d mirrors, want 3: %+v", len(byApp), stored)
	}

	if npm := byApp["npm"]; !npm.OK || npm.StatusCode != http.StatusOK || npm.Error != "" {
		t.Errorf("the healthy mirror stored as %+v", npm)
	}
	// A 5xx is reachable but unusable, and it carries a status rather than a
	// transport error.
	if pip := byApp["pip"]; pip.OK || pip.StatusCode != http.StatusInternalServerError || pip.Error == "" {
		t.Errorf("the 500 mirror stored as %+v", pip)
	}
	if gem := byApp["gem"]; gem.OK || gem.StatusCode != 0 || gem.Error == "" {
		t.Errorf("the refused mirror stored as %+v", gem)
	}

	// The gauges mirror what went into the store.
	if got := gaugeValue(t, metrics.mirrorUp, prometheus.Labels{"app": "npm", "region": "cn", "url": m.healthy}); got != 1 {
		t.Errorf("regtool_hub_mirror_up for the healthy mirror = %v, want 1", got)
	}
	if got := gaugeValue(t, metrics.mirrorUp, prometheus.Labels{"app": "gem", "region": "cn", "url": m.refused}); got != 0 {
		t.Errorf("regtool_hub_mirror_up for the refused mirror = %v, want 0", got)
	}
	if got := gaugeValue(t, metrics.mirrorLatency, prometheus.Labels{"app": "npm", "region": "cn", "url": m.healthy}); got < 0 {
		t.Errorf("regtool_hub_mirror_latency_seconds = %v, want a non-negative number", got)
	}
	if got := counterValue(t, metrics.checksTotal.WithLabelValues(resultLabelOK)); got != 1 {
		t.Errorf(`regtool_hub_checks_total{result="ok"} = %v, want 1`, got)
	}
	if got := counterValue(t, metrics.checksTotal.WithLabelValues(resultLabelError)); got != 2 {
		t.Errorf(`regtool_hub_checks_total{result="error"} = %v, want 2`, got)
	}
}

func TestCheckerPrunesPastRetention(t *testing.T) {
	t.Parallel()

	m := newMirrors(t)
	checker, store, _ := newTestChecker(t, []probe.Target{{App: "npm", Region: "cn", URL: m.healthy}}, time.Hour)

	// A row far older than the retention window, which the run must sweep.
	if err := store.Insert(context.Background(), []Result{
		{App: "npm", Region: "cn", URL: m.healthy, OK: true, CheckedAt: time.Now().Add(-72 * time.Hour)},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if _, err := checker.CheckOnce(context.Background()); err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}

	history, err := store.History(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("%d rows survived the retention sweep, want only the fresh one: %+v", len(history), history)
	}
}

func TestCheckerSingleFlight(t *testing.T) {
	t.Parallel()

	// The mirror blocks until the test lets it go, so the first run is
	// guaranteed to still be in flight when the second one starts.
	var (
		release     = make(chan struct{})
		releaseOnce sync.Once
		releaseAll  = func() { releaseOnce.Do(func() { close(release) }) }
		started     = make(chan struct{}, 1)
	)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	// The handler must be let go before the server is closed, or Close blocks
	// waiting for a request that is waiting for the test.
	defer slow.Close()
	defer releaseAll()

	checker, _, _ := newTestChecker(t, []probe.Target{{App: "npm", Region: "cn", URL: slow.URL}}, 0)

	done := make(chan error, 1)
	go func() {
		_, err := checker.CheckOnce(context.Background())
		done <- err
	}()
	<-started

	if _, err := checker.CheckOnce(context.Background()); !errors.Is(err, ErrCheckInProgress) {
		t.Errorf("an overlapping CheckOnce returned %v, want ErrCheckInProgress", err)
	}

	releaseAll()
	if err := <-done; err != nil {
		t.Errorf("the first CheckOnce failed: %v", err)
	}
}

func TestCheckerReadyClosesAfterFirstRun(t *testing.T) {
	t.Parallel()

	m := newMirrors(t)
	checker, _, _ := newTestChecker(t, []probe.Target{{App: "npm", Region: "cn", URL: m.healthy}}, 0)

	select {
	case <-checker.Ready():
		t.Fatal("Ready is closed before the first check has run")
	default:
	}

	if _, err := checker.CheckOnce(context.Background()); err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}

	select {
	case <-checker.Ready():
	case <-time.After(time.Second):
		t.Error("Ready did not close after the first check")
	}
}

func TestCheckerRunChecksAtStartupAndStopsOnCancel(t *testing.T) {
	t.Parallel()

	m := newMirrors(t)
	checker, store, _ := newTestChecker(t, []probe.Target{{App: "npm", Region: "cn", URL: m.healthy}}, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- checker.Run(ctx) }()

	// The loop checks immediately rather than waiting out the first interval,
	// which here is an hour.
	select {
	case <-checker.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not perform its first check at startup")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	latest, err := store.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 1 {
		t.Errorf("the startup check stored %d rows, want 1", len(latest))
	}
}

func TestCheckerRunRepeatsOnTheInterval(t *testing.T) {
	t.Parallel()

	hits := make(chan struct{}, 16)
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hits <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()

	store := newTestStore(t)
	checker := NewChecker(CheckerConfig{
		Targets:  []probe.Target{{App: "npm", Region: "cn", URL: mirror.URL}},
		Store:    store,
		Metrics:  NewMetrics(),
		Probe:    probe.Options{Timeout: time.Second},
		Interval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = checker.Run(ctx) }()

	// Two hits means the ticker fired at least once after the startup check.
	for i := range 2 {
		select {
		case <-hits:
		case <-time.After(10 * time.Second):
			t.Fatalf("only saw %d checks; want the loop to repeat on its interval", i)
		}
	}
}

func TestCheckerCancelledContextStillStores(t *testing.T) {
	t.Parallel()

	m := newMirrors(t)
	checker, _, metrics := newTestChecker(t, []probe.Target{{App: "npm", Region: "cn", URL: m.healthy}}, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// probe.Run reports the cancellation as a per-target failure rather than
	// returning an error, so the run still produces a result to record.
	results, err := checker.CheckOnce(ctx)
	if len(results) != 1 {
		t.Fatalf("CheckOnce returned %d results, want 1 (err=%v)", len(results), err)
	}
	if results[0].OK {
		t.Errorf("a cancelled probe reported OK: %+v", results[0])
	}
	if got := counterValue(t, metrics.checksTotal.WithLabelValues(resultLabelError)); got != 1 {
		t.Errorf(`regtool_hub_checks_total{result="error"} = %v, want 1`, got)
	}
}

func TestTargetsFromSources(t *testing.T) {
	t.Parallel()

	sources := structs.RegistrySources{
		"us": {"npm": {"https://registry.npmjs.org"}, "cargo": {"sparse+https://index.crates.io/"}},
		"cn": {"npm": {"https://registry.npmmirror.com"}, "empty": {}, "blank": {"  "}},
	}

	targets := TargetsFromSources(&sources)
	want := []probe.Target{
		{App: "npm", Region: "cn", URL: "https://registry.npmmirror.com"},
		{App: "cargo", Region: "us", URL: "sparse+https://index.crates.io/"},
		{App: "npm", Region: "us", URL: "https://registry.npmjs.org"},
	}
	if len(targets) != len(want) {
		t.Fatalf("TargetsFromSources gave %d targets, want %d: %+v", len(targets), len(want), targets)
	}
	// Sorted by region then app, so the order does not shuffle between runs
	// even though it comes out of a map.
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("target %d = %+v, want %+v", i, targets[i], want[i])
		}
	}

	if got := TargetsFromSources(nil); got != nil {
		t.Errorf("TargetsFromSources(nil) = %+v, want nil", got)
	}
}

// gaugeValue reads one series out of a gauge vector.
func gaugeValue(t *testing.T, vec *prometheus.GaugeVec, labels prometheus.Labels) float64 {
	t.Helper()

	gauge, err := vec.GetMetricWith(labels)
	if err != nil {
		t.Fatalf("GetMetricWith(%v): %v", labels, err)
	}
	var metric dto.Metric
	if err := gauge.Write(&metric); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return metric.GetGauge().GetValue()
}

// counterValue reads a counter.
func counterValue(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()

	var metric dto.Metric
	if err := counter.Write(&metric); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return metric.GetCounter().GetValue()
}
