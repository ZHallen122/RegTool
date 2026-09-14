package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZHallen122/RegTool/internal/backend"
	"github.com/ZHallen122/RegTool/internal/history"
	"github.com/ZHallen122/RegTool/internal/probe"
	"github.com/ZHallen122/RegTool/source/structs"
)

// probeServer starts a server that answers with status after waiting delay.
func probeServer(t *testing.T, status int, delay time.Duration) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// deadURL returns a URL on a port nothing listens on.
func deadURL(t *testing.T) string {
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

// probeService builds a Service whose npm and yarn mirrors are the given URLs,
// one per region, and returns it with the home directory its backends write to.
func probeService(t *testing.T, us, cn, eu string) (*Service, string) {
	t.Helper()

	env, root := testEnv(t)
	sources := &structs.RegistrySources{
		structs.US: {"npm": {us}, "yarn": {us}},
		structs.CN: {"npm": {cn}, "yarn": {cn}},
		structs.EU: {"npm": {eu}, "yarn": {eu}},
	}
	store := history.New(filepath.Join(root, "history"))
	return New(sources, backend.All(env), store, WithProbeOptions(probe.Options{Timeout: 2 * time.Second})), env.Home
}

// findReport returns the npm report for one region.
func findReport(t *testing.T, reports []ProbeReport, region string) ProbeReport {
	t.Helper()

	for _, report := range reports {
		if report.App == "npm" && report.Region == region {
			return report
		}
	}
	t.Fatalf("Doctor() returned no npm report for %s: %+v", region, reports)
	return ProbeReport{}
}

func TestDoctorReportsEveryMirrorOfEveryRegion(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)
	slow := probeServer(t, http.StatusNotFound, 120*time.Millisecond)
	dead := deadURL(t)

	svc, _ := probeService(t, fast, slow, dead)

	reports, err := svc.Doctor(context.Background(), []string{"npm"})
	if err != nil {
		t.Fatalf("Doctor() returned an unexpected error: %v", err)
	}
	if len(reports) != 3 {
		t.Fatalf("Doctor() returned %d reports, want one per region: %+v", len(reports), reports)
	}

	// Quickest first, then the other reachable one, then the dead one.
	wantOrder := []string{"us", "cn", "eu"}
	for i, region := range wantOrder {
		if reports[i].Region != region {
			t.Errorf("report %d is for %s, want %s (full order: %+v)", i, reports[i].Region, region, reports)
		}
	}

	if got := findReport(t, reports, "us"); got.Status != probeStatusOK || got.Latency < 0 {
		t.Errorf("the fast mirror reported as %+v", got)
	}
	// A 404 is still a reachable mirror.
	if got := findReport(t, reports, "cn"); got.Status != probeStatusOK || got.StatusCode != http.StatusNotFound {
		t.Errorf("a 404 mirror reported as %+v", got)
	}

	broken := findReport(t, reports, "eu")
	if broken.Status != probeStatusError {
		t.Errorf("the dead mirror reported as %+v", broken)
	}
	if broken.Err == nil {
		t.Error("the dead mirror carries no error")
	}
	if broken.Reason == "" {
		t.Error("the dead mirror carries no short reason")
	}
	if broken.URL != dead {
		t.Errorf("the dead mirror reports URL %q, want %q", broken.URL, dead)
	}
}

func TestDoctorMarksAServerErrorUnusable(t *testing.T) {
	broken := probeServer(t, http.StatusBadGateway, 0)
	ok := probeServer(t, http.StatusOK, 0)

	svc, _ := probeService(t, broken, ok, ok)

	reports, err := svc.Doctor(context.Background(), []string{"npm"})
	if err != nil {
		t.Fatalf("Doctor() returned an unexpected error: %v", err)
	}

	got := findReport(t, reports, "us")
	if got.Status != probeStatusError {
		t.Errorf("a 502 reported as %+v", got)
	}
	if got.Err == nil {
		t.Error("a 502 produced no error to explain itself")
	}
	if got.Reason != "http 502" {
		t.Errorf("a 502 reads as %q, want %q", got.Reason, "http 502")
	}
}

