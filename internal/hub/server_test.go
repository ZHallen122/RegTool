package hub

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZHallen122/RegTool/internal/probe"
)

// testSources is a mirror list in the shape the CLI expects: regions at the top
// level, then app names, then a list of URLs.
const testSources = `{"cn":{"npm":["https://registry.npmmirror.com"]},"us":{"npm":["https://registry.npmjs.org"]}}`

// newTestServer wires a server over a temporary store and returns both, plus a
// client bound to an httptest listener.
func newTestServer(t *testing.T, ready func() bool) (*httptest.Server, *Store, *Metrics) {
	t.Helper()

	store := newTestStore(t)
	metrics := NewMetrics()
	server := NewServer(ServerConfig{
		Sources: []byte(testSources),
		Store:   store,
		Metrics: metrics,
		Ready:   ready,
	})

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, store, metrics
}

// discardLogger is the logger for the middleware tests, which care about the
// response rather than the log line.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// get issues a GET and returns the response with its body already read.
func get(t *testing.T, url string, headers map[string]string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", url, err)
	}
	return resp, string(body)
}

func TestGetSources(t *testing.T) {
	t.Parallel()

	ts, _, _ := newTestServer(t, nil)

	resp, body := get(t, ts.URL+"/v1/sources", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body != testSources {
		t.Errorf("body = %q, want the sources verbatim", body)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", got)
	}

	// A strong ETag: quoted, and with no weak validator prefix.
	tag := resp.Header.Get("ETag")
	if !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) || len(tag) < 10 {
		t.Fatalf("ETag = %q, want a quoted hash", tag)
	}
	if strings.HasPrefix(tag, "W/") {
		t.Errorf("ETag = %q, want a strong validator", tag)
	}

	// The body decodes into the shape the CLI unmarshals.
	var decoded map[string]map[string][]string
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the body is not a sources.json: %v", err)
	}
	if got := decoded["cn"]["npm"]; len(got) != 1 {
		t.Errorf("cn.npm = %v, want one mirror", got)
	}

	t.Run("if-none-match", func(t *testing.T) {
		conditional, body := get(t, ts.URL+"/v1/sources", map[string]string{"If-None-Match": tag})
		if conditional.StatusCode != http.StatusNotModified {
			t.Fatalf("status = %d, want 304", conditional.StatusCode)
		}
		if body != "" {
			t.Errorf("a 304 carried a body: %q", body)
		}
		if got := conditional.Header.Get("ETag"); got != tag {
			t.Errorf("the 304 came back with ETag %q, want %q", got, tag)
		}
	})

	t.Run("if-none-match with a stale tag", func(t *testing.T) {
		stale, body := get(t, ts.URL+"/v1/sources", map[string]string{"If-None-Match": `"0000"`})
		if stale.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200 for a stale tag", stale.StatusCode)
		}
		if body != testSources {
			t.Errorf("a stale tag got body %q", body)
		}
	})

	t.Run("if-none-match with a list", func(t *testing.T) {
		listed, _ := get(t, ts.URL+"/v1/sources", map[string]string{"If-None-Match": `"0000", ` + tag})
		if listed.StatusCode != http.StatusNotModified {
			t.Errorf("status = %d, want 304 when the list contains the tag", listed.StatusCode)
		}
	})

	t.Run("if-none-match with a star", func(t *testing.T) {
		star, _ := get(t, ts.URL+"/v1/sources", map[string]string{"If-None-Match": "*"})
		if star.StatusCode != http.StatusNotModified {
			t.Errorf("status = %d, want 304 for *", star.StatusCode)
		}
	})
}

