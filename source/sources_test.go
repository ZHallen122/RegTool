package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"regtool/source/structs"
)

// withRemote points the package-level sources URL at srv for the duration of
// the test.
func withRemote(t *testing.T, url string) {
	t.Helper()
	original := remoteSourcesURL
	remoteSourcesURL = url
	t.Cleanup(func() { remoteSourcesURL = original })
}

const testSourcesJSON = `{
  "cn": {
    "npm": ["https://remote.example/npm"],
    "yarn": ["https://remote.example/yarn"],
    "pip": ["https://remote.example/pip"],
    "gem": ["https://remote.example/gem"]
  }
}`

func TestLoadRegistrySourcesUsesRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(testSourcesJSON))
	}))
	defer srv.Close()
	withRemote(t, srv.URL)

	got, err := LoadRegistrySources(context.Background())
	if err != nil {
		t.Fatalf("LoadRegistrySources returned error: %v", err)
	}

	cn, ok := (*got)[structs.CN]
	if !ok {
		t.Fatalf("expected region cn in remote sources, got %v", *got)
	}
	if len(cn["npm"]) == 0 || cn["npm"][0] != "https://remote.example/npm" {
		t.Errorf("expected the remote npm url, got %v", cn["npm"])
	}
	// The remote fixture only defines cn, so a us entry would mean the embedded
	// file was used instead.
	if _, ok := (*got)[structs.US]; ok {
		t.Errorf("expected remote sources only, but region us is present")
	}
}

func TestLoadRegistrySourcesFallsBackOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	withRemote(t, srv.URL)

	got, err := LoadRegistrySources(context.Background())
	if err != nil {
		t.Fatalf("LoadRegistrySources returned error: %v", err)
	}

	assertEmbeddedShape(t, got)
}

func TestLoadRegistrySourcesFallsBackOnCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testSourcesJSON))
	}))
	defer srv.Close()
	withRemote(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := LoadRegistrySources(ctx)
	if err != nil {
		t.Fatalf("LoadRegistrySources returned error: %v", err)
	}

	assertEmbeddedShape(t, got)
}

func TestGetRemoteRegistrySourcesPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	withRemote(t, srv.URL)

	if _, err := GetRemoteRegistrySources(context.Background()); err == nil {
		t.Fatal("expected an error for a 500 response, got nil")
	}
}

func TestGetRemoteRegistrySourcesRejectsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	withRemote(t, srv.URL)

	if _, err := GetRemoteRegistrySources(context.Background()); err == nil {
		t.Fatal("expected an error for a malformed body, got nil")
	}
}

func TestEmbeddedSourcesAreComplete(t *testing.T) {
	got, err := GetEmbeddedRegistrySources()
	if err != nil {
		t.Fatalf("GetEmbeddedRegistrySources returned error: %v", err)
	}
	assertEmbeddedShape(t, got)
}

func TestEmbeddedSourcesConvert(t *testing.T) {
	embedded, err := GetEmbeddedRegistrySources()
	if err != nil {
		t.Fatalf("GetEmbeddedRegistrySources returned error: %v", err)
	}

	converted := ConvertSources(embedded)
	for _, key := range []string{"npm", "yarn", "pip", "gem"} {
		entry, ok := converted[key]
		if !ok {
			t.Errorf("ConvertSources dropped %q", key)
			continue
		}
		if entry.Name != key {
			t.Errorf("ConvertSources set Name=%q for key %q", entry.Name, key)
		}
		if entry.Url == "" {
			t.Errorf("ConvertSources left an empty Url for %q", key)
		}
		if entry.Region == "" {
			t.Errorf("ConvertSources left an empty Region for %q", key)
		}
	}
}

// assertEmbeddedShape checks that sources look like the bundled sources.json:
// every supported region present, each carrying a url for every package
// manager the backends read.
func assertEmbeddedShape(t *testing.T, sources *structs.RegistrySources) {
	t.Helper()

	wantKeys := []string{
		"npm",
		"yarn",
		"pip",
		"gem",
		"homebrew_api_domain",
		"homebrew_bottle_domain",
		"homebrew_brew_git_remote",
		"homebrew_core_git_remote",
		"homebrew_pip_index_url",
	}

	for _, region := range []structs.Region{structs.CN, structs.US, structs.EU} {
		regionSources, ok := (*sources)[region]
		if !ok {
			t.Errorf("region %s missing", region)
			continue
		}
		for _, key := range wantKeys {
			urls, ok := regionSources[key]
			if !ok {
				t.Errorf("region %s is missing key %q", region, key)
				continue
			}
			if len(urls) == 0 || urls[0] == "" {
				t.Errorf("region %s key %q has no url", region, key)
			}
		}
	}
}
