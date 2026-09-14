package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests run the same contract against every backend, so a new backend
// only has to add a fixture.

func TestPlanOnMissingFileCreatesIt(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			env := f.newEnv(t)
			b := f.make(env)

			if got, err := b.Current(); err != nil || got != "" {
				t.Fatalf("Current() on missing file = %q, %v; want \"\", nil", got, err)
			}
			if ok, err := b.Detect(); err != nil || ok {
				t.Fatalf("Detect() on empty home = %v, %v; want false, nil", ok, err)
			}

			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.Existed {
				t.Error("Existed = true; want false")
			}
			if plan.IsNoop() {
				t.Error("IsNoop = true; want false")
			}
			if plan.From != "" || plan.To != target {
				t.Errorf("From/To = %q/%q; want \"\"/%q", plan.From, plan.To, target)
			}
			if plan.Backend != b.Name() {
				t.Errorf("Backend = %q; want %q", plan.Backend, b.Name())
			}
			if plan.Path != b.ConfigPath() {
				t.Errorf("Path = %q; want %q", plan.Path, b.ConfigPath())
			}
			if !filepath.IsAbs(plan.Path) {
				t.Errorf("Path %q is not absolute", plan.Path)
			}
			if d := plan.Diff(); d == "" || !strings.Contains(d, target) {
				t.Errorf("Diff() = %q; want a diff mentioning %q", d, target)
			}

			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := readFile(t, plan.Path); got != string(plan.After) {
				t.Errorf("file content = %q; want %q", got, plan.After)
			}
			got, err := b.Current()
			if err != nil {
				t.Fatalf("Current after apply: %v", err)
			}
			if got != target {
				t.Errorf("Current after apply = %q; want %q", got, target)
			}
			if ok, err := b.Detect(); err != nil || !ok {
				t.Errorf("Detect after apply = %v, %v; want true, nil", ok, err)
			}
		})
	}
}

func TestPlanPreservesExistingConfig(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			env := f.newEnv(t)
			b := f.make(env)
			seed(t, b, f.existing)

			if got, err := b.Current(); err != nil || got != f.from {
				t.Fatalf("Current() = %q, %v; want %q, nil", got, err, f.from)
			}

			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if !plan.Existed {
				t.Error("Existed = false; want true")
			}
			if plan.From != f.from {
				t.Errorf("From = %q; want %q", plan.From, f.from)
			}
			if plan.IsNoop() {
				t.Fatal("IsNoop = true; want false")
			}
			if string(plan.Before) != f.existing {
				t.Errorf("Before = %q; want the file as it was on disk", plan.Before)
			}
			after := string(plan.After)
			for _, keep := range f.mustKeep {
				if !strings.Contains(after, keep) {
					t.Errorf("plan dropped %q:\n%s", keep, after)
				}
			}
			if f.wantAfter != "" && after != f.wantAfter {
				t.Errorf("After =\n%q\nwant\n%q", after, f.wantAfter)
			}
			if d := plan.Diff(); !strings.Contains(d, target) || !strings.Contains(d, f.from) {
				t.Errorf("Diff() = %q; want both %q and %q", d, f.from, target)
			}

			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got, err := b.Current()
			if err != nil {
				t.Fatalf("Current after apply: %v", err)
			}
			if got != target {
				t.Errorf("Current after apply = %q; want %q", got, target)
			}
		})
	}
}

func TestPlanIsNoopWhenAlreadySet(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			env := f.newEnv(t)
			b := f.make(env)
			seed(t, b, f.existing)

			first, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if err := first.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			second, err := b.Plan(target)
			if err != nil {
				t.Fatalf("second Plan: %v", err)
			}
			if !second.IsNoop() {
				t.Errorf("second Plan is not a no-op:\n%s", second.Diff())
			}
			if second.From != target {
				t.Errorf("From = %q; want %q", second.From, target)
			}
			if d := second.Diff(); d != "" {
				t.Errorf("Diff() = %q; want \"\" for a no-op", d)
			}

			before := readFile(t, second.Path)
			if err := second.Apply(); err != nil {
				t.Fatalf("Apply of a no-op: %v", err)
			}
			if after := readFile(t, second.Path); after != before {
				t.Errorf("applying a no-op rewrote the file")
			}
		})
	}
}

func TestApplyLeavesNoTempFilesAndKeepsMode(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			env := f.newEnv(t)
			b := f.make(env)
			seed(t, b, f.existing)

			path := b.ConfigPath()
			if runtime.GOOS != "windows" {
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatalf("chmod: %v", err)
				}
			}

			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".regtool-") {
					t.Errorf("temp file %q left behind", e.Name())
				}
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v; want 0600 preserved", info.Mode().Perm())
			}
		})
	}
}

func TestApplyCreatesParentDirectoriesWithMode0644(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			env := f.newEnv(t)
			b := f.make(env)

			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			info, err := os.Stat(plan.Path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
				t.Errorf("mode = %v; want 0644 for a new file", info.Mode().Perm())
			}
		})
	}
}

// A config path that is a directory must surface as an error rather than a
// silently empty configuration.
func TestReadErrorsSurface(t *testing.T) {
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			env := f.newEnv(t)
			b := f.make(env)
			if err := os.MkdirAll(b.ConfigPath(), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Current(); err == nil {
				t.Error("Current succeeded although the config path is a directory")
			}
			if _, err := b.Plan(target); err == nil {
				t.Error("Plan succeeded although the config path is a directory")
			}
		})
	}
}

func TestAllReturnsEveryBackendOnce(t *testing.T) {
	env := Env{Home: t.TempDir()}
	all := All(env)
	if len(all) != len(fixtures()) {
		t.Fatalf("All returned %d backends; fixtures cover %d", len(all), len(fixtures()))
	}
	seen := map[string]bool{}
	for _, b := range all {
		if seen[b.Name()] {
			t.Errorf("duplicate backend name %q", b.Name())
		}
		seen[b.Name()] = true
		if b.ConfigPath() == "" {
			t.Errorf("%s: empty ConfigPath", b.Name())
		}
	}
	for _, f := range fixtures() {
		if !seen[f.name] {
			t.Errorf("All is missing backend %q", f.name)
		}
	}
}