func TestGetSourcesETagChangesWithTheBody(t *testing.T) {
	t.Parallel()

	first := NewServer(ServerConfig{Sources: []byte(testSources), Store: newTestStore(t), Metrics: NewMetrics()})
	second := NewServer(ServerConfig{Sources: []byte(`{"cn":{}}`), Store: newTestStore(t), Metrics: NewMetrics()})

	firstTS := httptest.NewServer(first.Handler())
	defer firstTS.Close()
	secondTS := httptest.NewServer(second.Handler())
	defer secondTS.Close()

	a, _ := get(t, firstTS.URL+"/v1/sources", nil)
	b, _ := get(t, secondTS.URL+"/v1/sources", nil)
	if a.Header.Get("ETag") == b.Header.Get("ETag") {
		t.Errorf("two different mirror lists share the ETag %q", a.Header.Get("ETag"))
	}
}

func TestGetHealthAfterACheck(t *testing.T) {
	t.Parallel()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()

	ts, store, metrics := newTestServer(t, nil)

	// Empty before anything has been checked, rather than an error.
	resp, body := get(t, ts.URL+"/v1/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 on an empty store", resp.StatusCode)
	}
	var empty healthResponse
	if err := json.Unmarshal([]byte(body), &empty); err != nil {
		t.Fatalf("decode: %v (body %q)", err, body)
	}
	if empty.Count != 0 || len(empty.Results) != 0 {
		t.Errorf("an unchecked hub reported %+v", empty)
	}

	checker := NewChecker(CheckerConfig{
		Targets: []probe.Target{{App: "npm", Region: "cn", URL: mirror.URL}},
		Store:   store,
		Metrics: metrics,
		Probe:   probe.Options{Timeout: 2 * time.Second},
	})
	if _, err := checker.CheckOnce(context.Background()); err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}

	resp, body = get(t, ts.URL+"/v1/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var checked healthResponse
	if err := json.Unmarshal([]byte(body), &checked); err != nil {
		t.Fatalf("decode: %v (body %q)", err, body)
	}
	if checked.Count != 1 || len(checked.Results) != 1 {
		t.Fatalf("/v1/health reported %+v, want one mirror", checked)
	}

	row := checked.Results[0]
	if row.App != "npm" || row.Region != "cn" || row.URL != mirror.URL {
		t.Errorf("the row identifies the wrong mirror: %+v", row)
	}
	if !row.OK || row.StatusCode != http.StatusOK {
		t.Errorf("the healthy mirror came back as %+v", row)
	}
	if row.CheckedAt.IsZero() {
		t.Error("checked_at is missing from the response")
	}

	// The contract with anything reading this endpoint is the JSON field
	// names, so check them rather than only the decoded struct.
	for _, field := range []string{`"app"`, `"region"`, `"url"`, `"ok"`, `"latency_ms"`, `"status_code"`, `"error"`, `"checked_at"`} {
		if !strings.Contains(body, field) {
			t.Errorf("the response is missing the %s field: %s", field, body)
		}
	}
}