func TestDoctorCoversEveryKnownAppByDefault(t *testing.T) {
	ok := probeServer(t, http.StatusOK, 0)
	svc, _ := probeService(t, ok, ok, ok)

	reports, err := svc.Doctor(context.Background(), nil)
	if err != nil {
		t.Fatalf("Doctor() returned an unexpected error: %v", err)
	}

	// The fixture only ships npm and yarn mirrors, and yarn-berry shares yarn's
	// key, so three apps are covered even though nothing is installed.
	apps := make(map[string]int)
	for _, report := range reports {
		apps[report.App]++
	}
	for _, app := range []string{"npm", "yarn", "yarn-berry"} {
		if apps[app] != 3 {
			t.Errorf("Doctor() produced %d reports for %s, want 3: %+v", apps[app], app, reports)
		}
	}
	// Reports stay grouped: every row for one app before the next app starts.
	seen := make(map[string]bool)
	last := ""
	for _, report := range reports {
		if report.App != last {
			if seen[report.App] {
				t.Fatalf("Doctor() interleaved the apps: %+v", reports)
			}
			seen[report.App], last = true, report.App
		}
	}
}

// helm and docker are probed like any other app: their mirrors are ordinary
// URLs and need no special case anywhere in Doctor.
func TestDoctorCoversHelmAndDocker(t *testing.T) {
	ok := probeServer(t, http.StatusOK, 0)
	env, root := testEnv(t)
	sources := &structs.RegistrySources{
		structs.US: {"helm": {ok}, "docker": {ok}},
		structs.CN: {"helm": {ok}, "docker": {ok}},
		structs.EU: {"helm": {ok}, "docker": {ok}},
	}
	svc := New(sources, backend.All(env), history.New(filepath.Join(root, "history")),
		WithProbeOptions(probe.Options{Timeout: 2 * time.Second}))

	reports, err := svc.Doctor(context.Background(), []string{"charts", "dockerd"})
	if err != nil {
		t.Fatalf("Doctor() returned an unexpected error: %v", err)
	}

	apps := make(map[string]int)
	for _, report := range reports {
		if !report.OK() {
			t.Errorf("Doctor() called a live mirror unusable: %+v", report)
		}
		apps[report.App]++
	}
	for _, app := range []string{"helm", "docker"} {
		if apps[app] != 3 {
			t.Errorf("Doctor() produced %d reports for %s, want one per region: %+v", apps[app], app, reports)
		}
	}
}

func TestDoctorRejectsAnUnknownApp(t *testing.T) {
	ok := probeServer(t, http.StatusOK, 0)
	svc, _ := probeService(t, ok, ok, ok)

	if _, err := svc.Doctor(context.Background(), []string{"deno"}); err == nil {
		t.Fatal("Doctor() accepted an app regtool has no backend for")
	}
}

func TestDoctorStopsOnACancelledContext(t *testing.T) {
	ok := probeServer(t, http.StatusOK, 0)
	svc, _ := probeService(t, ok, ok, ok)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.Doctor(ctx, nil); err == nil {
		t.Fatal("Doctor() ran with a cancelled context")
	}
}

func TestProbeReportSurvivesJSON(t *testing.T) {
	dead := deadURL(t)
	ok := probeServer(t, http.StatusOK, 0)
	svc, _ := probeService(t, ok, dead, dead)

	reports, err := svc.Doctor(context.Background(), []string{"npm"})
	if err != nil {
		t.Fatalf("Doctor() returned an unexpected error: %v", err)
	}

	raw, err := json.Marshal(reports)
	if err != nil {
		t.Fatalf("failed to marshal the reports: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal the reports: %v", err)
	}
	if len(decoded) != 3 {
		t.Fatalf("the JSON holds %d reports, want 3", len(decoded))
	}
	if _, has := decoded[0]["latencyMs"]; !has {
		t.Errorf("a report went to JSON without a latency: %v", decoded[0])
	}
	if decoded[0]["status"] != probeStatusOK {
		t.Errorf("the first report is %v, want the reachable one first", decoded[0])
	}
	// The dead ones carry their failure as a plain string.
	if text, _ := decoded[2]["error"].(string); text == "" {
		t.Errorf("a failed report went to JSON without an error: %v", decoded[2])
	}
}

func TestUseFastestPicksTheQuickestRegionPerApp(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)
	slow := probeServer(t, http.StatusOK, 150*time.Millisecond)
	dead := deadURL(t)

	// us is quickest, cn is slow, eu is dead.
	svc, home := probeService(t, fast, slow, dead)
	npmrc := filepath.Join(home, ".npmrc")
	writeFile(t, npmrc, "registry="+slow+"\n")

	result, err := svc.UseFastest(context.Background(), []string{"npm"}, false)
	if err != nil {
		t.Fatalf("UseFastest() returned an unexpected error: %v", err)
	}

	change := findChange(t, result, "npm")
	if change.Region != "us" {
		t.Errorf("UseFastest() chose region %q, want us", change.Region)
	}
	if change.Latency < 0 {
		t.Error("UseFastest() reported a negative latency for the region it chose")
	}
	if change.To != fast {
		t.Errorf("UseFastest() pointed npm at %q, want %q", change.To, fast)
	}
	if got := readFile(t, npmrc); !strings.Contains(got, fast) {
		t.Errorf(".npmrc is %q, want the fast mirror", got)
	}
	if result.SnapshotID == "" {
		t.Error("UseFastest() applied a change without taking a snapshot")
	}

	snapshots, err := svc.History(context.Background())
	if err != nil {
		t.Fatalf("History() returned an unexpected error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("UseFastest() took %d snapshots, want one for the whole run", len(snapshots))
	}
	if snapshots[0].Note != "use --fastest" {
		t.Errorf("the snapshot is noted %q, want %q", snapshots[0].Note, "use --fastest")
	}
}

