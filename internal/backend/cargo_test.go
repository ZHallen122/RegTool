package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCargoConfigPath(t *testing.T) {
	home := t.TempDir()
	cargoHome := filepath.Join(home, "elsewhere")

	t.Run("default", func(t *testing.T) {
		b := NewCargo(Env{Home: home})
		if want := filepath.Join(home, ".cargo", "config.toml"); b.ConfigPath() != want {
			t.Errorf("ConfigPath = %q; want %q", b.ConfigPath(), want)
		}
	})

	t.Run("CARGO_HOME override", func(t *testing.T) {
		b := NewCargo(Env{Home: home, Getenv: func(k string) string {
			if k == "CARGO_HOME" {
				return cargoHome
			}
			return ""
		}})
		want := filepath.Join(cargoHome, "config.toml")
		if b.ConfigPath() != want {
			t.Errorf("ConfigPath = %q; want %q", b.ConfigPath(), want)
		}
		plan, err := b.Plan(target)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if err := plan.Apply(); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("override path not written: %v", err)
		}
	})

	t.Run("legacy extension-less config", func(t *testing.T) {
		legacyHome := t.TempDir()
		legacy := filepath.Join(legacyHome, ".cargo", "config")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte("[net]\nretry = 3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		b := NewCargo(Env{Home: legacyHome})
		if b.ConfigPath() != legacy {
			t.Errorf("ConfigPath = %q; want the legacy %q", b.ConfigPath(), legacy)
		}
		plan, err := b.Plan(target)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if err := plan.Apply(); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if got, _ := b.Current(); got != target {
			t.Errorf("Current = %q; want %q", got, target)
		}
		if _, err := os.Stat(filepath.Join(legacyHome, ".cargo", "config.toml")); err == nil {
			t.Error("config.toml was created alongside the legacy config")
		}
	})

	t.Run("config.toml wins when both exist", func(t *testing.T) {
		bothHome := t.TempDir()
		dir := filepath.Join(bothHome, ".cargo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"config", "config.toml"} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		b := NewCargo(Env{Home: bothHome})
		if want := filepath.Join(dir, "config.toml"); b.ConfigPath() != want {
			t.Errorf("ConfigPath = %q; want %q", b.ConfigPath(), want)
		}
	})
}

func TestCargoDetect(t *testing.T) {
	home := t.TempDir()
	b := NewCargo(Env{Home: home})
	if ok, _ := b.Detect(); ok {
		t.Fatal("Detect = true on an empty home")
	}
	if err := os.MkdirAll(filepath.Join(home, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Detect(); err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil once ~/.cargo exists", ok, err)
	}
}

func TestCargoSourceReplacement(t *testing.T) {
	tests := []struct {
		name     string
		before   string
		target   string
		wantOld  string
		wantKeep []string
	}{
		{
			name:   "missing file",
			target: target,
		},
		{
			name:     "other tables survive",
			before:   "[net]\nretry = 3\n\n[build]\njobs = 4\n",
			target:   target,
			wantKeep: []string{"[net]", "retry = 3", "[build]", "jobs = 4"},
		},
		{
			name:    "existing replacement is repointed",
			before:  "[source.crates-io]\nreplace-with = \"mirror\"\n\n[source.mirror]\nregistry = \"https://old.example/index\"\n",
			target:  target,
			wantOld: "https://old.example/index",
		},
		{
			name:     "a differently named replacement is followed and kept",
			before:   "[source.crates-io]\nreplace-with = \"tuna\"\n\n[source.tuna]\nregistry = \"https://tuna.example/index\"\n",
			target:   target,
			wantOld:  "https://tuna.example/index",
			wantKeep: []string{"tuna"},
		},
		{
			name:   "replace-with pointing at a missing table reads as unset",
			before: "[source.crates-io]\nreplace-with = \"ghost\"\n",
			target: target,
		},
		{
			name:   "source table without crates-io reads as unset",
			before: "[source.other]\nregistry = \"https://other.example/index\"\n",
			target: target,
		},
		{
			name:   "sparse targets are written verbatim",
			target: "sparse+https://mirror.example/index/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			b := NewCargo(Env{Home: home})
			if tt.before != "" {
				seed(t, b, tt.before)
			}
			if got, _ := b.Current(); got != tt.wantOld {
				t.Errorf("Current = %q; want %q", got, tt.wantOld)
			}
			plan, err := b.Plan(tt.target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			after := string(plan.After)
			if !strings.Contains(after, tt.target) {
				t.Errorf("After does not mention %q:\n%s", tt.target, after)
			}
			if !strings.Contains(after, "[source.crates-io]") || !strings.Contains(after, "replace-with") {
				t.Errorf("After is missing the source replacement:\n%s", after)
			}
			for _, keep := range tt.wantKeep {
				if !strings.Contains(after, keep) {
					t.Errorf("After dropped %q:\n%s", keep, after)
				}
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, _ := b.Current(); got != tt.target {
				t.Errorf("Current after apply = %q; want %q", got, tt.target)
			}
		})
	}
}

func TestCargoRejectsBrokenTOML(t *testing.T) {
	b := NewCargo(Env{Home: t.TempDir()})
	seed(t, b, "[source.crates-io\nreplace-with = \n")
	if _, err := b.Plan(target); err == nil {
		t.Error("Plan succeeded; want an error")
	}
	if _, err := b.Current(); err == nil {
		t.Error("Current succeeded; want an error")
	}
}
