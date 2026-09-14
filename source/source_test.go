package source

import (
	"reflect"
	"testing"

	"regtool/source/structs"
)

func TestConvertSourcesSingleRegion(t *testing.T) {
	tests := []struct {
		name    string
		sources structs.RegistrySources
		want    map[string]Source
	}{
		{
			name:    "empty document",
			sources: structs.RegistrySources{},
			want:    map[string]Source{},
		},
		{
			name: "region with no package managers",
			sources: structs.RegistrySources{
				structs.US: structs.RegistryRegionSources{},
			},
			want: map[string]Source{},
		},
		{
			name: "one package manager",
			sources: structs.RegistrySources{
				structs.US: structs.RegistryRegionSources{
					"npm": {"https://registry.npmjs.org/"},
				},
			},
			want: map[string]Source{
				"npm": {Region: "us", Url: "https://registry.npmjs.org/", Name: "npm"},
			},
		},
		{
			name: "several package managers keep their own names",
			sources: structs.RegistrySources{
				structs.CN: structs.RegistryRegionSources{
					"npm":                 {"https://registry.npmmirror.com/"},
					"yarn":                {"https://registry.npmmirror.com/"},
					"homebrew_api_domain": {"https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api"},
				},
			},
			want: map[string]Source{
				"npm":                 {Region: "cn", Url: "https://registry.npmmirror.com/", Name: "npm"},
				"yarn":                {Region: "cn", Url: "https://registry.npmmirror.com/", Name: "yarn"},
				"homebrew_api_domain": {Region: "cn", Url: "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api", Name: "homebrew_api_domain"},
			},
		},
		{
			name: "only the first url of a list is used",
			sources: structs.RegistrySources{
				structs.CN: structs.RegistryRegionSources{
					"homebrew_api_domain": {
						"https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api",
						"https://mirrors.ustc.edu.cn/homebrew-bottles/api",
					},
				},
			},
			want: map[string]Source{
				"homebrew_api_domain": {
					Region: "cn",
					Url:    "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api",
					Name:   "homebrew_api_domain",
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertSources(&tt.sources)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ConvertSources() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// ConvertSources flattens a region -> app -> urls document into a single
// app -> Source map, so an app present in several regions collapses to one
// entry. Map iteration order decides which region wins, so this asserts only
// what is deterministic: the key set, and that each entry is a coherent
// (region, url) pair taken from the input.
func TestConvertSourcesFlattensRegions(t *testing.T) {
	sources := structs.RegistrySources{
		structs.CN: structs.RegistryRegionSources{
			"npm":  {"https://registry.npmmirror.com/"},
			"yarn": {"https://registry.npmmirror.com/"},
		},
		structs.US: structs.RegistryRegionSources{
			"npm":                 {"https://registry.npmjs.org/"},
			"homebrew_api_domain": {"https://formulae.brew.sh/api"},
		},
		structs.EU: structs.RegistryRegionSources{
			"npm": {"https://registry.npmjs.org/"},
		},
	}

	got := ConvertSources(&sources)

	wantKeys := map[string]bool{"npm": true, "yarn": true, "homebrew_api_domain": true}
	if len(got) != len(wantKeys) {
		t.Fatalf("ConvertSources() returned %d entries, want %d: %#v", len(got), len(wantKeys), got)
	}
	for key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Fatalf("ConvertSources() is missing key %q: %#v", key, got)
		}
	}

	for name, src := range got {
		if src.Name != name {
			t.Fatalf("entry %q has Name %q", name, src.Name)
		}
		region, ok := structs.StringToRegion(src.Region)
		if !ok {
			t.Fatalf("entry %q has unknown region %q", name, src.Region)
		}
		urls := sources[region][name]
		if len(urls) == 0 || urls[0] != src.Url {
			t.Fatalf("entry %q has Url %q, which is not the first url of %v in region %q", name, src.Url, urls, region)
		}
	}
}

func TestConvertSourcesUnknownRegionIsPassedThrough(t *testing.T) {
	// The remote document is the source of truth for region keys; ConvertSources
	// does not validate them, it copies them verbatim.
	sources := structs.RegistrySources{
		structs.Region("jp"): structs.RegistryRegionSources{
			"npm": {"https://registry.npmjs.org/"},
		},
	}

	got := ConvertSources(&sources)
	want := map[string]Source{
		"npm": {Region: "jp", Url: "https://registry.npmjs.org/", Name: "npm"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConvertSources() = %#v, want %#v", got, want)
	}
}
