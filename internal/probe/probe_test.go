package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newServer starts a server that answers with status after waiting delay, and
// returns its URL. The server is shut down when the test ends.
func newServer(t *testing.T, status int, delay time.Duration) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, strings.Repeat("x", 64))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// refusedURL returns a URL nothing listens on: a listener is opened to get a
// port the OS is willing to hand out, then closed again.
func refusedURL(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a listener: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("failed to close the listener: %v", err)
	}
	return "http://" + address
}

// findResult returns the result for one region.
func findResult(t *testing.T, results []Result, region string) Result {
	t.Helper()

	for _, result := range results {
		if result.Region == region {
			return result
		}
	}
	t.Fatalf("Run() returned no result for region %q: %+v", region, results)
	return Result{}
}

func TestRunReportsEveryKindOfOutcome(t *testing.T) {
	t.Parallel()

	fast := newServer(t, http.StatusOK, 0)
	notFound := newServer(t, http.StatusNotFound, 0)
	broken := newServer(t, http.StatusInternalServerError, 0)
	slow := newServer(t, http.StatusOK, 300*time.Millisecond)
	refused := refusedURL(t)

	targets := []Target{
		{App: "npm", Region: "ok", URL: fast},
		{App: "npm", Region: "notfound", URL: notFound},
		{App: "npm", Region: "broken", URL: broken},
		{App: "npm", Region: "slow", URL: slow},
		{App: "npm", Region: "refused", URL: refused},
	}

	results := Run(context.Background(), targets, Options{Timeout: 100 * time.Millisecond})

	if len(results) != len(targets) {
		t.Fatalf("Run() returned %d results, want %d", len(results), len(targets))
	}
	// Order is part of the contract: the slow target finishes last but stays in
	// the middle of the slice.
	for i := range targets {
		if results[i].Target != targets[i] {
			t.Errorf("result %d is for %+v, want %+v", i, results[i].Target, targets[i])
		}
	}

	if ok := findResult(t, results, "ok"); !ok.OK() || ok.StatusCode != http.StatusOK {
		t.Errorf("the reachable mirror came back as %+v", ok)
	}

	// A 404 on the bare root is what several real registries answer with; it
	// still proves the mirror is there.
	notFoundResult := findResult(t, results, "notfound")
	if !notFoundResult.OK() {
		t.Errorf("a 404 was treated as unreachable: %+v", notFoundResult)
	}
	if notFoundResult.Reason() != "" {
		t.Errorf("a usable mirror has reason %q, want none", notFoundResult.Reason())
	}

	brokenResult := findResult(t, results, "broken")
	if brokenResult.OK() {
		t.Error("a 500 was treated as usable")
	}
	if brokenResult.Err != nil {
		t.Errorf("a 500 was reported as a transport failure: %v", brokenResult.Err)
	}
	if got, want := brokenResult.Reason(), "http 500"; got != want {
		t.Errorf("a 500 reads as %q, want %q", got, want)
	}

	slowResult := findResult(t, results, "slow")
	if slowResult.Err == nil {
		t.Fatalf("the slow mirror answered within the timeout: %+v", slowResult)
	}
	if !errors.Is(slowResult.Err, context.DeadlineExceeded) {
		t.Errorf("the slow mirror failed with %v, want a deadline error", slowResult.Err)
	}
	if got, want := slowResult.Reason(), "timed out"; got != want {
		t.Errorf("a timeout reads as %q, want %q", got, want)
	}

	refusedResult := findResult(t, results, "refused")
	if refusedResult.Err == nil {
		t.Fatalf("a closed port answered: %+v", refusedResult)
	}
	if refusedResult.OK() {
		t.Error("a closed port was treated as usable")
	}
	if reason := refusedResult.Reason(); reason == "" || strings.Contains(reason, "Get \"") {
		t.Errorf("a refused connection reads as %q, want a short reason", reason)
	}
}

func TestRunHonoursTheConcurrencyLimit(t *testing.T) {
	t.Parallel()

	const limit = 3

	var (
		inFlight atomic.Int64
		peak     atomic.Int64
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}
		// Hold the request open long enough that the limit has to bite.
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	targets := make([]Target, 0, 24)
	for i := range 24 {
		targets = append(targets, Target{App: "npm", Region: fmt.Sprintf("r%d", i), URL: server.URL})
	}

	results := Run(context.Background(), targets, Options{Concurrency: limit, Timeout: 5 * time.Second})

	for i, result := range results {
		if !result.OK() {
			t.Fatalf("target %d failed: %+v", i, result)
		}
	}
	if got := peak.Load(); got > limit {
		t.Errorf("Run() had %d probes in flight at once, want at most %d", got, limit)
	}
	if got := peak.Load(); got < 2 {
		t.Errorf("Run() never had more than %d probe in flight, so nothing concurrent was tested", got)
	}
}

