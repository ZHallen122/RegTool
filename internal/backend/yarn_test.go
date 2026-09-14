package backend

import (
	"path/filepath"
	"testing"
)

func TestYarnConfigPaths(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home}
	if got, want := NewYarn(env).ConfigPath(), filepath.Join(home, ".yarnrc"); got != want {
		t.Errorf("yarn ConfigPath = %q; want %q", got, want)
	}
	if got, want := NewYarnBerry(env).ConfigPath(), filepath.Join(home, ".yarnrc.yml"); got != want {
		t.Errorf("yarn-berry ConfigPath = %q; want %q", got, want)
	}
}

// The two yarn backends must not claim each other's file, otherwise a yarn 1
// machine would grow a berry config and vice versa.
func TestYarnBackendsDetectIndependently(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home}
	v1, berry := NewYarn(env), NewYarnBerry(env)

	seed(t, v1, "registry \"https://old.example\"\n")
	if ok, _ := v1.Detect(); !ok {
		t.Error("yarn Detect = false with ~/.yarnrc present")
	}
	if ok, _ := berry.Detect(); ok {
		t.Error("yarn-berry Detect = true with only ~/.yarnrc present")
	}

	seed(t, berry, "npmRegistryServer: https://old.example\n")
	if ok, _ := berry.Detect(); !ok {
		t.Error("yarn-berry Detect = false with ~/.yarnrc.yml present")
	}
}

func TestYarnV1LineHandling(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		want    string
		wantOld string
	}{
		{
			name:   "empty file",
			before: "",
			want:   "registry \"" + target + "\"\n",
		},
		{
			name:    "unquoted value",
			before:  "registry https://old.example\n",
			want:    "registry \"" + target + "\"\n",
			wantOld: "https://old.example",
		},
		{
			name:    "single quoted value",
			before:  "registry 'https://old.example'\n",
			want:    "registry \"" + target + "\"\n",
			wantOld: "https://old.example",
		},
		{
			name:   "a key that merely starts with registry is untouched",
			before: "registry-extra \"https://other.example\"\n",
			want:   "registry-extra \"https://other.example\"\nregistry \"" + target + "\"\n",
		},
		{
			name:   "comment is not a setting",
			before: "# registry \"https://commented.example\"\n",
			want:   "# registry \"https://commented.example\"\nregistry \"" + target + "\"\n",
		},
		{
			name:   "bare registry word without a value is not a setting",
			before: "registry\n",
			want:   "registry\nregistry \"" + target + "\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewYarn(Env{Home: t.TempDir()})
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
		})
	}
}

func TestYarnBerryLineHandling(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		want    string
		wantOld string
	}{
		{
			name:   "empty file",
			before: "",
			want:   "npmRegistryServer: \"" + target + "\"\n",
		},
		{
			name:    "unquoted value",
			before:  "npmRegistryServer: https://old.example\n",
			want:    "npmRegistryServer: \"" + target + "\"\n",
			wantOld: "https://old.example",
		},
		{
			name:   "nested scope keys are not the top level key",
			before: "npmScopes:\n  acme:\n    npmRegistryServer: https://acme.example\n",
			want:   "npmScopes:\n  acme:\n    npmRegistryServer: https://acme.example\nnpmRegistryServer: \"" + target + "\"\n",
		},
		{
			name:    "existing top level key wins over the nested one",
			before:  "npmRegistryServer: https://old.example\nnpmScopes:\n  acme:\n    npmRegistryServer: https://acme.example\n",
			want:    "npmRegistryServer: \"" + target + "\"\nnpmScopes:\n  acme:\n    npmRegistryServer: https://acme.example\n",
			wantOld: "https://old.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewYarnBerry(Env{Home: t.TempDir()})
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
