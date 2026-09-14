package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiffFormat(t *testing.T) {
	tests := []struct {
		name    string
		plan    Plan
		want    string
		wantNil bool
	}{
		{
			name: "replaced line with context",
			plan: Plan{
				Path:    "cfg",
				Existed: true,
				Before:  []byte("a\nb\nc\n"),
				After:   []byte("a\nB\nc\n"),
			},
			want: "--- cfg\n+++ cfg\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n",
		},
		{
			name: "new file diffs against /dev/null",
			plan: Plan{
				Path:   "cfg",
				Before: nil,
				After:  []byte("registry=x\n"),
			},
			want: "--- /dev/null\n+++ cfg\n@@ -0,0 +1,1 @@\n+registry=x\n",
		},
		{
			name: "appended line",
			plan: Plan{
				Path:    "cfg",
				Existed: true,
				Before:  []byte("a\n"),
				After:   []byte("a\nb\n"),
			},
			want: "--- cfg\n+++ cfg\n@@ -1,1 +1,2 @@\n a\n+b\n",
		},
		{
			name: "distant changes become separate hunks",
			plan: Plan{
				Path:    "cfg",
				Existed: true,
				Before:  []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"),
				After:   []byte("x\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\ny\n"),
			},
			want: "--- cfg\n+++ cfg\n" +
				"@@ -1,4 +1,4 @@\n-1\n+x\n 2\n 3\n 4\n" +
				"@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+y\n",
		},
		{
			name: "no change produces no diff",
			plan: Plan{
				Path:    "cfg",
				Existed: true,
				Before:  []byte("a\n"),
				After:   []byte("a\n"),
			},
			wantNil: true,
		},
		{
			name: "missing trailing newline still diffs",
			plan: Plan{
				Path:    "cfg",
				Existed: true,
				Before:  []byte("a"),
				After:   []byte("b"),
			},
			want: "--- cfg\n+++ cfg\n@@ -1,1 +1,1 @@\n-a\n+b\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.plan.Diff()
			if tt.wantNil {
				if got != "" {
					t.Errorf("Diff = %q; want \"\"", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("Diff =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestIsNoop(t *testing.T) {
	tests := []struct {
		name string
		plan Plan
		want bool
	}{
		{name: "identical and existing", plan: Plan{Existed: true, Before: []byte("a"), After: []byte("a")}, want: true},
		{name: "identical but missing", plan: Plan{Before: nil, After: nil}, want: false},
		{name: "different", plan: Plan{Existed: true, Before: []byte("a"), After: []byte("b")}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.plan.IsNoop(); got != tt.want {
				t.Errorf("IsNoop = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestApplyOverwritesAndCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "cfg")

	p := &Plan{Backend: "test", Path: path, After: []byte("one\n")}
	if err := p.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readFile(t, path); got != "one\n" {
		t.Errorf("content = %q; want %q", got, "one\n")
	}

	p2 := &Plan{Backend: "test", Path: path, Existed: true, Before: []byte("one\n"), After: []byte("two\n")}
	if err := p2.Apply(); err != nil {
		t.Fatalf("Apply overwrite: %v", err)
	}
	if got := readFile(t, path); got != "two\n" {
		t.Errorf("content = %q; want %q", got, "two\n")
	}
}

func TestApplyRejectsAnEmptyPath(t *testing.T) {
	p := &Plan{Backend: "test", After: []byte("x")}
	if err := p.Apply(); err == nil {
		t.Fatal("Apply succeeded with an empty path")
	}
}

func TestApplyFailsWhenTheParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Backend: "test", Path: filepath.Join(blocker, "cfg"), After: []byte("x")}
	if err := p.Apply(); err == nil {
		t.Fatal("Apply succeeded with a file as the parent directory")
	}
}

func TestApplyFailsWhenTheTargetIsADirectory(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "adir")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Backend: "test", Path: dst, Existed: true, Before: []byte("x"), After: []byte("y")}
	if err := p.Apply(); err == nil {
		t.Fatal("Apply succeeded onto a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".regtool-") {
			t.Errorf("temp file %q left behind after a failed apply", e.Name())
		}
	}
}

func TestDefaultEnv(t *testing.T) {
	env := DefaultEnv()
	if env.Getenv == nil {
		t.Fatal("DefaultEnv left Getenv nil")
	}
	if env.Home == "" {
		t.Error("DefaultEnv left Home empty")
	}
	if env.goosName() != runtime.GOOS {
		t.Errorf("goosName = %q; want %q", env.goosName(), runtime.GOOS)
	}
	// The real environment must be visible through the injected reader.
	key := "PATH"
	if env.getenv(key) != os.Getenv(key) {
		t.Errorf("getenv(%q) does not read the process environment", key)
	}
	for _, b := range All(env) {
		if b.ConfigPath() == "" {
			t.Errorf("%s: empty ConfigPath with the default env", b.Name())
		}
		if _, err := b.Detect(); err != nil {
			t.Errorf("%s: Detect failed on the real machine: %v", b.Name(), err)
		}
	}
}

func TestEnvWithoutGetenvReadsEmpty(t *testing.T) {
	env := Env{Home: t.TempDir()}
	if got := env.getenv("ANYTHING"); got != "" {
		t.Errorf("getenv = %q; want \"\" when Getenv is nil", got)
	}
	if got, want := env.configDir(), filepath.Join(env.Home, ".config"); got != want {
		t.Errorf("configDir = %q; want %q", got, want)
	}
}
