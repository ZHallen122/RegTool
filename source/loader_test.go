package source

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZHallen122/RegTool/source/structs"
)

// firstETag labels the document etagServer serves until a test replaces it.
const firstETag = `"v1"`

// document is one version of the served sources: a body and the ETag the
// server labels it with. The two travel together so a test can never swap one
// without the other.
type document struct{ etag, body string }

// etagServer serves a sources document with a strong ETag and honours
// If-None-Match, the way the sources service does. It counts the requests it
// answered and which of them were conditional.
type etagServer struct {
	*httptest.Server
	served        atomic.Value // document
	requests      atomic.Int64
	conditional   atomic.Int64
	lastIfNoneTag atomic.Value // string
}

func newETagServer(t *testing.T) *etagServer {
	t.Helper()

	srv := &etagServer{}
	srv.served.Store(document{etag: firstETag, body: testSourcesJSON})
	srv.lastIfNoneTag.Store("")

	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.requests.Add(1)
		current := srv.served.Load().(document)

		match := r.Header.Get("If-None-Match")
		srv.lastIfNoneTag.Store(match)
		if match != "" {
			srv.conditional.Add(1)
		}

		w.Header().Set("ETag", current.etag)
		w.Header().Set("Cache-Control", "public, max-age=300")
		if match == current.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(current.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serve replaces the document the server hands out from now on.
func (s *etagServer) serve(etag, body string) { s.served.Store(document{etag: etag, body: body}) }

// newLoader returns a loader pointed at url that caches into a directory of its
// own, so no test ever reads or writes the developer's configuration.
func newLoader(t *testing.T, url string) *Loader {
	t.Helper()
	return &Loader{URL: url, CacheDir: t.TempDir()}
}

// readCacheFile decodes the cache a loader wrote, failing the test when there
// is none.
func readCacheFile(t *testing.T, l *Loader) Cache {
	t.Helper()

	raw, err := os.ReadFile(l.CachePath())
	if err != nil {
		t.Fatalf("failed to read the cache file: %v", err)
	}
	var cache Cache
	if err := json.Unmarshal(raw, &cache); err != nil {
		t.Fatalf("failed to decode the cache file: %v", err)
	}
	return cache
}

// assertRemoteShape checks that sources are the ones the test server serves:
// cn only, pointing at remote.example.
func assertRemoteShape(t *testing.T, sources *structs.RegistrySources) {
	t.Helper()

	cn, ok := (*sources)[structs.CN]
	if !ok {
		t.Fatalf("expected region cn in the loaded sources, got %v", *sources)
	}
	if len(cn["npm"]) == 0 || cn["npm"][0] != "https://remote.example/npm" {
		t.Errorf("expected the remote npm url, got %v", cn["npm"])
	}
	// The fixture only defines cn, so a us entry means the embedded copy was
	// used instead.
	if _, ok := (*sources)[structs.US]; ok {
		t.Errorf("expected the remote sources, but region us is present")
	}
}

func TestLoaderCachesA200WithItsETag(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginRemote {
		t.Errorf("expected origin %q, got %q", OriginRemote, origin)
	}
	assertRemoteShape(t, sources)

	cache := readCacheFile(t, loader)
	if cache.URL != srv.URL {
		t.Errorf("expected the cache to name %q, got %q", srv.URL, cache.URL)
	}
	if cache.ETag != `"v1"` {
		t.Errorf(`expected the cached etag to be "v1", got %q`, cache.ETag)
	}
	if cache.FetchedAt.IsZero() {
		t.Error("expected the cache to record when it was fetched")
	}
	assertRemoteShape(t, &cache.Sources)
}

func TestLoaderSendsIfNoneMatchAndUsesTheCacheOn304(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginRemote {
		t.Fatalf("first Load: origin %q, err %v", origin, err)
	}
	if got := srv.lastIfNoneTag.Load().(string); got != "" {
		t.Errorf("expected the first fetch to be unconditional, it sent If-None-Match %q", got)
	}

	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("second Load returned error: %v", err)
	}
	if origin != OriginCache {
		t.Errorf("expected origin %q after a 304, got %q", OriginCache, origin)
	}
	assertRemoteShape(t, sources)

	if got := srv.conditional.Load(); got != 1 {
		t.Errorf("expected exactly one conditional request, got %d", got)
	}
	if got := srv.lastIfNoneTag.Load().(string); got != `"v1"` {
		t.Errorf(`expected If-None-Match "v1", got %q`, got)
	}
}

func TestLoaderUsesTheCacheWhenTheServerIsDown(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginRemote {
		t.Fatalf("first Load: origin %q, err %v", origin, err)
	}
	srv.Close()

	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginCache {
		t.Errorf("expected origin %q with the server down, got %q", OriginCache, origin)
	}
	assertRemoteShape(t, sources)
}

func TestLoaderFallsBackToEmbeddedWithNoCache(t *testing.T) {
	srv := newETagServer(t)
	url := srv.URL
	srv.Close()

	loader := newLoader(t, url)
	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginEmbedded {
		t.Errorf("expected origin %q with no cache and no server, got %q", OriginEmbedded, origin)
	}
	assertEmbeddedShape(t, sources)

	if _, err := os.Stat(loader.CachePath()); !os.IsNotExist(err) {
		t.Errorf("expected no cache file to be written, stat returned %v", err)
	}
}

func TestLoaderFallsBackOnInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	loader := newLoader(t, srv.URL)
	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginEmbedded {
		t.Errorf("expected origin %q for a malformed body, got %q", OriginEmbedded, origin)
	}
	assertEmbeddedShape(t, sources)
}

