package cli_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ZHallen122/RegTool/common/alias"
	"github.com/ZHallen122/RegTool/internal/cli"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"

	"github.com/rogpeppe/go-internal/testscript"
)

// The end-to-end tests run the regtool command in a subprocess of this test
// binary, so the real backends under source/app are never linked in: the only
// registered package managers are the fakes below. That keeps the tests green
// on a machine with no npm, pip, gem or brew, on every platform.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"regtool": regtoolMain,
	})
}

func regtoolMain() {
	registerFakeBackends()
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			home := filepath.Join(env.WorkDir, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				return fmt.Errorf("failed to create the test home directory: %w", err)
			}
			// os.UserHomeDir reads HOME on unix and USERPROFILE on Windows;
			// set both so nothing reaches the real user's config.
			env.Setenv("HOME", home)
			env.Setenv("USERPROFILE", home)
			// Never hit the network: use the sources embedded in the binary.
			env.Setenv(source.OfflineEnvVar, "1")
			// With -coverprofile every regtool subprocess writes coverage
			// data into GOCOVERDIR. Scripts run in parallel, and on Windows
			// two processes renaming the same meta-data file collide and
			// print an error to stderr. Give each script its own directory
			// there; the merged profile then only counts in-process tests.
			if runtime.GOOS == "windows" && os.Getenv("GOCOVERDIR") != "" {
				coverDir := filepath.Join(env.WorkDir, "coverdir")
				if err := os.MkdirAll(coverDir, 0o755); err != nil {
					return fmt.Errorf("failed to create the coverage directory: %w", err)
				}
				env.Setenv("GOCOVERDIR", coverDir)
			}
			return nil
		},
	})
}

// fakeBackends is the set of package managers the end-to-end tests pretend are
// installed, with the registry each one starts out on.
var fakeBackends = map[string]string{
	// A known mirror, so `status` reports a region.
	"npm": "https://registry.npmjs.org/",
	// An unknown mirror, so `status` reports "local".
	"pip": "https://pypi.example.com/simple",
}

func registerFakeBackends() {
	for name, defaultURL := range fakeBackends {
		alias.RegisterAlias(name, nil)
		source.RegisterManager([]string{name}, fakeManager{name: name, defaultURL: defaultURL})
	}
}

// fakeManager is an AppManager that keeps its "configuration" in a file under
// the test's HOME, so a change made by one regtool run is visible to the next.
type fakeManager struct {
	name       string
	defaultURL string
}

func (f fakeManager) statePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to locate the home directory: %w", err)
	}
	return filepath.Join(home, "fake-backends", f.name+".txt"), nil
}

func (f fakeManager) GetCurrRegistry() (string, error) {
	path, err := f.statePath()
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f.defaultURL, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read the fake %s registry: %w", f.name, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func (f fakeManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	if sources == nil {
		return "", errors.New("sources is nil")
	}
	urls := (*sources)[region][f.name]
	if len(urls) == 0 {
		return "", fmt.Errorf("no %s registry for region %s", f.name, region)
	}

	path, err := f.statePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("failed to create the fake backend directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(urls[0]), 0o600); err != nil {
		return "", fmt.Errorf("failed to write the fake %s registry: %w", f.name, err)
	}
	return urls[0], nil
}

func (f fakeManager) IsExists() bool { return true }
