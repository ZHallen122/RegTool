package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZHallen122/RegTool/internal/backend"
	"github.com/ZHallen122/RegTool/internal/history"
	"github.com/ZHallen122/RegTool/source/localdata"
	"github.com/ZHallen122/RegTool/source/structs"
)

const (
	npmUS = "https://registry.npmjs.org"
	npmCN = "https://registry.npmmirror.com"
	gemUS = "https://rubygems.org/"
	gemCN = "https://mirrors.tuna.tsinghua.edu.cn/rubygems/"
	goCN  = "https://goproxy.cn"

	helmUS   = "https://charts.helm.sh/stable"
	helmCN   = "https://mirror.azure.cn/kubernetes/charts"
	dockerUS = "https://registry-1.docker.io"
	dockerCN = "https://docker.m.daocloud.io"
)

// testSources mirrors the shape of the bundled sources.json, with just enough
// entries for the tests. The eu region deliberately ships no gem mirror so the
// "region has no mirror for this app" path can be exercised.
func testSources() *structs.RegistrySources {
	return &structs.RegistrySources{
		structs.US: {
			"npm":                    {npmUS},
			"yarn":                   {npmUS},
			"gem":                    {gemUS},
			"go":                     {"https://proxy.golang.org"},
			"cargo":                  {"sparse+https://index.crates.io/"},
			"helm":                   {helmUS},
			"docker":                 {dockerUS},
			"homebrew_bottle_domain": {"https://ghcr.io/v2/homebrew/core"},
		},
		structs.CN: {
			"npm":                    {npmCN},
			"yarn":                   {npmCN},
			"gem":                    {gemCN},
			"go":                     {goCN},
			"cargo":                  {"sparse+https://rsproxy.cn/index/"},
			"helm":                   {helmCN},
			"docker":                 {dockerCN},
			"homebrew_bottle_domain": {"https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles"},
		},
		structs.EU: {
			"npm": {npmUS},
		},
	}
}

// testEnv is a backend environment rooted entirely in a temporary directory, so
// the real backends can be exercised without touching the developer's home.
func testEnv(t *testing.T) (backend.Env, string) {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "config")
	for _, dir := range []string{home, config} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}
	// Every variable but one reads as empty, which keeps the backends off the
	// ambient NPM_CONFIG_USERCONFIG, GOENV, CARGO_HOME and friends. The
	// exception is docker, whose daemon.json lives under /etc on Linux: a test
	// must neither read the machine's own file nor try to write it.
	daemonJSON := filepath.Join(root, "docker", "daemon.json")
	getenv := func(key string) string {
		if key == backend.DockerDaemonJSONEnvVar {
			return daemonJSON
		}
		return ""
	}
	return backend.Env{Home: home, ConfigDir: config, Getenv: getenv}, root
}

// newTestService builds a Service whose backends and snapshots all live under a
// temporary directory, and returns it together with the home directory the
// configuration files go in.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()

	env, root := testEnv(t)
	store := history.New(filepath.Join(root, "history"))
	return New(testSources(), backend.All(env), store), env.Home
}

// writeFile creates a configuration file under the test home.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create the parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// readFile returns the content of a file that must exist.
func readFile(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(raw)
}

// findChange returns the result for one app.
func findChange(t *testing.T, result *UseResult, app string) ChangeResult {
	t.Helper()

	for _, change := range result.Changes {
		if change.App == app {
			return change
		}
	}
	t.Fatalf("Use() returned no result for %s: %+v", app, result.Changes)
	return ChangeResult{}
}

func TestUseDryRunWritesNothingAndTakesNoSnapshot(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	writeFile(t, npmrc, "registry="+npmUS+"\n")

	result, err := svc.Use(context.Background(), "cn", []string{"npm"}, true)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}

	if !result.DryRun {
		t.Error("Use() did not mark the result as a dry run")
	}
	if result.SnapshotID != "" {
		t.Errorf("a dry run took snapshot %q", result.SnapshotID)
	}
	change := findChange(t, result, "npm")
	if change.From != npmUS || change.To != npmCN {
		t.Errorf("Use() reported %q -> %q, want %q -> %q", change.From, change.To, npmUS, npmCN)
	}
	if change.Noop {
		t.Error("Use() called a real change a no-op")
	}
	if !strings.Contains(change.Diff, "-registry="+npmUS) || !strings.Contains(change.Diff, "+registry="+npmCN) {
		t.Errorf("Use() produced an unhelpful diff:\n%s", change.Diff)
	}
	if change.Path != npmrc {
		t.Errorf("Use() reported path %q, want %q", change.Path, npmrc)
	}

	if got := readFile(t, npmrc); got != "registry="+npmUS+"\n" {
		t.Errorf("a dry run rewrote .npmrc to %q", got)
	}
	snapshots, err := svc.History(context.Background())
	if err != nil {
		t.Fatalf("History() returned an unexpected error: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("a dry run left %d snapshots behind", len(snapshots))
	}
}

