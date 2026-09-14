package structs

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStringToRegion(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   Region
		wantOK bool
	}{
		{name: "cn", input: "cn", want: CN, wantOK: true},
		{name: "us", input: "us", want: US, wantOK: true},
		{name: "eu", input: "eu", want: EU, wantOK: true},
		{name: "unknown jp", input: "jp", want: "", wantOK: false},
		{name: "empty", input: "", want: "", wantOK: false},
		{name: "uppercase is not accepted", input: "US", want: "", wantOK: false},
		{name: "whitespace is not trimmed", input: " us", want: "", wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, ok := StringToRegion(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("StringToRegion(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("StringToRegion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAllRegions(t *testing.T) {
	want := []Region{US, CN, EU}
	got := AllRegions()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllRegions() = %v, want %v", got, want)
	}

	// Every listed region must round-trip through StringToRegion, which is the
	// invariant that kept "jp" out of the menu.
	for _, r := range got {
		resolved, ok := StringToRegion(string(r))
		if !ok || resolved != r {
			t.Fatalf("region %q does not round-trip: got %q ok=%v", r, resolved, ok)
		}
	}
}

func TestAllRegionsReturnsCopy(t *testing.T) {
	first := AllRegions()
	first[0] = Region("mutated")

	second := AllRegions()
	if second[0] != US {
		t.Fatalf("AllRegions() leaked its backing array: second[0] = %q", second[0])
	}
}

func TestAllRegionStrings(t *testing.T) {
	want := []string{"us", "cn", "eu"}
	got := AllRegionStrings()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllRegionStrings() = %v, want %v", got, want)
	}

	regions := AllRegions()
	if len(got) != len(regions) {
		t.Fatalf("AllRegionStrings() has %d entries, AllRegions() has %d", len(got), len(regions))
	}
	for i := range regions {
		if got[i] != string(regions[i]) {
			t.Fatalf("index %d: AllRegionStrings() = %q, AllRegions() = %q", i, got[i], regions[i])
		}
	}
}

const sampleSourcesJSON = `{
  "cn": {
    "npm": ["https://registry.npmmirror.com/"],
    "yarn": ["https://registry.npmmirror.com/"],
    "homebrew_api_domain": [
      "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api",
      "https://mirrors.ustc.edu.cn/homebrew-bottles/api"
    ]
  },
  "us": {
    "npm": ["https://registry.npmjs.org/"],
    "yarn": ["https://registry.yarnpkg.com/"],
    "homebrew_api_domain": ["https://formulae.brew.sh/api"]
  },
  "eu": {
    "npm": ["https://registry.npmjs.org/"],
    "yarn": ["https://registry.yarnpkg.com/"],
    "homebrew_api_domain": ["https://formulae.brew.sh/api"]
  }
}`

func TestRegistrySourcesUnmarshal(t *testing.T) {
	var sources RegistrySources
	if err := json.Unmarshal([]byte(sampleSourcesJSON), &sources); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if len(sources) != 3 {
		t.Fatalf("got %d regions, want 3", len(sources))
	}

	for _, region := range AllRegions() {
		if _, ok := sources[region]; !ok {
			t.Fatalf("region %q missing from unmarshalled document", region)
		}
	}

	tests := []struct {
		region  Region
		key     string
		wantLen int
		wantURL string
	}{
		{region: CN, key: "npm", wantLen: 1, wantURL: "https://registry.npmmirror.com/"},
		{region: CN, key: "homebrew_api_domain", wantLen: 2, wantURL: "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/api"},
		{region: US, key: "npm", wantLen: 1, wantURL: "https://registry.npmjs.org/"},
		{region: US, key: "yarn", wantLen: 1, wantURL: "https://registry.yarnpkg.com/"},
		{region: EU, key: "homebrew_api_domain", wantLen: 1, wantURL: "https://formulae.brew.sh/api"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.region)+"/"+tt.key, func(t *testing.T) {
			urls := sources[tt.region][tt.key]
			if len(urls) != tt.wantLen {
				t.Fatalf("got %d urls, want %d (%v)", len(urls), tt.wantLen, urls)
			}
			if urls[0] != tt.wantURL {
				t.Fatalf("first url = %q, want %q", urls[0], tt.wantURL)
			}
		})
	}

	// A region that is not in the supported list simply does not appear.
	if _, ok := sources[Region("jp")]; ok {
		t.Fatal("unexpected jp region in sample document")
	}
}

func TestRegistrySourcesUnmarshalInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "not json", input: "not json"},
		{name: "truncated", input: `{"cn": {"npm": [`},
		{name: "urls not an array", input: `{"cn": {"npm": "https://example.com"}}`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var sources RegistrySources
			if err := json.Unmarshal([]byte(tt.input), &sources); err == nil {
				t.Fatalf("json.Unmarshal(%q) = nil error, want error", tt.input)
			}
		})
	}
}
