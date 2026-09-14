package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoConfigPath(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	custom := filepath.Join(home, "custom", "goenv")

	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{
			name: "default config dir",
			want: filepath.Join(cfg, "go", "env"),
		},
		{
			name: "GOENV override",
			vars: map[string]string{"GOENV": custom},
			want: custom,
		},
		{
			name: "GOENV=off falls back to the default path",
			vars: map[string]string{"GOENV": "off"},
			want: filepath.Join(cfg, "go", "env"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := tt.vars
			env := Env{Home: home, ConfigDir: cfg, Getenv: func(k string) string { return vars[k] }}
			if got := NewGo(env).ConfigPath(); got != tt.want {
				t.Errorf("ConfigPath = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestGoConfigPathWithoutConfigDir(t *testing.T) {
	home := t.TempDir()
	b := NewGo(Env{Home: home})
	if want := filepath.Join(home, ".config", "go", "env"); b.ConfigPath() != want {
		t.Errorf("ConfigPath = %q; want %q", b.ConfigPath(), want)
	}
}

func TestGoDetectFindsGopath(t *testing.T) {
	home := t.TempDir()
	b := NewGo(Env{Home: home, ConfigDir: filepath.Join(home, "cfg")})
	if ok, _ := b.Detect(); ok {
		t.Fatal("Detect = true on an empty home")
	}
	if err := os.MkdirAll(filepath.Join(home, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Detect(); err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil once ~/go exists", ok, err)
	}
}

func TestGoProxyValueHandling(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		target  string
		want    string
		wantOld string
		wantNew string
	}{
		{
			name:    "missing file gets the direct fallback",
			target:  target,
			want:    "GOPROXY=" + target + ",direct\n",
			wantNew: target,
		},
		{
			name:    "current strips the direct fallback",
			before:  "GOPROXY=https://old.example,direct\n",
			target:  target,
			want:    "GOPROXY=" + target + ",direct\n",
			wantOld: "https://old.example",
			wantNew: target,
		},
		{
			name:    "a value without the fallback is reported as is",
			before:  "GOPROXY=https://old.example\n",
			target:  target,
			want:    "GOPROXY=" + target + ",direct\n",
			wantOld: "https://old.example",
			wantNew: target,
		},
		{
			name:    "an explicit list is written verbatim",
			target:  "https://a.example,https://b.example,direct",
			want:    "GOPROXY=https://a.example,https://b.example,direct\n",
			wantNew: "https://a.example,https://b.example",
		},
		{
			name:    "the magic direct value is written verbatim",
			target:  "direct",
			want:    "GOPROXY=direct\n",
			wantNew: "direct",
		},
		{
			name:    "the magic off value is written verbatim",
			target:  "off",
			want:    "GOPROXY=off\n",
			wantNew: "off",
		},
		{
			name:    "other variables survive",
			before:  "GOFLAGS=-mod=mod\nGOPROXY=https://old.example,direct\nGOSUMDB=off\n",
			target:  target,
			want:    "GOFLAGS=-mod=mod\nGOPROXY=" + target + ",direct\nGOSUMDB=off\n",
			wantOld: "https://old.example",
			wantNew: target,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			b := NewGo(Env{Home: home, ConfigDir: filepath.Join(home, "cfg")})
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
			if string(plan.After) != tt.want {
				t.Errorf("After = %q; want %q", plan.After, tt.want)
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, _ := b.Current(); got != tt.wantNew {
				t.Errorf("Current after apply = %q; want %q", got, tt.wantNew)
			}
		})
	}
}