func TestGetHistory(t *testing.T) {
	t.Parallel()

	ts, store, _ := newTestServer(t, nil)

	var rows []Result
	for i := range 5 {
		rows = append(rows, Result{
			App: "npm", Region: "cn", URL: "https://a.example",
			OK: true, LatencyMS: int64(i), StatusCode: 200,
			CheckedAt: baseTime.Add(time.Duration(i) * time.Minute),
		})
	}
	rows = append(rows, Result{App: "pip", Region: "us", URL: "https://b.example", OK: false, CheckedAt: baseTime})
	if err := store.Insert(context.Background(), rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	decode := func(t *testing.T, url string) healthResponse {
		t.Helper()
		resp, body := get(t, url, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, body %s", url, resp.StatusCode, body)
		}
		var out healthResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode %s: %v (body %q)", url, err, body)
		}
		return out
	}

	t.Run("default", func(t *testing.T) {
		got := decode(t, ts.URL+"/v1/health/history")
		if got.Count != 6 {
			t.Errorf("count = %d, want every row", got.Count)
		}
		for i := 1; i < len(got.Results); i++ {
			if got.Results[i].CheckedAt.After(got.Results[i-1].CheckedAt) {
				t.Fatalf("the history is not newest first around row %d", i)
			}
		}
	})

	t.Run("filtered", func(t *testing.T) {
		got := decode(t, ts.URL+"/v1/health/history?app=npm&region=cn")
		if got.Count != 5 {
			t.Errorf("count = %d, want the 5 npm/cn rows", got.Count)
		}
	})

	t.Run("limit", func(t *testing.T) {
		got := decode(t, ts.URL+"/v1/health/history?limit=2")
		if got.Count != 2 {
			t.Errorf("count = %d, want 2", got.Count)
		}
	})

	t.Run("limit clamps to the maximum", func(t *testing.T) {
		// Far over the cap; the request succeeds with what there is rather
		// than failing, because asking for more than the cap is not an error.
		got := decode(t, ts.URL+"/v1/health/history?limit=99999")
		if got.Count != 6 {
			t.Errorf("count = %d, want every row", got.Count)
		}
	})

	t.Run("limit clamps to at least one", func(t *testing.T) {
		for _, raw := range []string{"0", "-5"} {
			got := decode(t, ts.URL+"/v1/health/history?limit="+raw)
			if got.Count != 1 {
				t.Errorf("limit=%s returned %d rows, want 1", raw, got.Count)
			}
		}
	})

	t.Run("limit must be a number", func(t *testing.T) {
		resp, body := get(t, ts.URL+"/v1/health/history?limit=lots", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		var out errorResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("the 400 is not JSON: %v (%q)", err, body)
		}
		if out.Error == "" {
			t.Error("the 400 carries no message")
		}
	})
}

func TestLivenessAndReadiness(t *testing.T) {
	t.Parallel()

	ready := false
	ts, _, _ := newTestServer(t, func() bool { return ready })

	// Liveness does not wait for the first check: the process is up.
	if resp, _ := get(t, ts.URL+"/healthz", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz = %d before the first check, want 200", resp.StatusCode)
	}
	resp, body := get(t, ts.URL+"/readyz", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d before the first check, want 503", resp.StatusCode)
	}
	if !strings.Contains(body, "unready") {
		t.Errorf("/readyz body = %q", body)
	}

	ready = true
	if resp, _ := get(t, ts.URL+"/readyz", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("/readyz = %d after the first check, want 200", resp.StatusCode)
	}
}

func TestUnknownRouteIs404JSON(t *testing.T) {
	t.Parallel()

	ts, _, _ := newTestServer(t, nil)

	resp, body := get(t, ts.URL+"/v1/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json even for a 404", got)
	}
	var out errorResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("the 404 is not JSON: %v (%q)", err, body)
	}
	if !strings.Contains(out.Error, "/v1/nope") {
		t.Errorf("the 404 message %q does not name the path", out.Error)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	t.Parallel()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()

	ts, store, metrics := newTestServer(t, nil)

	checker := NewChecker(CheckerConfig{
		Targets: []probe.Target{{App: "npm", Region: "cn", URL: mirror.URL}},
		Store:   store,
		Metrics: metrics,
		Probe:   probe.Options{Timeout: 2 * time.Second},
	})
	if _, err := checker.CheckOnce(context.Background()); err != nil {
		t.Fatalf("CheckOnce: %v", err)
	}

	// A request the scrape itself can report on: the HTTP metrics are labelled
	// per route, so they have no series at all until something has been served.
	get(t, ts.URL+"/v1/sources", nil)

	resp, body := get(t, ts.URL+"/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	for _, name := range []string{
		"regtool_hub_mirror_up",
		"regtool_hub_mirror_latency_seconds",
		"regtool_hub_checks_total",
		"regtool_hub_check_duration_seconds",
		"regtool_hub_http_requests_total",
		"regtool_hub_http_request_duration_seconds",
		// The runtime and process collectors are registered too, so a
		// dashboard can see memory and file descriptors without another
		// exporter.
		"go_goroutines",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics is missing %s", name)
		}
	}

	// The gauge carries the mirror's identity, not just a number.
	if !strings.Contains(body, `regtool_hub_mirror_up{app="npm",region="cn",url="`+mirror.URL+`"} 1`) {
		t.Errorf("regtool_hub_mirror_up is not labelled with the mirror that was checked:\n%s", body)
	}
}