func TestLoaderRejectsSourcesWithNoRegions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	loader := newLoader(t, srv.URL)
	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginEmbedded {
		t.Errorf("expected origin %q for an empty document, got %q", OriginEmbedded, origin)
	}
	assertEmbeddedShape(t, sources)
}

func TestLoaderRejectsSourcesWhoseRegionsAreEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"cn": {}}`))
	}))
	defer srv.Close()

	loader := newLoader(t, srv.URL)
	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginEmbedded {
		t.Errorf("expected the embedded fallback for a region with no keys, got origin %q, err %v", origin, err)
	}
}

func TestLoaderIgnoresACacheWrittenForAnotherURL(t *testing.T) {
	first := newETagServer(t)
	dir := t.TempDir()

	// Fill the cache from one URL...
	if _, _, err := (&Loader{URL: first.URL, CacheDir: dir}).Load(context.Background()); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	// ...then read it back as a loader pointed somewhere else. The cache is
	// not this URL's, so it is neither served nor used to make the fetch
	// conditional.
	second := newETagServer(t)
	loader := &Loader{URL: second.URL, CacheDir: dir}

	cache, err := loader.ReadCache()
	if err != nil {
		t.Fatalf("ReadCache returned error: %v", err)
	}
	if cache != nil {
		t.Fatalf("expected the cache of another url to be ignored, got %+v", cache)
	}

	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginRemote {
		t.Fatalf("Load: origin %q, err %v", origin, err)
	}
	if got := second.conditional.Load(); got != 0 {
		t.Errorf("expected no conditional request against the new url, got %d", got)
	}
	if got := readCacheFile(t, loader); got.URL != second.URL {
		t.Errorf("expected the cache to be rewritten for %q, got %q", second.URL, got.URL)
	}
}

func TestLoaderIgnoresAnUnreadableCache(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	if err := os.WriteFile(loader.CachePath(), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("failed to write the broken cache: %v", err)
	}

	if _, err := loader.ReadCache(); err == nil {
		t.Error("expected ReadCache to report the broken cache")
	}

	// Load treats it as no cache at all and refetches.
	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginRemote {
		t.Errorf("expected a refetch past the broken cache, got origin %q, err %v", origin, err)
	}
}

func TestLoaderOfflineUsesTheCache(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	if _, _, err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	offline := &Loader{URL: srv.URL, CacheDir: loader.CacheDir, Offline: true}
	sources, origin, err := offline.Load(context.Background())
	if err != nil {
		t.Fatalf("offline Load returned error: %v", err)
	}
	if origin != OriginCache {
		t.Errorf("expected origin %q offline with a cache, got %q", OriginCache, origin)
	}
	assertRemoteShape(t, sources)

	if got := srv.requests.Load(); got != 1 {
		t.Errorf("expected the offline load to make no request, the server saw %d in total", got)
	}
}

func TestLoaderOfflineWithNoCacheUsesEmbedded(t *testing.T) {
	srv := newETagServer(t)
	loader := &Loader{URL: srv.URL, CacheDir: t.TempDir(), Offline: true}

	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginEmbedded {
		t.Errorf("expected origin %q offline with no cache, got %q", OriginEmbedded, origin)
	}
	assertEmbeddedShape(t, sources)
	if got := srv.requests.Load(); got != 0 {
		t.Errorf("expected no request at all offline, the server saw %d", got)
	}
}

func TestLoaderSourcesFileWinsOverEverything(t *testing.T) {
	srv := newETagServer(t)

	path := filepath.Join(t.TempDir(), "sources.json")
	fileJSON := `{"eu": {"npm": ["https://file.example/npm"]}}`
	if err := os.WriteFile(path, []byte(fileJSON), 0o644); err != nil {
		t.Fatalf("failed to write the sources file: %v", err)
	}

	loader := &Loader{URL: srv.URL, CacheDir: t.TempDir(), FilePath: path}
	sources, origin, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if origin != OriginFile {
		t.Errorf("expected origin %q, got %q", OriginFile, origin)
	}
	if got := (*sources)[structs.EU]["npm"]; len(got) == 0 || got[0] != "https://file.example/npm" {
		t.Errorf("expected the file's npm url, got %v", got)
	}
	if got := srv.requests.Load(); got != 0 {
		t.Errorf("expected no request when a sources file is named, the server saw %d", got)
	}
	if _, err := os.Stat(loader.CachePath()); !os.IsNotExist(err) {
		t.Errorf("expected no cache to be written for a sources file, stat returned %v", err)
	}
}

func TestLoaderSourcesFileFailureIsFatal(t *testing.T) {
	loader := &Loader{CacheDir: t.TempDir(), FilePath: filepath.Join(t.TempDir(), "missing.json")}
	if _, _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("expected a missing sources file to be an error, got nil")
	}
}

func TestLoaderWithNoCacheDirNeverTouchesDisk(t *testing.T) {
	srv := newETagServer(t)
	loader := &Loader{URL: srv.URL}

	if got := loader.CachePath(); got != "" {
		t.Errorf("expected no cache path without a cache dir, got %q", got)
	}
	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginRemote {
		t.Errorf("expected a plain remote load, got origin %q, err %v", origin, err)
	}
}

func TestLoadRegistrySourcesHonoursConfigDirEnvVar(t *testing.T) {
	srv := newETagServer(t)
	withRemote(t, srv.URL)

	dir := filepath.Join(t.TempDir(), "regtool")
	t.Setenv(ConfigDirEnvVar, dir)

	if _, err := LoadRegistrySources(context.Background()); err != nil {
		t.Fatalf("LoadRegistrySources returned error: %v", err)
	}

	path := filepath.Join(dir, CacheFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the cache under %s, stat returned %v", ConfigDirEnvVar, err)
	}
	if got := NewLoader().CachePath(); got != path {
		t.Errorf("expected the loader to cache at %q, got %q", path, got)
	}
}

func TestLoadRegistrySourcesHonoursSourcesURLEnvVar(t *testing.T) {
	srv := newETagServer(t)
	t.Setenv(SourcesURLEnvVar, srv.URL)
	t.Setenv(ConfigDirEnvVar, t.TempDir())

	sources, err := LoadRegistrySources(context.Background())
	if err != nil {
		t.Fatalf("LoadRegistrySources returned error: %v", err)
	}
	assertRemoteShape(t, sources)
	if got := srv.requests.Load(); got != 1 {
		t.Errorf("expected one request to the url from the environment, got %d", got)
	}
}

func TestRefreshIgnoresTheCachedETag(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	if _, _, err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	result, err := loader.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if got := srv.conditional.Load(); got != 0 {
		t.Errorf("expected refresh to fetch unconditionally, it sent %d conditional requests", got)
	}
	if result.Changed {
		t.Error("expected refresh to report no change when the document is the same")
	}
	if result.ETag != `"v1"` {
		t.Errorf(`expected etag "v1", got %q`, result.ETag)
	}
	if result.Regions != 1 || result.Apps != 4 {
		t.Errorf("expected 1 region and 4 apps, got %d and %d", result.Regions, result.Apps)
	}
	if result.CachePath != loader.CachePath() {
		t.Errorf("expected the cache path %q, got %q", loader.CachePath(), result.CachePath)
	}
}

func TestRefreshReportsAChangedDocument(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	result, err := loader.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if !result.Changed {
		t.Error("expected the first refresh, which creates the cache, to report a change")
	}

	srv.serve(`"v2"`, `{"cn": {"npm": ["https://remote.example/npm-2"]}}`)

	result, err = loader.Refresh(context.Background())
	if err != nil {
		t.Fatalf("second Refresh returned error: %v", err)
	}
	if !result.Changed {
		t.Error("expected a new document to be reported as a change")
	}

	cache := readCacheFile(t, loader)
	if got := cache.Sources[structs.CN]["npm"]; len(got) == 0 || got[0] != "https://remote.example/npm-2" {
		t.Errorf("expected the refreshed cache to hold the new url, got %v", got)
	}
}

func TestRefreshDoesNotFallBack(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	if _, _, err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	srv.Close()

	if _, err := loader.Refresh(context.Background()); err == nil {
		t.Fatal("expected refresh to fail when the server cannot be reached, got nil")
	}
}

func TestRefreshRefusesOfflineAndSourcesFile(t *testing.T) {
	offline := &Loader{URL: "https://example.invalid", CacheDir: t.TempDir(), Offline: true}
	_, err := offline.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), OfflineEnvVar) {
		t.Errorf("expected an error naming %s, got %v", OfflineEnvVar, err)
	}

	fromFile := &Loader{CacheDir: t.TempDir(), FilePath: "sources.json"}
	_, err = fromFile.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), SourcesFileEnvVar) {
		t.Errorf("expected an error naming %s, got %v", SourcesFileEnvVar, err)
	}
}

func TestDescribeReportsTheCacheState(t *testing.T) {
	srv := newETagServer(t)
	loader := newLoader(t, srv.URL)

	before := time.Now().UTC().Add(-time.Second)

	status, err := loader.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if status.Origin != OriginRemote {
		t.Errorf("expected origin %q, got %q", OriginRemote, status.Origin)
	}
	if status.URL != srv.URL {
		t.Errorf("expected url %q, got %q", srv.URL, status.URL)
	}
	if status.CachePath != loader.CachePath() {
		t.Errorf("expected cache path %q, got %q", loader.CachePath(), status.CachePath)
	}
	if status.ETag != `"v1"` {
		t.Errorf(`expected etag "v1", got %q`, status.ETag)
	}
	if status.FetchedAt == nil || status.FetchedAt.Before(before) {
		t.Errorf("expected a fetched timestamp after %s, got %v", before, status.FetchedAt)
	}
	if status.Regions != 1 || status.Apps != 4 {
		t.Errorf("expected 1 region and 4 apps, got %d and %d", status.Regions, status.Apps)
	}
	if status.Offline {
		t.Error("expected offline to be false")
	}
}

func TestDescribeWithNoCacheReportsNothingCached(t *testing.T) {
	loader := &Loader{URL: "https://example.invalid", CacheDir: t.TempDir(), Offline: true}

	status, err := loader.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if status.Origin != OriginEmbedded {
		t.Errorf("expected origin %q, got %q", OriginEmbedded, status.Origin)
	}
	if status.ETag != "" || status.FetchedAt != nil {
		t.Errorf("expected no cache state, got etag %q and fetched %v", status.ETag, status.FetchedAt)
	}
	if !status.Offline {
		t.Error("expected offline to be true")
	}
	if status.Regions != 3 || status.Apps != 11 {
		t.Errorf("expected the embedded 3 regions and 11 apps, got %d and %d", status.Regions, status.Apps)
	}
}

// roundTripFunc turns a function into an [http.RoundTripper].
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoaderUsesTheInjectedClient(t *testing.T) {
	srv := newETagServer(t)

	var used atomic.Bool
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		used.Store(true)
		return nil, errors.New("the injected client refuses to travel")
	})}
	loader := &Loader{URL: srv.URL, CacheDir: t.TempDir(), Client: client}

	if _, origin, err := loader.Load(context.Background()); err != nil || origin != OriginEmbedded {
		t.Errorf("expected the injected client's failure to reach the embedded copy, got origin %q, err %v", origin, err)
	}
	if !used.Load() {
		t.Error("expected the injected client to perform the fetch")
	}
	if got := srv.requests.Load(); got != 0 {
		t.Errorf("expected the server to see no request, it saw %d", got)
	}
}