func TestUseFastestTakesOneSnapshotForEveryApp(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)
	dead := deadURL(t)

	svc, home := probeService(t, fast, dead, dead)
	npmrc := filepath.Join(home, ".npmrc")
	yarnrc := filepath.Join(home, ".yarnrc")
	writeFile(t, npmrc, "registry=https://registry.npmjs.org\n")
	writeFile(t, yarnrc, "registry \"https://registry.npmjs.org\"\n")

	result, err := svc.UseFastest(context.Background(), []string{"npm", "yarn"}, false)
	if err != nil {
		t.Fatalf("UseFastest() returned an unexpected error: %v", err)
	}
	if len(result.Changes) != 2 {
		t.Fatalf("UseFastest() changed %d apps, want 2: %+v", len(result.Changes), result.Changes)
	}

	snapshots, err := svc.History(context.Background())
	if err != nil {
		t.Fatalf("History() returned an unexpected error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("UseFastest() took %d snapshots, want one for the whole run", len(snapshots))
	}
	if len(snapshots[0].Files) != 2 {
		t.Errorf("the snapshot captured %d files, want both: %+v", len(snapshots[0].Files), snapshots[0].Files)
	}
}

func TestUseFastestDryRunWritesNothing(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)
	slow := probeServer(t, http.StatusOK, 150*time.Millisecond)

	svc, home := probeService(t, fast, slow, slow)
	npmrc := filepath.Join(home, ".npmrc")
	const before = "registry=https://registry.npmjs.org\n"
	writeFile(t, npmrc, before)

	result, err := svc.UseFastest(context.Background(), []string{"npm"}, true)
	if err != nil {
		t.Fatalf("UseFastest() returned an unexpected error: %v", err)
	}

	if !result.DryRun {
		t.Error("UseFastest() did not mark the result as a dry run")
	}
	if result.SnapshotID != "" {
		t.Errorf("a dry run took snapshot %q", result.SnapshotID)
	}
	change := findChange(t, result, "npm")
	if change.Region != "us" {
		t.Errorf("UseFastest() chose region %q, want us", change.Region)
	}
	if change.Diff == "" {
		t.Error("a dry run produced no diff")
	}
	if got := readFile(t, npmrc); got != before {
		t.Errorf("a dry run rewrote .npmrc to %q", got)
	}
}

func TestUseFastestReportsAnAppWithNoReachableMirror(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)
	dead := deadURL(t)

	env, root := testEnv(t)
	// npm has one live mirror; yarn has none anywhere.
	sources := &structs.RegistrySources{
		structs.US: {"npm": {fast}, "yarn": {dead}},
		structs.CN: {"npm": {fast}, "yarn": {dead}},
		structs.EU: {"npm": {fast}, "yarn": {dead}},
	}
	svc := New(sources, backend.All(env), history.New(filepath.Join(root, "history")),
		WithProbeOptions(probe.Options{Timeout: 2 * time.Second}))

	npmrc := filepath.Join(env.Home, ".npmrc")
	writeFile(t, npmrc, "registry=https://registry.npmjs.org\n")
	writeFile(t, filepath.Join(env.Home, ".yarnrc"), "registry \"https://registry.npmjs.org\"\n")

	result, err := svc.UseFastest(context.Background(), []string{"npm", "yarn"}, false)
	if err == nil {
		t.Fatal("UseFastest() succeeded even though yarn had no reachable mirror")
	}

	yarn := findChange(t, result, "yarn")
	if yarn.Err == nil {
		t.Error("UseFastest() did not record the failure against yarn")
	} else if !strings.Contains(yarn.Err.Error(), "no reachable yarn mirror") {
		t.Errorf("yarn failed with %q, want an explanation naming every region tried", yarn.Err)
	}

	// The other app was still switched, which is the point.
	if npm := findChange(t, result, "npm"); npm.Err != nil {
		t.Errorf("UseFastest() failed npm too: %v", npm.Err)
	}
	if got := readFile(t, npmrc); !strings.Contains(got, fast) {
		t.Errorf("npm was left at %q even though only yarn had no mirror", got)
	}
}

