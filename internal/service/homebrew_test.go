package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ZHallen122/RegTool/source"
)

// fakeShell is a shell.ShellManager that keeps its exports in memory, so the
// homebrew change can be exercised without a shell, a home directory or brew.
type fakeShell struct {
	path    string
	vars    map[string]string
	setErr  error
	written []string
}

func (f *fakeShell) Path() string { return f.path }

func (f *fakeShell) GetEnv(key string) (string, error) {
	value, ok := f.vars[key]
	if !ok {
		return "", errors.New("not set")
	}
	return value, nil
}

func (f *fakeShell) SetEnv(key, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.vars[key] = value
	f.written = append(f.written, key)
	return nil
}

// newHomebrewChange builds a change the way homebrewSwitcher.Plan does, minus
// the shell lookup.
func newHomebrewChange(manager *fakeShell, to string, vars []homebrewVar) *homebrewChange {
	change := &homebrewChange{manager: manager, path: manager.path, vars: vars, to: to}
	for _, v := range vars {
		if v.Key == "HOMEBREW_BOTTLE_DOMAIN" {
			change.from = v.Old
		}
	}
	return change
}

func TestHomebrewChangeIsANoopWhenEveryExportMatches(t *testing.T) {
	manager := &fakeShell{path: "/home/u/.zshrc", vars: map[string]string{}}
	change := newHomebrewChange(manager, "https://mirror.example", []homebrewVar{
		{Key: "HOMEBREW_API_DOMAIN", Old: "https://api.example", New: "https://api.example"},
		{Key: "HOMEBREW_BOTTLE_DOMAIN", Old: "https://mirror.example", New: "https://mirror.example"},
	})

	if !change.IsNoop() {
		t.Error("a change that sets the values already exported is not a no-op")
	}
	if change.Diff() != "" {
		t.Errorf("a no-op produced a diff:\n%s", change.Diff())
	}
	if change.From() != "https://mirror.example" || change.To() != "https://mirror.example" {
		t.Errorf("the change reports %q -> %q", change.From(), change.To())
	}
}

func TestHomebrewChangeDiffListsTheExports(t *testing.T) {
	manager := &fakeShell{path: "/home/u/.zshrc", vars: map[string]string{}}
	change := newHomebrewChange(manager, "https://new.example", []homebrewVar{
		{Key: "HOMEBREW_API_DOMAIN", Old: "https://api.example", New: "https://api.example"},
		{Key: "HOMEBREW_BOTTLE_DOMAIN", Old: "https://old.example", New: "https://new.example"},
		{Key: "HOMEBREW_PIP_INDEX_URL", New: "https://pypi.example"},
	})

	if change.IsNoop() {
		t.Fatal("a change that rewrites two exports reports itself as a no-op")
	}
	diff := change.Diff()
	for _, want := range []string{
		"/home/u/.zshrc",
		`-export HOMEBREW_BOTTLE_DOMAIN="https://old.example"`,
		`+export HOMEBREW_BOTTLE_DOMAIN="https://new.example"`,
		`+export HOMEBREW_PIP_INDEX_URL="https://pypi.example"`,
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("the diff is missing %q:\n%s", want, diff)
		}
	}
	// The unchanged variable has nothing to show.
	if strings.Contains(diff, "HOMEBREW_API_DOMAIN") {
		t.Errorf("the diff mentions an export that does not change:\n%s", diff)
	}
	if paths := change.Paths(); len(paths) != 1 || paths[0] != "/home/u/.zshrc" {
		t.Errorf("the change snapshots %v, want the rc file alone", paths)
	}
}

func TestHomebrewChangeWithoutAnRCFileSnapshotsNothing(t *testing.T) {
	manager := &fakeShell{vars: map[string]string{}}
	change := newHomebrewChange(manager, "https://new.example", []homebrewVar{
		{Key: "HOMEBREW_BOTTLE_DOMAIN", New: "https://new.example"},
	})

	if paths := change.Paths(); len(paths) != 0 {
		t.Errorf("the change wants to snapshot %v, but there is no rc file", paths)
	}
}

func TestHomebrewIsNotDetectedWithoutBrew(t *testing.T) {
	// PATH is emptied, so exec.LookPath cannot find brew whatever the machine
	// running the tests has installed.
	t.Setenv("PATH", "")

	detected, err := newHomebrewSwitcher().Detect()
	if err != nil {
		t.Fatalf("Detect() returned an unexpected error: %v", err)
	}
	if detected {
		t.Error("homebrew was detected even though brew is not on PATH")
	}
}

func TestLoadBuildsAServiceFromTheEmbeddedSources(t *testing.T) {
	t.Setenv(source.OfflineEnvVar, "1")

	svc, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load() returned an unexpected error: %v", err)
	}

	apps := svc.Apps()
	if len(apps) == 0 || apps[len(apps)-1] != homebrewName {
		t.Fatalf("Load() wired up %v, want the file backends followed by homebrew", apps)
	}
	entries, err := svc.List(context.Background(), "cargo")
	if err != nil {
		t.Fatalf("List() returned an unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Error("Load() produced a service that knows no cargo mirrors")
	}
}
