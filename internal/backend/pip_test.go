package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPipConfigPathPerOS(t *testing.T) {
	home := t.TempDir()
	appdata := filepath.Join(home, "AppData", "Roaming")

	tests := []struct {
		name string
		goos string
		vars map[string]string
		// pre creates files before the path is resolved.
		pre  func(t *testing.T)
		want string
	}{
		{
			name: "PIP_CONFIG_FILE wins everywhere",
			goos: "linux",
			vars: map[string]string{"PIP_CONFIG_FILE": filepath.Join(home, "explicit.conf")},
			want: filepath.Join(home, "explicit.conf"),
		},
		{
			name: "linux uses XDG style config",
			goos: "linux",
			want: filepath.Join(home, ".config", "pip", "pip.conf"),
		},
		{
			name: "windows uses APPDATA",
			goos: "windows",
			vars: map[string]string{"APPDATA": appdata},
			want: filepath.Join(appdata, "pip", "pip.ini"),
		},
		{
			name: "windows falls back to the config dir without APPDATA",
			goos: "windows",
			want: filepath.Join(home, "cfg", "pip", "pip.ini"),
		},
		{
			name: "macos uses Application Support by default",
			goos: "darwin",
			want: filepath.Join(home, "Library", "Application Support", "pip", "pip.conf"),
		},
		{
			name: "macos prefers an existing XDG style file",
			goos: "darwin",
			pre: func(t *testing.T) {
				p := filepath.Join(home, ".config", "pip", "pip.conf")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: filepath.Join(home, ".config", "pip", "pip.conf"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.pre != nil {
				tt.pre(t)
			}
			vars := tt.vars
			env := Env{
				Home:      home,
				ConfigDir: filepath.Join(home, "cfg"),
				Getenv:    func(k string) string { return vars[k] },
				goos:      tt.goos,
			}
			if got := NewPip(env).ConfigPath(); got != tt.want {
				t.Errorf("ConfigPath = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestPipINIHandling(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		want    string
		wantOld string
	}{
		{
			name:   "empty file gets a global section",
			before: "",
			want:   "[global]\nindex-url = " + target + "\n",
		},
		{
			name:   "global section exists without the key",
			before: "[global]\ntimeout = 60\n",
			want:   "[global]\nindex-url = " + target + "\ntimeout = 60\n",
		},
		{
			name:   "other sections only, global is appended",
			before: "[install]\nuser = true\n",
			want:   "[install]\nuser = true\n\n[global]\nindex-url = " + target + "\n",
		},
		{
			name:    "index_url spelling is recognised and normalised",
			before:  "[global]\nindex_url = https://old.example/simple\n",
			want:    "[global]\nindex-url = " + target + "\n",
			wantOld: "https://old.example/simple",
		},
		{
			name:    "colon separator",
			before:  "[global]\nindex-url: https://old.example/simple\n",
			want:    "[global]\nindex-url = " + target + "\n",
			wantOld: "https://old.example/simple",
		},
		{
			name:   "index-url outside global is left alone",
			before: "[install]\nindex-url = https://other.example/simple\n",
			want:   "[install]\nindex-url = https://other.example/simple\n\n[global]\nindex-url = " + target + "\n",
		},
		{
			name:    "duplicate keys in global collapse",
			before:  "[global]\nindex-url = https://first.example\ntimeout = 60\nindex-url = https://second.example\n",
			want:    "[global]\ntimeout = 60\nindex-url = " + target + "\n",
			wantOld: "https://second.example",
		},
		{
			name:   "no trailing newline",
			before: "[global]\ntimeout = 60",
			want:   "[global]\nindex-url = " + target + "\ntimeout = 60",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			vars := map[string]string{"PIP_CONFIG_FILE": filepath.Join(home, "pip.conf")}
			b := NewPip(Env{Home: home, Getenv: func(k string) string { return vars[k] }})
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
