package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// target is the registry every test plans towards.
const target = "https://mirror.example/registry"

// fixture describes one backend well enough to run the shared contract tests
// in generic_test.go against it.
type fixture struct {
	name string
	// make builds the backend under test.
	make func(Env) Backend
	// vars are the environment variables the backend should see. The single
	// argument is the temporary home directory.
	vars func(home string) map[string]string
	// existing is a realistic config file that already points somewhere else.
	existing string
	// from is the registry that existing configures.
	from string
	// mustKeep are substrings of existing that the plan must not disturb.
	mustKeep []string
	// wantAfter is the exact content the plan must produce from existing. It is
	// empty for the backends that reserialise the whole file.
	wantAfter string
}

// newEnv builds an Env rooted at a fresh temporary directory.
func (f fixture) newEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	vars := map[string]string{}
	if f.vars != nil {
		vars = f.vars(home)
	}
	return Env{
		Home:      home,
		ConfigDir: filepath.Join(home, "cfg"),
		Getenv:    func(k string) string { return vars[k] },
	}
}

// seed writes f.existing to the backend's config path.
func seed(t *testing.T, b Backend, content string) {
	t.Helper()
	path := b.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readFile returns the content of path.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func fixtures() []fixture {
	return []fixture{
		{
			name: "npm",
			make: NewNPM,
			existing: "; managed by hand\n" +
				"//registry.npmjs.org/:_authToken=secret\n" +
				"@acme:registry=https://acme.example/npm\n" +
				"registry=https://old.example/npm\n" +
				"save-exact=true\n",
			from:     "https://old.example/npm",
			mustKeep: []string{"; managed by hand", "_authToken=secret", "@acme:registry=", "save-exact=true"},
			wantAfter: "; managed by hand\n" +
				"//registry.npmjs.org/:_authToken=secret\n" +
				"@acme:registry=https://acme.example/npm\n" +
				"registry=" + target + "\n" +
				"save-exact=true\n",
		},
		{
			name: "yarn",
			make: NewYarn,
			existing: "# yarn 1 config\n" +
				"registry \"https://old.example/npm\"\n" +
				"network-timeout 60000\n",
			from:     "https://old.example/npm",
			mustKeep: []string{"# yarn 1 config", "network-timeout 60000"},
			wantAfter: "# yarn 1 config\n" +
				"registry \"" + target + "\"\n" +
				"network-timeout 60000\n",
		},
		{
			name: "yarn-berry",
			make: NewYarnBerry,
			existing: "# yarn berry config\n" +
				"nodeLinker: node-modules\n" +
				"npmRegistryServer: \"https://old.example/npm\"\n" +
				"npmScopes:\n" +
				"  acme:\n" +
				"    npmRegistryServer: \"https://acme.example/npm\"\n",
			from:     "https://old.example/npm",
			mustKeep: []string{"# yarn berry config", "nodeLinker: node-modules", "    npmRegistryServer: \"https://acme.example/npm\""},
			wantAfter: "# yarn berry config\n" +
				"nodeLinker: node-modules\n" +
				"npmRegistryServer: \"" + target + "\"\n" +
				"npmScopes:\n" +
				"  acme:\n" +
				"    npmRegistryServer: \"https://acme.example/npm\"\n",
		},
		{
			name: "pip",
			make: NewPip,
			existing: "# pip config\n" +
				"[global]\n" +
				"timeout = 60\n" +
				"index-url = https://old.example/simple\n" +
				"\n" +
				"[install]\n" +
				"trusted-host = old.example\n",
			from:     "https://old.example/simple",
			mustKeep: []string{"# pip config", "timeout = 60", "[install]", "trusted-host = old.example"},
			wantAfter: "# pip config\n" +
				"[global]\n" +
				"timeout = 60\n" +
				"index-url = " + target + "\n" +
				"\n" +
				"[install]\n" +
				"trusted-host = old.example\n",
		},
		{
			name: "gem",
			make: NewGem,
			existing: "---\n" +
				":update_sources: true\n" +
				":sources:\n" +
				"- https://old.example/\n" +
				":verbose: true\n",
			from:     "https://old.example/",
			mustKeep: []string{":update_sources", ":verbose"},
		},
		{
			name: "go",
			make: NewGo,
			existing: "GOFLAGS=-mod=mod\n" +
				"GOPROXY=https://old.example,direct\n" +
				"GOSUMDB=off\n",
			from:     "https://old.example",
			mustKeep: []string{"GOFLAGS=-mod=mod", "GOSUMDB=off"},
			wantAfter: "GOFLAGS=-mod=mod\n" +
				"GOPROXY=" + target + ",direct\n" +
				"GOSUMDB=off\n",
		},
		{
			name: "cargo",
			make: NewCargo,
			existing: "[source.crates-io]\n" +
				"replace-with = \"mirror\"\n" +
				"\n" +
				"[source.mirror]\n" +
				"registry = \"https://old.example/index\"\n" +
				"\n" +
				"[net]\n" +
				"retry = 3\n",
			from:     "https://old.example/index",
			mustKeep: []string{"[net]", "retry = 3"},
		},
		{
			name: "helm",
			make: NewHelm,
			existing: "apiVersion: v1\n" +
				"generated: \"2026-01-02T03:04:05Z\"\n" +
				"repositories:\n" +
				"  - name: bitnami\n" +
				"    url: https://charts.bitnami.com/bitnami\n" +
				"  - name: stable\n" +
				"    url: https://old.example/charts\n" +
				"    username: someone\n",
			from:     "https://old.example/charts",
			mustKeep: []string{"apiVersion: v1", "generated:", "bitnami", "username: someone"},
		},
	}
}