func TestUseSnapshotsThenApplies(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	gemrc := filepath.Join(home, ".gemrc")
	writeFile(t, npmrc, "registry="+npmUS+"\n")
	writeFile(t, gemrc, "---\n:sources:\n  - "+gemUS+"\n")

	result, err := svc.Use(context.Background(), "cn", nil, false)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}

	if result.SnapshotID == "" {
		t.Fatal("Use() applied a change without taking a snapshot")
	}
	if got := readFile(t, npmrc); !strings.Contains(got, npmCN) {
		t.Errorf(".npmrc is %q, want the cn mirror", got)
	}
	if got := readFile(t, gemrc); !strings.Contains(got, gemCN) {
		t.Errorf(".gemrc is %q, want the cn mirror", got)
	}

	snapshots, err := svc.History(context.Background())
	if err != nil {
		t.Fatalf("History() returned an unexpected error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("Use() took %d snapshots, want 1", len(snapshots))
	}
	if snapshots[0].Note != "use cn" {
		t.Errorf("the snapshot is noted %q, want %q", snapshots[0].Note, "use cn")
	}
	if len(snapshots[0].Files) != 2 {
		t.Errorf("the snapshot captured %d files, want 2: %+v", len(snapshots[0].Files), snapshots[0].Files)
	}
}

func TestUseIsANoopWhenTheMirrorIsAlreadySet(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	writeFile(t, npmrc, "registry="+npmCN+"\n")

	result, err := svc.Use(context.Background(), "cn", []string{"npm"}, false)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}

	change := findChange(t, result, "npm")
	if !change.Noop {
		t.Errorf("Use() did not report a no-op: %+v", change)
	}
	if change.Diff != "" {
		t.Errorf("a no-op produced a diff:\n%s", change.Diff)
	}
	if result.SnapshotID != "" {
		t.Errorf("a run with nothing to do took snapshot %q", result.SnapshotID)
	}
}

func TestUseAppliesTheOtherAppsWhenOneFails(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	writeFile(t, npmrc, "registry="+npmUS+"\n")
	// A directory where gem expects its configuration file: the backend still
	// detects gem, but reading the file fails and so does planning.
	if err := os.MkdirAll(filepath.Join(home, ".gemrc"), 0o755); err != nil {
		t.Fatalf("failed to create the fake .gemrc directory: %v", err)
	}

	result, err := svc.Use(context.Background(), "cn", nil, false)
	if err == nil {
		t.Fatal("Use() succeeded even though one backend could not be planned")
	}
	if change := findChange(t, result, "gem"); change.Err == nil {
		t.Error("Use() did not record the failure against gem")
	}
	if change := findChange(t, result, "npm"); change.Err != nil {
		t.Errorf("Use() reported an error for npm: %v", change.Err)
	}
	if got := readFile(t, npmrc); !strings.Contains(got, npmCN) {
		t.Errorf("npm was left at %q even though only the other backend failed", got)
	}
	if result.SnapshotID == "" {
		t.Error("Use() applied changes without taking a snapshot")
	}
}

// unwritableBackend plans a change whose target path is a directory, so the
// change can be computed but neither snapshotted nor written.
type unwritableBackend struct{ dir string }

func (b unwritableBackend) Name() string             { return "unwritable" }
func (b unwritableBackend) ConfigPath() string       { return b.dir }
func (b unwritableBackend) Detect() (bool, error)    { return true, nil }
func (b unwritableBackend) Current() (string, error) { return "https://broken.example", nil }

func (b unwritableBackend) Plan(target string) (*backend.Plan, error) {
	return &backend.Plan{
		Backend: b.Name(),
		Path:    b.dir,
		Existed: true,
		Before:  []byte("before\n"),
		After:   []byte("after\n"),
		From:    "https://broken.example",
		To:      target,
	}, nil
}

