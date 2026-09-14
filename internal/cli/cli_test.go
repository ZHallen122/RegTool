package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ZHallen122/RegTool/internal/cli"
	"github.com/ZHallen122/RegTool/source"

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
	testscript.Run(t, testscript.Params{
		Dir:                 filepath.Join("testdata", "script"),
		RequireExplicitExec: true,
		Setup:               setup,
	})
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

	// Never hit the network: use the sources embedded in the binary.
	env.Setenv(source.OfflineEnvVar, "1")

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
