package alias

import (
	"reflect"
	"sort"
	"testing"
)

// reset clears the package-level manager so each test starts from a known
// state. The package intentionally exposes only functions over a global, so the
// test lives in the same package to be able to swap it out.
func reset(t *testing.T) {
	t.Helper()
	manager = newAliasManager()
	t.Cleanup(func() { manager = newAliasManager() })
}

func TestGetPrimary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "primary resolves to itself", input: "npm", want: "npm"},
		{name: "alias resolves to primary", input: "pnpm", want: "npm"},
		{name: "second alias resolves to primary", input: "cnpm", want: "npm"},
		{name: "other primary resolves to itself", input: "gem", want: "gem"},
		{name: "unknown name is returned unchanged", input: "cargo", want: "cargo"},
		{name: "empty string is returned unchanged", input: "", want: ""},
	}

	reset(t)
	RegisterAlias("npm", []string{"pnpm", "cnpm"})
	RegisterAlias("gem", []string{"rubygem"})

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := GetPrimary(tt.input); got != tt.want {
				t.Fatalf("GetPrimary(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetAllAliases(t *testing.T) {
	reset(t)
	RegisterAlias("npm", []string{"pnpm", "cnpm"})
	RegisterAlias("homebrew", []string{"brew"})
	RegisterAlias("pip", nil)

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "multiple aliases", input: "npm", want: []string{"pnpm", "cnpm"}},
		{name: "single alias", input: "homebrew", want: []string{"brew"}},
		{name: "registered with no aliases", input: "pip", want: []string{}},
		{name: "unknown primary", input: "cargo", want: []string{}},
		{name: "an alias is not a primary", input: "brew", want: []string{}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := GetAllAliases(tt.input)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("GetAllAliases(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetAllPrimary(t *testing.T) {
	tests := []struct {
		name     string
		register map[string][]string
		want     []string
	}{
		{
			name:     "nothing registered",
			register: nil,
			want:     []string{},
		},
		{
			name:     "single primary",
			register: map[string][]string{"npm": {"pnpm"}},
			want:     []string{"npm"},
		},
		{
			name: "several primaries, aliases are not primaries",
			register: map[string][]string{
				"npm":      {"pnpm", "cnpm"},
				"yarn":     nil,
				"homebrew": {"brew"},
			},
			want: []string{"homebrew", "npm", "yarn"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			for primary, aliases := range tt.register {
				RegisterAlias(primary, aliases)
			}

			got := GetAllPrimary()
			sort.Strings(got)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("GetAllPrimary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegisterAliasOverwrites(t *testing.T) {
	reset(t)
	RegisterAlias("npm", []string{"pnpm"})
	if got := GetPrimary("pnpm"); got != "npm" {
		t.Fatalf("GetPrimary(\"pnpm\") = %q, want \"npm\"", got)
	}

	// Re-registering replaces the alias list for that primary.
	RegisterAlias("npm", []string{"cnpm"})
	if got := GetAllAliases("npm"); !reflect.DeepEqual(got, []string{"cnpm"}) {
		t.Fatalf("GetAllAliases(\"npm\") = %v, want [cnpm]", got)
	}
	if got := GetPrimary("cnpm"); got != "npm" {
		t.Fatalf("GetPrimary(\"cnpm\") = %q, want \"npm\"", got)
	}

	// Known behaviour: the stale alias mapping is not pruned by a re-register.
	if got := GetPrimary("pnpm"); got != "npm" {
		t.Fatalf("GetPrimary(\"pnpm\") = %q, want \"npm\" (stale mapping retained)", got)
	}
}

func TestRegisterAliasIsIsolatedPerPrimary(t *testing.T) {
	reset(t)
	RegisterAlias("npm", []string{"pnpm"})
	RegisterAlias("yarn", []string{"yarnpkg"})

	if got := GetPrimary("yarnpkg"); got != "yarn" {
		t.Fatalf("GetPrimary(\"yarnpkg\") = %q, want \"yarn\"", got)
	}
	if got := GetAllAliases("yarn"); !reflect.DeepEqual(got, []string{"yarnpkg"}) {
		t.Fatalf("GetAllAliases(\"yarn\") = %v, want [yarnpkg]", got)
	}
	if got := GetAllAliases("npm"); !reflect.DeepEqual(got, []string{"pnpm"}) {
		t.Fatalf("GetAllAliases(\"npm\") = %v, want [pnpm]", got)
	}
}