func TestUseChangesNothingWhenTheSnapshotCannotBeTaken(t *testing.T) {
	env, root := testEnv(t)
	broken := unwritableBackend{dir: filepath.Join(root, "a-directory")}
	if err := os.MkdirAll(broken.dir, 0o755); err != nil {
		t.Fatalf("failed to create the unwritable backend's directory: %v", err)
	}
	sources := testSources()
	for _, region := range []structs.Region{structs.US, structs.CN} {
		(*sources)[region][broken.Name()] = []string{"https://broken.example/" + string(region)}
	}
	svc := New(sources, append(backend.All(env), broken), history.New(filepath.Join(root, "history")))

	npmrc := filepath.Join(env.Home, ".npmrc")
	const before = "registry=" + npmUS + "\n"
	writeFile(t, npmrc, before)

	result, err := svc.Use(context.Background(), "cn", []string{"npm", broken.Name()}, false)
	if err == nil {
		t.Fatal("Use() succeeded even though the snapshot could not be taken")
	}
	if result.SnapshotID != "" {
		t.Errorf("Use() reported snapshot %q after the snapshot failed", result.SnapshotID)
	}
	if got := readFile(t, npmrc); got != before {
		t.Errorf("Use() wrote %q even though the snapshot failed", got)
	}
}

func TestUseReportsARegionWithoutAMirror(t *testing.T) {
	svc, home := newTestService(t)
	writeFile(t, filepath.Join(home, ".gemrc"), "---\n:sources:\n  - "+gemUS+"\n")

	result, err := svc.Use(context.Background(), "eu", []string{"gem"}, false)
	if err == nil {
		t.Fatal("Use() succeeded for a region that ships no gem mirror")
	}
	if change := findChange(t, result, "gem"); change.Err == nil {
		t.Error("Use() did not record the missing mirror against gem")
	}
}

func TestUseRejectsUnknownRegionsAndApps(t *testing.T) {
	tests := []struct {
		name   string
		region string
		apps   []string
	}{
		{name: "unknown region", region: "xx", apps: []string{"npm"}},
		{name: "unknown app", region: "cn", apps: []string{"deno"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, home := newTestService(t)
			npmrc := filepath.Join(home, ".npmrc")
			writeFile(t, npmrc, "registry="+npmUS+"\n")

			result, err := svc.Use(context.Background(), tt.region, tt.apps, false)
			if err == nil {
				t.Fatalf("Use(%q, %v) succeeded, want an error", tt.region, tt.apps)
			}
			if result != nil {
				t.Errorf("Use() returned a result alongside a validation error: %+v", result)
			}
			if got := readFile(t, npmrc); got != "registry="+npmUS+"\n" {
				t.Errorf("Use() changed .npmrc to %q despite failing validation", got)
			}
		})
	}
}

func TestUseAcceptsAnAlias(t *testing.T) {
	svc, home := newTestService(t)
	writeFile(t, filepath.Join(home, ".gemrc"), "---\n:sources:\n  - "+gemUS+"\n")

	result, err := svc.Use(context.Background(), "cn", []string{"rubygems"}, true)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}
	if change := findChange(t, result, "gem"); change.To != gemCN {
		t.Errorf("Use() targeted %q, want %q", change.To, gemCN)
	}
}

// configPath returns the file one backend of env edits.
func configPath(t *testing.T, env backend.Env, app string) string {
	t.Helper()

	for _, b := range backend.All(env) {
		if b.Name() == app {
			return b.ConfigPath()
		}
	}
	t.Fatalf("no backend named %q", app)
	return ""
}

// helmAndDockerService builds a Service together with the two configuration
// files the new backends edit, seeded with the us mirrors.
func helmAndDockerService(t *testing.T) (*Service, string, string) {
	t.Helper()

	env, root := testEnv(t)
	svc := New(testSources(), backend.All(env), history.New(filepath.Join(root, "history")))

	helmConfig := configPath(t, env, "helm")
	writeFile(t, helmConfig, "apiVersion: v1\n"+
		"repositories:\n"+
		"  - name: bitnami\n"+
		"    url: https://charts.bitnami.com/bitnami\n"+
		"  - name: stable\n"+
		"    url: "+helmUS+"\n")

	dockerConfig := configPath(t, env, "docker")
	writeFile(t, dockerConfig, "{\n  \"log-driver\": \"json-file\",\n  \"registry-mirrors\": [\"https://old.example\"]\n}\n")

	return svc, helmConfig, dockerConfig
}