func TestUseFastestReportsAnAppWithNoMirrorAtAll(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)

	env, root := testEnv(t)
	sources := &structs.RegistrySources{
		structs.US: {"npm": {fast}},
		structs.CN: {"npm": {fast}},
		structs.EU: {"npm": {fast}},
	}
	svc := New(sources, backend.All(env), history.New(filepath.Join(root, "history")),
		WithProbeOptions(probe.Options{Timeout: 2 * time.Second}))

	result, err := svc.UseFastest(context.Background(), []string{"gem"}, true)
	if err == nil {
		t.Fatal("UseFastest() succeeded for an app with no mirrors at all")
	}
	change := findChange(t, result, "gem")
	if change.Err == nil || !strings.Contains(change.Err.Error(), "no gem mirror is known") {
		t.Errorf("gem failed with %v, want an explanation that no mirror is known", change.Err)
	}
}

func TestUseFastestRejectsAnUnknownApp(t *testing.T) {
	ok := probeServer(t, http.StatusOK, 0)
	svc, _ := probeService(t, ok, ok, ok)

	result, err := svc.UseFastest(context.Background(), []string{"deno"}, true)
	if err == nil {
		t.Fatal("UseFastest() accepted an app regtool has no backend for")
	}
	if result != nil {
		t.Errorf("UseFastest() returned a result alongside a validation error: %+v", result)
	}
}

func TestUseFastestUsesTheInjectedClient(t *testing.T) {
	// A client that answers everything instantly, and faster for cn, proves the
	// probe options really are what the service probes with.
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "slow") {
			time.Sleep(80 * time.Millisecond)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: r}, nil
	})}

	env, root := testEnv(t)
	sources := &structs.RegistrySources{
		structs.US: {"npm": {"https://slow.invalid"}},
		structs.CN: {"npm": {"https://quick.invalid"}},
		structs.EU: {"npm": {"https://slow.invalid"}},
	}
	svc := New(sources, backend.All(env), history.New(filepath.Join(root, "history")),
		WithProbeOptions(probe.Options{Client: client, Concurrency: 4}))

	writeFile(t, filepath.Join(env.Home, ".npmrc"), "registry=https://registry.npmjs.org\n")

	result, err := svc.UseFastest(context.Background(), []string{"npm"}, true)
	if err != nil {
		t.Fatalf("UseFastest() returned an unexpected error: %v", err)
	}
	if change := findChange(t, result, "npm"); change.Region != "cn" {
		t.Errorf("UseFastest() chose %q through the injected client, want cn", change.Region)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUseDoesNotReportARegionItWasGiven(t *testing.T) {
	svc, home := newTestService(t)
	writeFile(t, filepath.Join(home, ".npmrc"), "registry="+npmUS+"\n")

	result, err := svc.Use(context.Background(), "cn", []string{"npm"}, true)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}

	change := findChange(t, result, "npm")
	if change.Region != "" || change.Latency != 0 {
		t.Errorf("Use() filled in the probing fields: region %q, latency %s", change.Region, change.Latency)
	}

	raw, err := json.Marshal(change)
	if err != nil {
		t.Fatalf("failed to marshal the change: %v", err)
	}
	for _, field := range []string{"region", "latencyMs"} {
		if strings.Contains(string(raw), fmt.Sprintf("%q", field)) {
			t.Errorf("a plain use emitted %q in its JSON: %s", field, raw)
		}
	}
}

func TestUseFastestJSONCarriesTheRegionAndLatency(t *testing.T) {
	fast := probeServer(t, http.StatusOK, 0)
	svc, home := probeService(t, fast, fast, fast)
	writeFile(t, filepath.Join(home, ".npmrc"), "registry=https://registry.npmjs.org\n")

	result, err := svc.UseFastest(context.Background(), []string{"npm"}, true)
	if err != nil {
		t.Fatalf("UseFastest() returned an unexpected error: %v", err)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal the result: %v", err)
	}
	var decoded struct {
		Changes []struct {
			Region    string  `json:"region"`
			LatencyMs float64 `json:"latencyMs"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal the result: %v", err)
	}
	if len(decoded.Changes) != 1 {
		t.Fatalf("the JSON holds %d changes, want 1: %s", len(decoded.Changes), raw)
	}
	if decoded.Changes[0].Region == "" {
		t.Errorf("the JSON carries no chosen region: %s", raw)
	}
}
