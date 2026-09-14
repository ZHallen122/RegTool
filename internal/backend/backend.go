// Package backend edits package-manager configuration files directly instead of
// shelling out to the package managers themselves. Every backend works on a
// machine where the corresponding tool is not installed at all, which makes the
// whole package testable without any external command.
//
// # Plan and apply
//
// Changing a registry is split in two steps. Plan reads the current
// configuration file, computes the exact bytes that should replace it and
// returns a [Plan] describing the change. Nothing is written at this point, so
// a caller can show [Plan.Diff] to the user, check [Plan.IsNoop] and only then
// call [Plan.Apply]. A Plan is a value: it can be stored, logged or snapshotted
// before it is applied.
//
// # Atomic writes
//
// [Plan.Apply] never truncates the target file in place. It creates the parent
// directory if needed, writes the new content to a temporary file in the same
// directory, fsyncs it, gives it the mode of the existing file (0644 for a new
// file) and renames it over the target. A reader therefore sees either the old
// file or the new one, never a partially written one. On Windows a rename over
// an existing file can fail, so Apply removes the target and retries once.
//
// # Environment injection
//
// Backends never call [os.UserHomeDir] or [os.Getenv] directly. They resolve
// every path through an [Env], so tests point the whole package at a
// [testing.T.TempDir]. [DefaultEnv] fills an Env from the running OS.
package backend

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Backend is a single package manager whose registry lives in a configuration
// file that this package knows how to edit.
type Backend interface {
	// Name is the stable identifier of the backend, for example "npm".
	Name() string
	// ConfigPath is the absolute path of the file this backend edits. It is
	// returned even when the file does not exist yet.
	ConfigPath() string
	// Detect reports whether the tool appears to be used on this machine. It
	// looks at the file system only and never runs an external command.
	Detect() (bool, error)
	// Current returns the registry URL currently configured, or "" when the
	// file does not exist or does not set one.
	Current() (string, error)
	// Plan computes the change needed to point the backend at target without
	// writing anything.
	Plan(target string) (*Plan, error)
}

// Env supplies every piece of ambient state the backends need. Tests build one
// pointing at a temporary directory; production code uses [DefaultEnv].
type Env struct {
	// Home is the user's home directory.
	Home string
	// ConfigDir is the per-user configuration directory, as returned by
	// [os.UserConfigDir]. When empty it falls back to Home/.config.
	ConfigDir string
	// Getenv reads an environment variable. When nil every variable reads as
	// empty, which is what a hermetic test usually wants.
	Getenv func(string) string

	// goos overrides runtime.GOOS. Only tests set it, so that the
	// platform-specific path rules can be exercised everywhere.
	goos string
}

// DefaultEnv returns an Env describing the current process and user.
func DefaultEnv() Env {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		cfg = ""
	}
	return Env{Home: home, ConfigDir: cfg, Getenv: os.Getenv}
}

// getenv reads key, tolerating a nil Getenv.
func (e Env) getenv(key string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(key)
}

// configDir returns the per-user configuration directory, falling back to
// Home/.config when the Env does not carry one.
func (e Env) configDir() string {
	if e.ConfigDir != "" {
		return e.ConfigDir
	}
	return filepath.Join(e.Home, ".config")
}

// goosName is runtime.GOOS unless the Env overrides it.
func (e Env) goosName() string {
	if e.goos != "" {
		return e.goos
	}
	return runtime.GOOS
}

// All returns every backend this package implements, in a stable order.
func All(env Env) []Backend {
	return []Backend{
		NewNPM(env),
		NewYarn(env),
		NewYarnBerry(env),
		NewPip(env),
		NewGem(env),
		NewGo(env),
		NewCargo(env),
	}
}

// readConfig reads path, reporting whether it existed. A missing file is not an
// error: it simply plans as an empty file that will be created.
func readConfig(path string) (data []byte, existed bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// exists reports whether path exists, of any kind.
func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

// newPlan assembles a Plan, normalising the no-op case.
func newPlan(name, path string, before []byte, existed bool, after []byte, from, to string) *Plan {
	return &Plan{
		Backend: name,
		Path:    path,
		Existed: existed,
		Before:  before,
		After:   after,
		From:    from,
		To:      to,
	}
}
