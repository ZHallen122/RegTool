package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmConfigPath(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	cfg := filepath.FromSlash("/home/u/cfg")

	tests := []struct {
		name string
		goos string
		vars map[string]string
		want string
	}{
		{
			name: "linux default",
			goos: "linux",
			want: filepath.Join(home, ".config", "helm", "repositories.yaml"),
		},
		{
			name: "macos default",
			goos: "darwin",
			want: filepath.Join(home, "Library", "Preferences", "helm", "repositories.yaml"),
		},
		{
			name: "windows uses APPDATA",
			goos: "windows",
			vars: map[string]string{"APPDATA": filepath.FromSlash("/appdata")},
			want: filepath.Join(filepath.FromSlash("/appdata"), "helm", "repositories.yaml"),
		},
		{
			name: "windows without APPDATA falls back to the config dir",
			goos: "windows",
			want: filepath.Join(cfg, "helm", "repositories.yaml"),
		},
		{
			name: "XDG_CONFIG_HOME wins over the per-OS default",
			goos: "darwin",
			vars: map[string]string{"XDG_CONFIG_HOME": filepath.FromSlash("/xdg")},
			want: filepath.Join(filepath.FromSlash("/xdg"), "helm", "repositories.yaml"),
		},
		{
			name: "HELM_CONFIG_HOME wins over XDG_CONFIG_HOME",
			goos: "linux",
			vars: map[string]string{
				"XDG_CONFIG_HOME":  filepath.FromSlash("/xdg"),
				"HELM_CONFIG_HOME": filepath.FromSlash("/helm"),
			},
			want: filepath.Join(filepath.FromSlash("/helm"), "repositories.yaml"),
		},
		{
			name: "HELM_REPOSITORY_CONFIG wins over everything",
			goos: "linux",
			vars: map[string]string{
				"HELM_CONFIG_HOME":       filepath.FromSlash("/helm"),
				"XDG_CONFIG_HOME":        filepath.FromSlash("/xdg"),
				"HELM_REPOSITORY_CONFIG": filepath.FromSlash("/elsewhere/repos.yaml"),
			},
			want: filepath.FromSlash("/elsewhere/repos.yaml"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Env{
				Home:      home,
				ConfigDir: cfg,
				Getenv:    func(k string) string { return tt.vars[k] },
				goos:      tt.goos,
			}
			if got := NewHelm(env).ConfigPath(); got != tt.want {
				t.Errorf("ConfigPath() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestHelmDetectFindsTheConfigHome(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }, goos: "linux"}
	b := NewHelm(env)

	if ok, err := b.Detect(); err != nil || ok {
		t.Fatalf("Detect() = %v, %v on an empty home; want false, nil", ok, err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "helm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Detect(); err != nil || !ok {
		t.Errorf("Detect() = %v, %v once the config home exists; want true, nil", ok, err)
	}
}

// newHelm builds a helm backend whose repositories.yaml is a named file in a
// fresh temporary directory.
func newHelm(t *testing.T) Backend {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repositories.yaml")
	return NewHelm(Env{Getenv: func(k string) string {
		if k == "HELM_REPOSITORY_CONFIG" {
			return path
		}
		return ""
	}})
}

func TestHelmStableEntryHandling(t *testing.T) {
	tests := []struct {
		name     string
		before   string
		wantOld  string
		wantKeep []string
	}{
		{
			name:     "missing file gets a header and the entry",
			wantKeep: []string{"apiVersion: v1", "name: stable"},
		},
		{
			name:     "empty file gets a header and the entry",
			before:   "\n",
			wantKeep: []string{"apiVersion: v1", "name: stable"},
		},
		{
			name:     "an empty repositories list is filled in",
			before:   "apiVersion: v1\nrepositories: []\n",
			wantKeep: []string{"apiVersion: v1", "name: stable"},
		},
		{
			name:     "a null repositories key is replaced by a list",
			before:   "apiVersion: v1\nrepositories:\n",
			wantKeep: []string{"apiVersion: v1", "name: stable"},
		},
		{
			name: "the stable entry is rewritten and the others are kept",
			before: "apiVersion: v1\n" +
				"generated: \"2026-01-02T03:04:05Z\"\n" +
				"repositories:\n" +
				"  - name: bitnami\n" +
				"    url: https://charts.bitnami.com/bitnami\n" +
				"    caFile: /etc/ssl/bitnami.pem\n" +
				"  - name: stable\n" +
				"    url: https://old.example/charts\n" +
				"    username: someone\n",
			wantOld: "https://old.example/charts",
			wantKeep: []string{
				"apiVersion: v1",
				"generated:",
				"name: bitnami",
				"https://charts.bitnami.com/bitnami",
				"caFile: /etc/ssl/bitnami.pem",
				"username: someone",
			},
		},
		{
			name: "a stable entry without a url gets one",
			before: "repositories:\n" +
				"  - name: stable\n" +
				"    username: someone\n",
			wantKeep: []string{"name: stable", "username: someone"},
		},
		{
			name: "an entry named stable is added to an existing list",
			before: "repositories:\n" +
				"  - name: bitnami\n" +
				"    url: https://charts.bitnami.com/bitnami\n",
			wantKeep: []string{"name: bitnami", "name: stable"},
		},
		{
			name:     "a quoted url keeps its quoting",
			before:   "repositories:\n  - name: stable\n    url: \"https://old.example/charts\"\n",
			wantOld:  "https://old.example/charts",
			wantKeep: []string{"\"" + target + "\""},
		},
		{
			name:     "a non-mapping list entry is ignored",
			before:   "repositories:\n  - just a string\n",
			wantKeep: []string{"just a string", "name: stable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newHelm(t)
			if tt.before != "" {
				seed(t, b, tt.before)
			}

			if got, err := b.Current(); err != nil || got != tt.wantOld {
				t.Errorf("Current() = %q, %v; want %q, nil", got, err, tt.wantOld)
			}

			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			after := string(plan.After)
			if !strings.Contains(after, target) {
				t.Errorf("After does not mention the target:\n%s", after)
			}
			if strings.Contains(after, "old.example") {
				t.Errorf("After still points at the old chart repository:\n%s", after)
			}
			for _, keep := range tt.wantKeep {
				if !strings.Contains(after, keep) {
					t.Errorf("After dropped %q:\n%s", keep, after)
				}
			}
			if d := plan.Diff(); !strings.Contains(d, target) {
				t.Errorf("Diff() = %q; want a diff mentioning %q", d, target)
			}

			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, err := b.Current(); err != nil || got != target {
				t.Errorf("Current() after apply = %q, %v; want %q, nil", got, err, target)
			}
		})
	}
}

func TestHelmRejectsAFileItDoesNotUnderstand(t *testing.T) {
	tests := []struct {
		name   string
		before string
	}{
		{name: "top level sequence", before: "- one\n- two\n"},
		{name: "top level scalar", before: "just a string\n"},
		{name: "not yaml at all", before: "\t- : : [\n"},
		{name: "repositories is not a list", before: "repositories:\n  stable: https://old.example\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newHelm(t)
			seed(t, b, tt.before)
			if _, err := b.Current(); err == nil {
				t.Error("Current succeeded; want an error")
			}
			if _, err := b.Plan(target); err == nil {
				t.Error("Plan succeeded; want an error")
			}
		})
	}
}

// The managed entry is found wherever it sits in the list, and an entry whose
// url is not a scalar reads as unset rather than as garbage.
func TestHelmCurrentIgnoresAnUnusableURL(t *testing.T) {
	b := newHelm(t)
	seed(t, b, "repositories:\n  - name: stable\n    url:\n      - https://old.example\n")
	if got, err := b.Current(); err != nil || got != "" {
		t.Errorf("Current() = %q, %v; want \"\", nil", got, err)
	}
	plan, err := b.Plan(target)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, _ := b.Current(); got != target {
		t.Errorf("Current() after apply = %q; want %q", got, target)
	}
}
