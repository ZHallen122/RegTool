package cli_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ZHallen122/RegTool/internal/cli"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"

	"github.com/rogpeppe/go-internal/testscript"
)

// The end-to-end tests run the real regtool in a subprocess of this test
// binary, against the real backends. Nothing is faked: every script gets a
// throwaway home directory, seeds the configuration files it cares about and
// then checks what regtool did to them. That works on a machine with no npm,
// pip, gem or brew installed, which is the point of editing the files
// directly.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"regtool": func() {
			os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
		},
	})
}

func TestScripts(t *testing.T) {
	// regtool runs in a subprocess, so the mirrors the probing commands are
	// pointed at have to be real listeners rather than an injected client.
	// These two live for the whole run and every script gets a sources file
	// naming them.
	mirrors := startMirrors(t)
	// The same goes for the sources document itself: `regtool sources` is
	// about a real HTTP round trip, so the scripts get a real server.
	sourcesURL := startSourcesServer(t)

	testscript.Run(t, testscript.Params{
		Dir:                 filepath.Join("testdata", "script"),
		RequireExplicitExec: true,
		Setup: func(env *testscript.Env) error {
			if err := setup(env); err != nil {
				return err
			}
			env.Setenv("SOURCES_URL", sourcesURL)
			return mirrors.seed(env)
		},
	})
}

// scriptSourcesETag labels the document startSourcesServer serves.
const scriptSourcesETag = `"script-v1"`

// startSourcesServer serves a sources document the way the sources service
// does: a strong ETag, a cache header and a 304 for a matching If-None-Match.
// It is what lets a script watch regtool fetch, cache and revalidate.
func startSourcesServer(t *testing.T) string {
	t.Helper()

	body, err := json.Marshal(structs.RegistrySources{
		structs.CN: {"npm": {"https://served.example/npm"}, "yarn": {"https://served.example/yarn"}},
		structs.US: {"npm": {"https://served.example/npm-us"}, "yarn": {"https://served.example/yarn-us"}},
	})
	if err != nil {
		t.Fatalf("failed to encode the served sources: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", scriptSourcesETag)
		w.Header().Set("Cache-Control", "public, max-age=300")
		if r.Header.Get("If-None-Match") == scriptSourcesETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// mirrorServers is a fast mirror, a deliberately slow one and an address
// nothing listens on: everything `regtool doctor` has to tell apart.
type mirrorServers struct {
	fast string
	slow string
	dead string
}

// slowMirrorDelay is long enough that the fast mirror always wins, and short
// enough to stay well inside the default probe timeout.
const slowMirrorDelay = 200 * time.Millisecond

func startMirrors(t *testing.T) mirrorServers {
	t.Helper()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fast.Close)

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(slowMirrorDelay)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	// A port that was handed out and immediately given back refuses
	// connections, which is what an unreachable mirror looks like.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a listener: %v", err)
	}
	dead := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("failed to close the listener: %v", err)
	}

	return mirrorServers{fast: fast.URL, slow: slow.URL, dead: dead}
}

// seed writes a sources file naming the three mirrors into the script's work
// directory and points $PROBE_SOURCES at it. A script that wants to probe sets
// REGTOOL_SOURCES_FILE to it; the others never notice.
func (m mirrorServers) seed(env *testscript.Env) error {
	sources := structs.RegistrySources{
		// us is the quickest, cn is slow but alive, eu is dead.
		structs.US: {"npm": {m.fast}, "yarn": {m.fast}},
		structs.CN: {"npm": {m.slow}, "yarn": {m.slow}},
		structs.EU: {"npm": {m.dead}, "yarn": {m.dead}},
	}
	raw, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the test sources: %w", err)
	}

	path := filepath.Join(env.WorkDir, "probe-sources.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("failed to write the test sources: %w", err)
	}
	env.Setenv("PROBE_SOURCES", path)
	return nil
}

// setup points every path regtool reads at the script's own work directory, so
// a script can neither see nor damage the developer's configuration.
func setup(env *testscript.Env) error {
	home := filepath.Join(env.WorkDir, "home")
	// os.UserConfigDir reads XDG_CONFIG_HOME on Linux, APPDATA on Windows and
	// $HOME/Library/Application Support on macOS. Setting all of them keeps the
	// snapshot store, the go env file and pip.ini inside the sandbox wherever
	// the tests run.
	dirs := map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
	}
	for name, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create the test %s directory: %w", name, err)
		}
		env.Setenv(name, dir)
	}
	// macOS needs the Application Support directory as well, because that is
	// where os.UserConfigDir points there.
	if err := os.MkdirAll(filepath.Join(home, "Library", "Application Support"), 0o755); err != nil {
		return fmt.Errorf("failed to create the test Application Support directory: %w", err)
	}

	// Never hit the network: use the sources embedded in the binary. A script
	// that wants a fetch clears this and points REGTOOL_SOURCES_URL at the
	// harness's own server.
	env.Setenv(source.OfflineEnvVar, "1")

	// The sources cache belongs inside the sandbox too, on every OS and
	// whatever os.UserConfigDir would otherwise have resolved to.
	configDir := filepath.Join(home, "regtool")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("failed to create the test config directory: %w", err)
	}
	env.Setenv(source.ConfigDirEnvVar, configDir)

	// testscript puts the directory holding the regtool binary first on PATH.
	// Keeping only that entry means a tool that happens to be installed on the
	// machine running the tests, brew in particular, is never found and never
	// run.
	if first, _, ok := strings.Cut(env.Getenv("PATH"), string(os.PathListSeparator)); ok {
		env.Setenv("PATH", first)
	}

	// With -coverprofile every regtool subprocess writes coverage data into
	// GOCOVERDIR. Scripts run in parallel, and on Windows two processes
	// renaming the same meta-data file collide and print an error to stderr.
	// Give each script its own directory there; the merged profile then only
	// counts in-process tests.
	if runtime.GOOS == "windows" && os.Getenv("GOCOVERDIR") != "" {
		coverDir := filepath.Join(env.WorkDir, "coverdir")
		if err := os.MkdirAll(coverDir, 0o755); err != nil {
			return fmt.Errorf("failed to create the coverage directory: %w", err)
		}
		env.Setenv("GOCOVERDIR", coverDir)
	}
	return nil
}