func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	// The server blocks until the test lets it go, so every probe is still in
	// flight when the context is cancelled.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	targets := make([]Target, 0, 10)
	for i := range 10 {
		targets = append(targets, Target{App: "npm", Region: fmt.Sprintf("r%d", i), URL: server.URL})
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	done := make(chan []Result, 1)
	go func() { done <- Run(ctx, targets, Options{Concurrency: 2, Timeout: time.Minute}) }()

	var results []Result
	select {
	case results = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return after the context was cancelled")
	}

	if len(results) != len(targets) {
		t.Fatalf("Run() returned %d results, want %d", len(results), len(targets))
	}
	for i, result := range results {
		if result.Err == nil {
			t.Errorf("target %d succeeded even though the run was cancelled: %+v", i, result)
			continue
		}
		if !errors.Is(result.Err, context.Canceled) {
			t.Errorf("target %d failed with %v, want a cancellation", i, result.Err)
		}
	}
}

func TestRunReportsATargetWithNoURL(t *testing.T) {
	t.Parallel()

	results := Run(context.Background(), []Target{{App: "npm", Region: "us"}}, Options{})

	if len(results) != 1 {
		t.Fatalf("Run() returned %d results, want 1", len(results))
	}
	if results[0].Err == nil {
		t.Fatalf("an empty URL probed successfully: %+v", results[0])
	}
	if results[0].OK() {
		t.Error("an empty URL was treated as usable")
	}
}

func TestRunRejectsAnUnrequestableURL(t *testing.T) {
	t.Parallel()

	results := Run(context.Background(), []Target{{App: "npm", Region: "us", URL: "https://exa mple.invalid/\x7f"}}, Options{})

	if results[0].Err == nil {
		t.Fatalf("a malformed URL probed successfully: %+v", results[0])
	}
}

func TestRunWithNoTargets(t *testing.T) {
	t.Parallel()

	if got := Run(context.Background(), nil, Options{}); len(got) != 0 {
		t.Errorf("Run() with no targets returned %+v", got)
	}
}

func TestRunSendsTheUserAgent(t *testing.T) {
	t.Parallel()

	agents := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case agents <- r.Header.Get("User-Agent"):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	Run(context.Background(), []Target{{App: "npm", Region: "us", URL: server.URL}}, Options{UserAgent: "regtool/1.2.3"})

	if got := <-agents; got != "regtool/1.2.3" {
		t.Errorf("the probe sent User-Agent %q, want %q", got, "regtool/1.2.3")
	}
}

