package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNPMConfigPathHonoursUserconfig(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom", "npmrc")

	def := NewNPM(Env{Home: home})
	if want := filepath.Join(home, ".npmrc"); def.ConfigPath() != want {
		t.Errorf("ConfigPath = %q; want %q", def.ConfigPath(), want)
	}

	override := NewNPM(Env{Home: home, Getenv: func(k string) string {
		if k == "NPM_CONFIG_USERCONFIG" {
			return custom
		}
		return ""
	}})
	if override.ConfigPath() != custom {
		t.Errorf("ConfigPath = %q; want %q", override.ConfigPath(), custom)
	}

	plan, err := override.Plan(target)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("override path not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".npmrc")); err == nil {
		t.Error("default ~/.npmrc was written even though the override is set")
	}
}

func TestNPMDetectFindsCacheDirectory(t *testing.T) {
	home := t.TempDir()
	b := NewNPM(Env{Home: home})
	if ok, _ := b.Detect(); ok {
		t.Fatal("Detect = true on an empty home")
	}
	if err := os.MkdirAll(filepath.Join(home, ".npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Detect(); err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil once ~/.npm exists", ok, err)
	}
}

func TestNPMLineHandling(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		want    string
		wantOld string
	}{
		{
			name:   "empty file",
			before: "",
			want:   "registry=" + target + "\n",
		},
		{
			name:   "no trailing newline",
			before: "save-exact=true",
			want:   "save-exact=true\nregistry=" + target + "\n",
		},
		{
			name:    "crlf line endings are preserved",
			before:  "; c\r\nregistry=https://old.example\r\nsave-exact=true\r\n",
			want:    "; c\r\nregistry=" + target + "\r\nsave-exact=true\r\n",
			wantOld: "https://old.example",
		},
		{
			name:    "spaces around the separator",
			before:  "registry = https://old.example\n",
			want:    "registry=" + target + "\n",
			wantOld: "https://old.example",
		},
		{
			name:    "duplicate keys collapse to the effective one",
			before:  "registry=https://first.example\nsave-exact=true\nregistry=https://second.example\n",
			want:    "save-exact=true\nregistry=" + target + "\n",
			wantOld: "https://second.example",
		},
		{
			name:   "commented out registry is not touched",
			before: "; registry=https://commented.example\n",
			want:   "; registry=https://commented.example\nregistry=" + target + "\n",
		},
		{
			name:   "scoped registry is a different key",
			before: "@acme:registry=https://acme.example\n",
			want:   "@acme:registry=https://acme.example\nregistry=" + target + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			b := NewNPM(Env{Home: home})
			if tt.before != "" {
				seed(t, b, tt.before)
			}
			if got, _ := b.Current(); got != tt.wantOld {
				t.Errorf("Current = %q; want %q", got, tt.wantOld)
			}
			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if string(plan.After) != tt.want {
				t.Errorf("After = %q; want %q", plan.After, tt.want)
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, _ := b.Current(); got != target {
				t.Errorf("Current after apply = %q; want %q", got, target)
			}
		})
	}
}

func TestNPMPlanTrimsTarget(t *testing.T) {
	b := NewNPM(Env{Home: t.TempDir()})
	plan, err := b.Plan("  " + target + "\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.To != target {
		t.Errorf("To = %q; want %q", plan.To, target)
	}
	if strings.Contains(string(plan.After), "  ") {
		t.Errorf("After kept the surrounding whitespace: %q", plan.After)
	}
}