func TestUsePointsHelmAndDockerAtTheRegionsMirrors(t *testing.T) {
	svc, helmConfig, dockerConfig := helmAndDockerService(t)

	result, err := svc.Use(context.Background(), "cn", []string{"helm", "docker"}, false)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}

	if change := findChange(t, result, "helm"); change.From != helmUS || change.To != helmCN {
		t.Errorf("Use() reported %q -> %q for helm, want %q -> %q", change.From, change.To, helmUS, helmCN)
	}
	if change := findChange(t, result, "docker"); change.From != "https://old.example" || change.To != dockerCN {
		t.Errorf("Use() reported %q -> %q for docker, want %q -> %q", change.From, change.To, "https://old.example", dockerCN)
	}

	got := readFile(t, helmConfig)
	if !strings.Contains(got, helmCN) || !strings.Contains(got, "bitnami") {
		t.Errorf("repositories.yaml is %q, want the cn mirror and the other repository", got)
	}
	got = readFile(t, dockerConfig)
	if !strings.Contains(got, dockerCN) || !strings.Contains(got, "log-driver") {
		t.Errorf("daemon.json is %q, want the cn mirror and the other key", got)
	}

	// status recognises both of them as the cn region now.
	statuses, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() returned an unexpected error: %v", err)
	}
	regions := make(map[string]string, len(statuses))
	for _, status := range statuses {
		regions[status.App] = status.Region
	}
	if regions["helm"] != "cn" {
		t.Errorf("Status() put helm in region %q, want cn", regions["helm"])
	}
	if regions["docker"] != "cn" {
		t.Errorf("Status() put docker in region %q, want cn", regions["docker"])
	}
}

// The us docker mirror is Docker Hub itself, which the backend expresses by
// dropping registry-mirrors rather than by naming the default registry.
func TestUsePointsDockerBackAtHub(t *testing.T) {
	svc, _, dockerConfig := helmAndDockerService(t)

	if _, err := svc.Use(context.Background(), "us", []string{"docker"}, false); err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}

	got := readFile(t, dockerConfig)
	if strings.Contains(got, "registry-mirrors") {
		t.Errorf("daemon.json is %q, want the mirrors gone", got)
	}
	if !strings.Contains(got, "log-driver") {
		t.Errorf("daemon.json is %q, want the other key kept", got)
	}
}

func TestUseAcceptsTheHelmAndDockerAliases(t *testing.T) {
	tests := []struct {
		alias string
		app   string
		want  string
	}{
		{alias: "chart", app: "helm", want: helmCN},
		{alias: "charts", app: "helm", want: helmCN},
		{alias: "dockerd", app: "docker", want: dockerCN},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			svc, _, _ := helmAndDockerService(t)

			result, err := svc.Use(context.Background(), "cn", []string{tt.alias}, true)
			if err != nil {
				t.Fatalf("Use() returned an unexpected error: %v", err)
			}
			if change := findChange(t, result, tt.app); change.To != tt.want {
				t.Errorf("Use(%q) targeted %q, want %q", tt.alias, change.To, tt.want)
			}
		})
	}
}

func TestUseSkipsAppsThatAreNotInstalled(t *testing.T) {
	svc, home := newTestService(t)
	writeFile(t, filepath.Join(home, ".npmrc"), "registry="+npmUS+"\n")

	result, err := svc.Use(context.Background(), "cn", nil, true)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].App != "npm" {
		t.Errorf("Use() selected %+v, want npm alone", result.Changes)
	}
}

func TestUndoRestoresTheOriginalBytes(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	const before = "# hand written\nregistry=" + npmUS + "\nstrict-ssl=true\n"
	writeFile(t, npmrc, before)

	result, err := svc.Use(context.Background(), "cn", []string{"npm"}, false)
	if err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}
	if got := readFile(t, npmrc); got == before {
		t.Fatal("Use() did not change .npmrc")
	}

	snapshot, err := svc.Undo(context.Background(), "")
	if err != nil {
		t.Fatalf("Undo() returned an unexpected error: %v", err)
	}
	if snapshot.ID != result.SnapshotID {
		t.Errorf("Undo() restored %q, want the newest snapshot %q", snapshot.ID, result.SnapshotID)
	}
	if got := readFile(t, npmrc); got != before {
		t.Errorf("Undo() left .npmrc as %q, want %q", got, before)
	}
}