func TestRunUsesTheInjectedClient(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Request:    r,
		}, nil
	})}

	results := Run(context.Background(), []Target{{App: "npm", Region: "us", URL: "https://registry.invalid"}}, Options{Client: client})

	if !results[0].OK() {
		t.Errorf("the injected client's answer came back as %+v", results[0])
	}
	if calls.Load() != 1 {
		t.Errorf("the injected client was called %d times, want 1", calls.Load())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunFollowsRedirects(t *testing.T) {
	t.Parallel()

	final := newServer(t, http.StatusOK, 0)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	results := Run(context.Background(), []Target{{App: "npm", Region: "us", URL: redirector.URL}}, Options{})

	if !results[0].OK() || results[0].StatusCode != http.StatusOK {
		t.Errorf("a redirect was not followed: %+v", results[0])
	}
}

func TestRunDefaultsAreApplied(t *testing.T) {
	t.Parallel()

	got := Options{}.withDefaults()

	if got.Concurrency != DefaultConcurrency {
		t.Errorf("Concurrency defaulted to %d, want %d", got.Concurrency, DefaultConcurrency)
	}
	if got.Timeout != DefaultTimeout {
		t.Errorf("Timeout defaulted to %s, want %s", got.Timeout, DefaultTimeout)
	}
	if got.UserAgent != DefaultUserAgent {
		t.Errorf("UserAgent defaulted to %q, want %q", got.UserAgent, DefaultUserAgent)
	}
	if got.Client == nil {
		t.Fatal("no default client was built")
	}
	if got.Client.Timeout != DefaultTimeout {
		t.Errorf("the default client's timeout is %s, want %s", got.Client.Timeout, DefaultTimeout)
	}
}

func TestFastestPicksTheLowestLatency(t *testing.T) {
	t.Parallel()

	results := []Result{
		{Target: Target{Region: "slow"}, Latency: 300 * time.Millisecond, StatusCode: 200},
		{Target: Target{Region: "broken"}, Latency: time.Millisecond, StatusCode: 503},
		{Target: Target{Region: "dead"}, Latency: time.Microsecond, Err: errors.New("refused")},
		{Target: Target{Region: "quick"}, Latency: 20 * time.Millisecond, StatusCode: 404},
		{Target: Target{Region: "middling"}, Latency: 90 * time.Millisecond, StatusCode: 200},
	}

	best, ok := Fastest(results)
	if !ok {
		t.Fatal("Fastest() found nothing usable")
	}
	// "dead" and "broken" are quicker but unusable, so the quickest usable one
	// wins.
	if best.Region != "quick" {
		t.Errorf("Fastest() picked %q, want %q", best.Region, "quick")
	}
}

func TestFastestReportsWhenNothingIsReachable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []Result
	}{
		{name: "empty", results: nil},
		{
			name: "all failed",
			results: []Result{
				{Target: Target{Region: "us"}, Err: errors.New("refused")},
				{Target: Target{Region: "cn"}, StatusCode: 502},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if best, ok := Fastest(tt.results); ok {
				t.Errorf("Fastest() picked %+v, want nothing", best)
			}
		})
	}
}

func TestProbeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain", raw: "https://registry.npmjs.org", want: "https://registry.npmjs.org"},
		{name: "cargo sparse index", raw: "sparse+https://rsproxy.cn/index/", want: "https://rsproxy.cn/index/"},
		{name: "pip simple index", raw: "https://pypi.org/simple", want: "https://pypi.org/simple"},
		{name: "surrounding space", raw: "  https://goproxy.cn  ", want: "https://goproxy.cn"},
		{name: "no scheme", raw: "registry.npmmirror.com", want: "https://registry.npmmirror.com"},
		{name: "plain http is left alone", raw: "http://127.0.0.1:1", want: "http://127.0.0.1:1"},
		{name: "empty", raw: "   ", want: ""},
		{name: "sparse prefix only", raw: "sparse+", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProbeURL(tt.raw); got != tt.want {
				t.Errorf("ProbeURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestReasonClassifiesFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{name: "usable", result: Result{StatusCode: 200}, want: ""},
		{name: "server error", result: Result{StatusCode: 503}, want: "http 503"},
		{name: "deadline", result: Result{Err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded)}, want: "timed out"},
		{name: "cancelled", result: Result{Err: fmt.Errorf("wrapped: %w", context.Canceled)}, want: "cancelled"},
		{
			name:   "dns",
			result: Result{Err: fmt.Errorf("wrapped: %w", &net.DNSError{Err: "no such host", Name: "nope.invalid"})},
			want:   "dns lookup failed",
		},
		{
			name: "client timeout",
			result: Result{Err: fmt.Errorf("wrapped: %w", &net.OpError{
				Op:  "dial",
				Err: timeoutError{},
			})},
			want: "timed out",
		},
		{name: "plain error", result: Result{Err: errors.New("something else")}, want: "something else"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.Reason(); got != tt.want {
				t.Errorf("Reason() = %q, want %q", got, tt.want)
			}
		})
	}
}

// timeoutError is a net.Error that reports a timeout without wrapping
// context.DeadlineExceeded, the way http.Client.Timeout does.
type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }

func TestReasonStripsTheURLPrefix(t *testing.T) {
	t.Parallel()

	// A real transport failure arrives wrapped in a *url.Error whose message
	// repeats the whole URL; the reason must not.
	results := Run(context.Background(), []Target{{App: "npm", Region: "us", URL: refusedURL(t)}}, Options{})

	reason := results[0].Reason()
	if strings.Contains(reason, "http://127.0.0.1") {
		t.Errorf("Reason() repeated the URL: %q", reason)
	}
	if reason == "" {
		t.Error("Reason() was empty for a refused connection")
	}
}
