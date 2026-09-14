package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerConfigPath(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	override := filepath.FromSlash("/rootless/daemon.json")

	tests := []struct {
		name string
		goos string
		vars map[string]string
		want string
	}{
		{
			name: "linux uses the system wide file",
			goos: "linux",
			want: filepath.Join(filepath.FromSlash("/etc"), "docker", "daemon.json"),
		},
		{
			name: "macos uses docker desktop's file",
			goos: "darwin",
			want: filepath.Join(home, ".docker", "daemon.json"),
		},
		{
			name: "windows uses docker desktop's file",
			goos: "windows",
			want: filepath.Join(home, ".docker", "daemon.json"),
		},
		{
			name: "the override wins on linux",
			goos: "linux",
			vars: map[string]string{DockerDaemonJSONEnvVar: override},
			want: override,
		},
		{
			name: "the override wins on macos too",
			goos: "darwin",
			vars: map[string]string{DockerDaemonJSONEnvVar: override},
			want: override,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Env{
				Home:   home,
				Getenv: func(k string) string { return tt.vars[k] },
				goos:   tt.goos,
			}
			if got := NewDocker(env).ConfigPath(); got != tt.want {
				t.Errorf("ConfigPath() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDockerDetectFindsTheDockerDirectory(t *testing.T) {
	home := t.TempDir()
	env := Env{
		Home: home,
		Getenv: func(k string) string {
			if k == DockerDaemonJSONEnvVar {
				return filepath.Join(home, "elsewhere", "daemon.json")
			}
			return ""
		},
	}
	b := NewDocker(env)

	if ok, err := b.Detect(); err != nil || ok {
		t.Fatalf("Detect() = %v, %v on an empty home; want false, nil", ok, err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Detect(); err != nil || !ok {
		t.Errorf("Detect() = %v, %v once ~/.docker exists; want true, nil", ok, err)
	}
}

// newDocker builds a docker backend whose daemon.json lives in a fresh
// temporary directory rather than in /etc.
func newDocker(t *testing.T) Backend {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.json")
	return NewDocker(Env{Getenv: func(k string) string {
		if k == DockerDaemonJSONEnvVar {
			return path
		}
		return ""
	}})
}

func TestDockerRegistryMirrors(t *testing.T) {
	tests := []struct {
		name      string
		before    string
		wantOld   string
		wantAfter string
	}{
		{
			name:      "missing file becomes an object with the mirror",
			wantAfter: "{\n  \"registry-mirrors\": [\n    \"" + target + "\"\n  ]\n}\n",
		},
		{
			name:      "empty file becomes an object with the mirror",
			before:    "\n",
			wantAfter: "{\n  \"registry-mirrors\": [\n    \"" + target + "\"\n  ]\n}\n",
		},
		{
			name:      "an empty object gains the key",
			before:    "{}\n",
			wantAfter: "{\n  \"registry-mirrors\": [\n    \"" + target + "\"\n  ]\n}\n",
		},
		{
			name:      "an empty mirror list reads as unset",
			before:    "{\"registry-mirrors\": []}\n",
			wantAfter: "{\n  \"registry-mirrors\": [\n    \"" + target + "\"\n  ]\n}\n",
		},
		{
			name:      "several mirrors collapse to the target",
			before:    "{\"registry-mirrors\": [\"https://old.example\", \"https://second.example\"]}\n",
			wantOld:   "https://old.example",
			wantAfter: "{\n  \"registry-mirrors\": [\n    \"" + target + "\"\n  ]\n}\n",
		},
		{
			name:    "other keys are kept and sorted",
			before:  "{\n  \"registry-mirrors\": [\"https://old.example\"],\n  \"log-driver\": \"json-file\",\n  \"debug\": true,\n  \"max-concurrent-downloads\": 12\n}\n",
			wantOld: "https://old.example",
			wantAfter: "{\n" +
				"  \"debug\": true,\n" +
				"  \"log-driver\": \"json-file\",\n" +
				"  \"max-concurrent-downloads\": 12,\n" +
				"  \"registry-mirrors\": [\n" +
				"    \"" + target + "\"\n" +
				"  ]\n" +
				"}\n",
		},
		{
			name:   "nested objects survive",
			before: "{\"log-opts\": {\"max-size\": \"10m\"}}\n",
			wantAfter: "{\n" +
				"  \"log-opts\": {\n" +
				"    \"max-size\": \"10m\"\n" +
				"  },\n" +
				"  \"registry-mirrors\": [\n" +
				"    \"" + target + "\"\n" +
				"  ]\n" +
				"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newDocker(t)
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
			if got := string(plan.After); got != tt.wantAfter {
				t.Errorf("After =\n%s\nwant\n%s", got, tt.wantAfter)
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

// Pointing docker at Hub itself means "no mirror", so the key goes away rather
// than naming the registry the daemon would use anyway.
func TestDockerHubTargetRemovesTheMirrors(t *testing.T) {
	for _, hub := range []string{DockerHubURL, DockerHubURL + "/"} {
		t.Run(hub, func(t *testing.T) {
			b := newDocker(t)
			seed(t, b, "{\"registry-mirrors\": [\"https://old.example\"], \"debug\": true}\n")

			plan, err := b.Plan(hub)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			want := "{\n  \"debug\": true\n}\n"
			if got := string(plan.After); got != want {
				t.Errorf("After = %q; want %q", got, want)
			}
			if plan.From != "https://old.example" || plan.To != hub {
				t.Errorf("From/To = %q/%q; want %q/%q", plan.From, plan.To, "https://old.example", hub)
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, err := b.Current(); err != nil || got != "" {
				t.Errorf("Current() after apply = %q, %v; want \"\", nil", got, err)
			}
		})
	}
}

func TestDockerRejectsAFileThatIsNotAnObject(t *testing.T) {
	tests := []struct {
		name   string
		before string
		want   string
	}{
		{name: "array", before: "[\"one\"]\n", want: "an array"},
		{name: "string", before: "\"just a string\"\n", want: "a string"},
		{name: "number", before: "42\n", want: "a number"},
		{name: "boolean", before: "true\n", want: "a boolean"},
		{name: "null", before: "null\n", want: "null"},
		{name: "trailing content", before: "{}\n{}\n", want: "trailing content"},
		{name: "not json at all", before: "{ nope\n", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newDocker(t)
			seed(t, b, tt.before)

			_, err := b.Current()
			if err == nil {
				t.Fatal("Current succeeded; want an error")
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Current error = %v; want it to mention %q", err, tt.want)
			}
			if _, err := b.Plan(target); err == nil {
				t.Error("Plan succeeded; want an error")
			}
		})
	}
}

// A large integer must survive the round trip through the decoder, which is
// what json.Number buys over the default float64.
func TestDockerKeepsLargeNumbersExact(t *testing.T) {
	b := newDocker(t)
	seed(t, b, "{\"some-size\": 9007199254740993}\n")

	plan, err := b.Plan(target)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !strings.Contains(string(plan.After), "9007199254740993") {
		t.Errorf("After lost the exact number:\n%s", plan.After)
	}
}