func TestUndoRemovesAFileThatDidNotExist(t *testing.T) {
	svc, home := newTestService(t)
	// gem detects on ~/.gem as well as on ~/.gemrc, so the config file itself
	// can be created from scratch by the change.
	if err := os.MkdirAll(filepath.Join(home, ".gem"), 0o755); err != nil {
		t.Fatalf("failed to create ~/.gem: %v", err)
	}
	gemrc := filepath.Join(home, ".gemrc")

	if _, err := svc.Use(context.Background(), "cn", []string{"gem"}, false); err != nil {
		t.Fatalf("Use() returned an unexpected error: %v", err)
	}
	if _, err := os.Stat(gemrc); err != nil {
		t.Fatalf("Use() did not create .gemrc: %v", err)
	}

	if _, err := svc.Undo(context.Background(), ""); err != nil {
		t.Fatalf("Undo() returned an unexpected error: %v", err)
	}
	if _, err := os.Stat(gemrc); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Undo() left .gemrc behind: %v", err)
	}
}

func TestUndoAcceptsASnapshotID(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	writeFile(t, npmrc, "registry="+npmUS+"\n")

	first, err := svc.Use(context.Background(), "cn", []string{"npm"}, false)
	if err != nil {
		t.Fatalf("Use(cn) returned an unexpected error: %v", err)
	}
	if _, err := svc.Use(context.Background(), "us", []string{"npm"}, false); err != nil {
		t.Fatalf("Use(us) returned an unexpected error: %v", err)
	}

	if _, err := svc.Undo(context.Background(), first.SnapshotID); err != nil {
		t.Fatalf("Undo() returned an unexpected error: %v", err)
	}
	if got := readFile(t, npmrc); !strings.Contains(got, npmUS) {
		t.Errorf("Undo() left .npmrc as %q, want the original us mirror", got)
	}
}

func TestUndoWithoutAnySnapshot(t *testing.T) {
	svc, _ := newTestService(t)

	if _, err := svc.Undo(context.Background(), ""); err == nil {
		t.Fatal("Undo() succeeded even though nothing has been changed yet")
	}
	if _, err := svc.Undo(context.Background(), "nosuchsnapshot"); !errors.Is(err, history.ErrNotFound) {
		t.Fatalf("Undo() returned %v, want history.ErrNotFound", err)
	}
}

func TestStatus(t *testing.T) {
	svc, home := newTestService(t)
	writeFile(t, filepath.Join(home, ".npmrc"), "registry="+npmCN+"/\n")
	writeFile(t, filepath.Join(home, ".gemrc"), "---\n:sources:\n  - https://gems.example.com\n")

	statuses, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() returned an unexpected error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("Status() returned %d entries, want npm and gem: %+v", len(statuses), statuses)
	}

	// backend.All lists npm before gem, and Status keeps that order.
	if statuses[0].App != "npm" || statuses[0].Region != "cn" {
		t.Errorf("Status()[0] = %+v, want npm in cn", statuses[0])
	}
	if statuses[1].App != "gem" || statuses[1].Region != LocalRegion {
		t.Errorf("Status()[1] = %+v, want gem reported as local", statuses[1])
	}
}

func TestStatusReportsNothingOnACleanMachine(t *testing.T) {
	svc, _ := newTestService(t)

	statuses, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() returned an unexpected error: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("Status() found %+v in an empty home directory", statuses)
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		name      string
		app       string
		wantFirst RegistryEntry
		wantCount int
		wantErr   bool
	}{
		{
			name:      "no app lists every mirror",
			app:       "",
			wantFirst: RegistryEntry{App: "cargo", Region: "cn", URL: "sparse+https://rsproxy.cn/index/"},
			wantCount: 17,
		},
		{
			name:      "helm is listed under its own name",
			app:       "helm",
			wantFirst: RegistryEntry{App: "helm", Region: "cn", URL: helmCN},
			wantCount: 2,
		},
		{
			name:      "docker is listed under its own name",
			app:       "dockerd",
			wantFirst: RegistryEntry{App: "docker", Region: "cn", URL: dockerCN},
			wantCount: 2,
		},
		{
			name:      "go is a first class backend now",
			app:       "go",
			wantFirst: RegistryEntry{App: "go", Region: "cn", URL: goCN},
			wantCount: 2,
		},
		{
			name:      "the berry backend shares yarn's mirrors",
			app:       "yarn-berry",
			wantFirst: RegistryEntry{App: "yarn", Region: "cn", URL: npmCN},
			wantCount: 2,
		},
		{
			name:      "homebrew lists every key it uses",
			app:       "brew",
			wantFirst: RegistryEntry{App: "homebrew_bottle_domain", Region: "cn", URL: "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles"},
			wantCount: 2,
		},
		{
			name:    "an unknown app is an error",
			app:     "deno",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)

			got, err := svc.List(context.Background(), tt.app)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("List(%q) succeeded, want an error", tt.app)
				}
				return
			}
			if err != nil {
				t.Fatalf("List(%q) returned an unexpected error: %v", tt.app, err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("List(%q) returned %d entries, want %d: %+v", tt.app, len(got), tt.wantCount, got)
			}
			if got[0] != tt.wantFirst {
				t.Errorf("List(%q)[0] = %+v, want %+v", tt.app, got[0], tt.wantFirst)
			}
		})
	}
}