func TestHTTPMetricsAreLabelledByRoute(t *testing.T) {
	t.Parallel()

	ts, _, _ := newTestServer(t, nil)

	get(t, ts.URL+"/v1/sources", nil)
	// Two different unknown paths must collapse to one series, or a scanner
	// walking the URL space would mint one per path it tries.
	get(t, ts.URL+"/nope/one", nil)
	get(t, ts.URL+"/nope/two", nil)

	_, body := get(t, ts.URL+"/metrics", nil)
	if !strings.Contains(body, `regtool_hub_http_requests_total{code="200",method="GET",route="GET /v1/sources"} 1`) {
		t.Errorf("the request counter is not labelled by route:\n%s", body)
	}
	if !strings.Contains(body, `route="`+routeUnmatched+`"} 2`) {
		t.Errorf("unknown paths did not collapse into one %q series:\n%s", routeUnmatched, body)
	}
}

func TestRequestIDIsEchoedOrMinted(t *testing.T) {
	t.Parallel()

	ts, _, _ := newTestServer(t, nil)

	minted, _ := get(t, ts.URL+"/healthz", nil)
	if minted.Header.Get(requestIDHeader) == "" {
		t.Error("no request id was minted")
	}

	echoed, _ := get(t, ts.URL+"/healthz", map[string]string{requestIDHeader: "trace-me"})
	if got := echoed.Header.Get(requestIDHeader); got != "trace-me" {
		t.Errorf("%s = %q, want the caller's own id back", requestIDHeader, got)
	}
}

func TestRecoveryTurnsAPanicIntoA500(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	handler := withRequestID(withObservability(withRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), discardLogger()), metrics, discardLogger()))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/sources", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a panicking handler produced %d, want 500", recorder.Code)
	}
}

// TestServeShutsDownGracefully is the check that a SIGTERM does not drop the
// listener on the floor: Serve must return once its context is cancelled.
func TestServeShutsDownGracefully(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerConfig{
		Sources: []byte(testSources),
		Store:   newTestStore(t),
		Metrics: NewMetrics(),
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener, 5*time.Second) }()

	// The server is really serving before the shutdown is asked for.
	if resp, _ := get(t, "http://"+addr+"/healthz", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz on the live server = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}

	// The listener is closed, so nothing answers on the address any more.
	if _, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		t.Error("the listener is still accepting connections after shutdown")
	}
}

func TestListenAndServeReportsABadAddress(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerConfig{
		Addr:    "127.0.0.1:not-a-port",
		Sources: []byte(testSources),
		Store:   newTestStore(t),
		Metrics: NewMetrics(),
	})
	if err := server.ListenAndServe(context.Background(), time.Second); err == nil {
		t.Error("ListenAndServe on an unusable address returned nil")
	}
}

func TestMatchesETag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		header string
		tag    string
		want   bool
	}{
		{"", `"a"`, false},
		{"*", `"a"`, true},
		{`"a"`, `"a"`, true},
		{`"b"`, `"a"`, false},
		{`"a", "b"`, `"b"`, true},
		{` "a" `, `"a"`, true},
		// RFC 9110 says If-None-Match uses the weak comparison, so a weak tag
		// and its strong twin match.
		{`W/"a"`, `"a"`, true},
	}
	for _, test := range tests {
		if got := matchesETag(test.header, test.tag); got != test.want {
			t.Errorf("matchesETag(%q, %q) = %v, want %v", test.header, test.tag, got, test.want)
		}
	}
}