func TestUseRespectsACancelledContext(t *testing.T) {
	svc, home := newTestService(t)
	npmrc := filepath.Join(home, ".npmrc")
	writeFile(t, npmrc, "registry="+npmUS+"\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.Use(ctx, "cn", []string{"npm"}, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Use() returned %v, want context.Canceled", err)
	}
	if got := readFile(t, npmrc); got != "registry="+npmUS+"\n" {
		t.Errorf("Use() changed .npmrc to %q despite the cancelled context", got)
	}
}

func TestRefreshRecordsCurrentRegistries(t *testing.T) {
	backup := redirectBackupFile(t)
	svc, home := newTestService(t)
	writeFile(t, filepath.Join(home, ".npmrc"), "registry="+npmCN+"\n")

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() returned an unexpected error: %v", err)
	}

	var recorded map[string]string
	if err := json.Unmarshal([]byte(readFile(t, backup)), &recorded); err != nil {
		t.Fatalf("failed to decode the backup file: %v", err)
	}
	if recorded["npm"] != npmCN {
		t.Errorf("the backup recorded %q for npm, want the cn mirror", recorded["npm"])
	}
}

func TestAppsListsEveryBackend(t *testing.T) {
	env, root := testEnv(t)
	svc := New(testSources(), backend.All(env), history.New(filepath.Join(root, "history")), WithHomebrew())

	want := []string{"npm", "yarn", "yarn-berry", "pip", "gem", "go", "cargo", "helm", "docker", "homebrew"}
	got := svc.Apps()
	if len(got) != len(want) {
		t.Fatalf("Apps() = %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("Apps()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestChangeResultMarshalsItsError(t *testing.T) {
	raw, err := json.Marshal(ChangeResult{App: "npm", Err: errors.New("boom")})
	if err != nil {
		t.Fatalf("failed to marshal a ChangeResult: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to decode the marshalled ChangeResult: %v", err)
	}
	if decoded["error"] != "boom" {
		t.Errorf("the marshalled ChangeResult carries error %v, want %q", decoded["error"], "boom")
	}
}

func TestAppStatusMarshalsItsError(t *testing.T) {
	raw, err := json.Marshal(AppStatus{App: "npm", Err: errors.New("boom")})
	if err != nil {
		t.Fatalf("failed to marshal an AppStatus: %v", err)
	}
	if !strings.Contains(string(raw), `"error":"boom"`) {
		t.Errorf("the marshalled AppStatus is %s, want it to carry the error", raw)
	}
}

// redirectBackupFile points the localdata package at a temporary directory so
// the tests never write to the developer's home directory.
func redirectBackupFile(t *testing.T) string {
	t.Helper()

	oldConfig, oldHub, oldFile := localdata.DOT_CONFIG_DIR, localdata.REGISTRY_HUB_DIR, localdata.SOURCE_BACKUP_FILE
	t.Cleanup(func() {
		localdata.DOT_CONFIG_DIR, localdata.REGISTRY_HUB_DIR, localdata.SOURCE_BACKUP_FILE = oldConfig, oldHub, oldFile
	})

	root := t.TempDir()
	localdata.DOT_CONFIG_DIR = filepath.Join(root, localdata.DOT_CONFIG_NAME)
	localdata.REGISTRY_HUB_DIR = filepath.Join(localdata.DOT_CONFIG_DIR, localdata.REGISTRY_HUB_FOLDER_NAME)
	localdata.SOURCE_BACKUP_FILE = filepath.Join(localdata.REGISTRY_HUB_DIR, localdata.SOURCE_BACKUP_FILE_NAME)
	return localdata.SOURCE_BACKUP_FILE
}
