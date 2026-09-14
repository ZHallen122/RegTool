package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ZHallen122/RegTool/console"
	"github.com/ZHallen122/RegTool/source/structs"
)

// Origin says where a loaded set of registry sources came from.
type Origin string

const (
	// OriginFile means the sources were read from the file named by
	// [SourcesFileEnvVar].
	OriginFile Origin = "file"
	// OriginRemote means they were fetched from the sources URL.
	OriginRemote Origin = "remote"
	// OriginCache means they came from the local cache, either because the
	// server answered 304, because it could not be reached, or because the
	// fetch was skipped altogether.
	OriginCache Origin = "cache"
	// OriginEmbedded means they are the copy bundled with the binary.
	OriginEmbedded Origin = "embedded"
)

// SourcesURLEnvVar names the sources URL to fetch instead of the default one.
// It is what points regtool at a self-hosted sources service.
const SourcesURLEnvVar = "REGTOOL_SOURCES_URL"

// ConfigDirEnvVar names the directory regtool keeps its own files in, the
// sources cache among them. When it is empty the directory is
// <os.UserConfigDir>/regtool.
const ConfigDirEnvVar = "REGTOOL_CONFIG_DIR"

// CacheFileName is the file, inside the configuration directory, that the
// fetched sources are cached in.
const CacheFileName = "sources.cache.json"

const (
	cacheDirPerm  = fs.FileMode(0o755)
	cacheFilePerm = fs.FileMode(0o644)
)

// errNotModified reports that the server answered 304 because the cached ETag
// is still current.
var errNotModified = errors.New("not modified")

// Cache is the on-disk sources cache: the fetched document together with
// everything needed to decide whether it is still usable. It is one file, so a
// half-written update can never pair an ETag with someone else's sources.
type Cache struct {
	// URL is the sources URL the document was fetched from. A cache written
	// for a different URL is ignored rather than served.
	URL string `json:"url"`
	// ETag is what the server last labelled the document with, sent back as
	// If-None-Match on the next fetch. It is empty when the server sent none.
	ETag string `json:"etag,omitempty"`
	// FetchedAt is when the document was last fetched, in UTC.
	FetchedAt time.Time `json:"fetched_at"`
	// Sources is the document itself.
	Sources structs.RegistrySources `json:"sources"`
}

// Loader resolves the registry mirrors regtool switches between. It goes
// through the sources file, the network, the local cache and the embedded copy
// in that order, and says which of them it ended up using.
//
// Every field is optional: the zero value fetches the default URL with the
// package's own client and caches nothing.
type Loader struct {
	// URL is the sources document to fetch. Empty means [SourcesURLEnvVar],
	// and the built-in default when that is empty too.
	URL string
	// CacheDir is the directory holding [CacheFileName]. Empty disables the
	// cache entirely, which is what tests that want no disk at all pass.
	CacheDir string
	// Client performs the fetch. Empty means the package's own client, which
	// gives up after ten seconds.
	Client *http.Client
	// Offline skips the network: the cache is used when it exists and the
	// embedded copy otherwise.
	Offline bool
	// FilePath names a sources file on disk. It wins over everything else.
	FilePath string
}

// NewLoader builds the Loader the commands run against from the environment.
func NewLoader() *Loader {
	return &Loader{
		URL:      os.Getenv(SourcesURLEnvVar),
		CacheDir: DefaultCacheDir(),
		Offline:  os.Getenv(OfflineEnvVar) != "",
		FilePath: os.Getenv(SourcesFileEnvVar),
	}
}

// DefaultCacheDir is the directory regtool keeps its own files in:
// [ConfigDirEnvVar] when it is set, and <os.UserConfigDir>/regtool otherwise.
// It is empty on a machine where the user configuration directory cannot be
// located, which simply means nothing is cached.
func DefaultCacheDir() string {
	if dir := os.Getenv(ConfigDirEnvVar); dir != "" {
		return dir
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfg, "regtool")
}

// URLInUse reports the sources URL the loader fetches from.
func (l *Loader) URLInUse() string {
	if l.URL != "" {
		return l.URL
	}
	if url := os.Getenv(SourcesURLEnvVar); url != "" {
		return url
	}
	return remoteSourcesURL
}

// CachePath is the file the fetched sources are cached in, empty when the
// loader has nowhere to cache them.
func (l *Loader) CachePath() string {
	if l.CacheDir == "" {
		return ""
	}
	return filepath.Join(l.CacheDir, CacheFileName)
}

func (l *Loader) client() *http.Client {
	if l.Client != nil {
		return l.Client
	}
	return httpClient
}

// Load returns the registry mirrors and says where they came from.
//
// A sources file named by [SourcesFileEnvVar] is used on its own and a failure
// to read it is fatal, because a caller that named a file meant that file.
// Otherwise the remote document is fetched, conditionally on the cached ETag,
// and stored in the cache. Every way that can fail - no network, a refusal, a
// body that is not usable sources - falls back to the cache and then to the
// copy embedded in the binary, with a warning saying why.
func (l *Loader) Load(ctx context.Context) (*structs.RegistrySources, Origin, error) {
	if l.FilePath != "" {
		sources, err := GetFileRegistrySources(l.FilePath)
		if err != nil {
			return nil, "", err
		}
		return sources, OriginFile, nil
	}

	cached := l.cachedOrNil()

	if l.Offline {
		if cached != nil {
			return &cached.Sources, OriginCache, nil
		}
		return l.embedded()
	}

	sources, etag, err := l.fetch(ctx, cachedETag(cached))
	if err == nil {
		l.store(&Cache{URL: l.URLInUse(), ETag: etag, FetchedAt: time.Now().UTC(), Sources: *sources})
		return sources, OriginRemote, nil
	}

	if cached != nil {
		if !errors.Is(err, errNotModified) {
			console.Warning("Falling back to the cached sources:", err.Error())
		}
		return &cached.Sources, OriginCache, nil
	}

	console.Warning("Falling back to built-in sources:", err.Error())
	embedded, origin, embedErr := l.embedded()
	if embedErr != nil {
		return nil, "", errors.Join(err, embedErr)
	}
	return embedded, origin, nil
}

func (l *Loader) embedded() (*structs.RegistrySources, Origin, error) {
	sources, err := GetEmbeddedRegistrySources()
	if err != nil {
		return nil, "", err
	}
	return sources, OriginEmbedded, nil
}

// fetch gets the sources document, conditionally when etag is not empty. A 304
// comes back as [errNotModified], which the caller answers from the cache.
func (l *Loader) fetch(ctx context.Context, etag string) (*structs.RegistrySources, string, error) {
	url := l.URLInUse()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to build request for %s: %w", url, err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := l.client().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch the sources from %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, fmt.Errorf("%s answered %s: %w", url, resp.Status, errNotModified)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("failed to fetch the sources from %s: unexpected status %s", url, resp.Status)
	}

	var sources structs.RegistrySources
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		return nil, "", fmt.Errorf("failed to decode the sources from %s: %w", url, err)
	}
	if err := validate(&sources); err != nil {
		return nil, "", fmt.Errorf("the sources from %s are unusable: %w", url, err)
	}
	return &sources, resp.Header.Get("ETag"), nil
}

// validate rejects a sources document that could not point a single package
// manager anywhere.
func validate(sources *structs.RegistrySources) error {
	if sources == nil || len(*sources) == 0 {
		return errors.New("no regions at all")
	}
	for _, regionSources := range *sources {
		if len(regionSources) > 0 {
			return nil
		}
	}
	return errors.New("every region is empty")
}

// cachedOrNil returns the usable cache, or nil after warning about the reason
// there is none. A cache is never a good enough reason to fail: it is an
// optimisation with a full fallback chain behind it.
func (l *Loader) cachedOrNil() *Cache {
	cache, err := l.ReadCache()
	if err != nil {
		console.Warning("Ignoring the sources cache:", err.Error())
		return nil
	}
	return cache
}

// ReadCache returns the cached sources, or nil when there is no cache, when it
// was written for a different sources URL, or when caching is disabled.
func (l *Loader) ReadCache() (*Cache, error) {
	path := l.CachePath()
	if path == "" {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the sources cache %s: %w", path, err)
	}

	var cache Cache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse the sources cache %s: %w", path, err)
	}
	if cache.URL != l.URLInUse() {
		// The cache describes a different sources URL, so it says nothing
		// about the one in use.
		return nil, nil
	}
	if err := validate(&cache.Sources); err != nil {
		return nil, fmt.Errorf("the sources cache %s is unusable: %w", path, err)
	}
	return &cache, nil
}

func cachedETag(cache *Cache) string {
	if cache == nil {
		return ""
	}
	return cache.ETag
}

// store writes the cache and warns instead of failing: a machine with a
// read-only configuration directory should still get its sources.
func (l *Loader) store(cache *Cache) {
	if err := l.writeCache(cache); err != nil {
		console.Warning("Failed to cache the sources:", err.Error())
	}
}

func (l *Loader) writeCache(cache *Cache) error {
	path := l.CachePath()
	if path == "" {
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, cacheDirPerm); err != nil {
		return fmt.Errorf("failed to create the config directory %s: %w", dir, err)
	}

	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the sources cache: %w", err)
	}
	if err := atomicWrite(path, append(raw, '\n'), cacheFilePerm); err != nil {
		return fmt.Errorf("failed to write the sources cache %s: %w", path, err)
	}
	return nil
}

// Status describes where the registry mirrors come from and what the cache
// holds, which is what `regtool sources` prints.
type Status struct {
	// URL is the sources URL in effect.
	URL string `json:"url"`
	// Origin is where the sources actually came from on this run.
	Origin Origin `json:"origin"`
	// Offline reports that the network was skipped.
	Offline bool `json:"offline"`
	// File is the sources file in use, empty unless one was named.
	File string `json:"file,omitempty"`
	// CachePath is the cache file, empty when caching is disabled.
	CachePath string `json:"cachePath,omitempty"`
	// ETag is what the cached document is labelled with, empty when there is
	// no cache or the server sent none.
	ETag string `json:"etag,omitempty"`
	// FetchedAt is when the cache was last written, nil when there is none.
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	// Regions and Apps count what was loaded.
	Regions int `json:"regions"`
	Apps    int `json:"apps"`
}

// Describe loads the sources and reports where they came from.
func (l *Loader) Describe(ctx context.Context) (*Status, error) {
	sources, origin, err := l.Load(ctx)
	if err != nil {
		return nil, err
	}

	status := &Status{
		URL:       l.URLInUse(),
		Origin:    origin,
		Offline:   l.Offline,
		File:      l.FilePath,
		CachePath: l.CachePath(),
		Regions:   len(*sources),
		Apps:      countApps(sources),
	}

	if cache, err := l.ReadCache(); err == nil && cache != nil {
		fetchedAt := cache.FetchedAt
		status.ETag, status.FetchedAt = cache.ETag, &fetchedAt
	}
	return status, nil
}

// RefreshResult reports what a forced fetch did to the cache.
type RefreshResult struct {
	URL       string    `json:"url"`
	CachePath string    `json:"cachePath,omitempty"`
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
	// Changed reports that the cache now holds something other than what it
	// held before, either different sources or a different ETag.
	Changed bool `json:"changed"`
	Regions int  `json:"regions"`
	Apps    int  `json:"apps"`
}

// Refresh fetches the sources unconditionally, ignoring the cached ETag, and
// rewrites the cache with what came back. Unlike [Loader.Load] it does not fall
// back: a refresh that could not reach the server is a failure, because
// silently keeping the cache is exactly what the caller asked not to do.
func (l *Loader) Refresh(ctx context.Context) (*RefreshResult, error) {
	if l.FilePath != "" {
		return nil, fmt.Errorf("%s points at %s, so there is no remote sources document to refresh", SourcesFileEnvVar, l.FilePath)
	}
	if l.Offline {
		return nil, fmt.Errorf("%s is set, so the sources cannot be refreshed", OfflineEnvVar)
	}

	previous := l.cachedOrNil()

	sources, etag, err := l.fetch(ctx, "")
	if err != nil {
		return nil, err
	}

	cache := &Cache{URL: l.URLInUse(), ETag: etag, FetchedAt: time.Now().UTC(), Sources: *sources}
	if err := l.writeCache(cache); err != nil {
		return nil, err
	}

	return &RefreshResult{
		URL:       cache.URL,
		CachePath: l.CachePath(),
		ETag:      cache.ETag,
		FetchedAt: cache.FetchedAt,
		Changed:   differs(previous, cache),
		Regions:   len(*sources),
		Apps:      countApps(sources),
	}, nil
}

// differs reports whether next holds anything other than what previous held.
// No cache at all counts as a change: there is now one.
func differs(previous, next *Cache) bool {
	if previous == nil || previous.ETag != next.ETag {
		return true
	}
	before, beforeErr := json.Marshal(previous.Sources)
	after, afterErr := json.Marshal(next.Sources)
	if beforeErr != nil || afterErr != nil {
		return true
	}
	return !bytes.Equal(before, after)
}

// countApps counts the distinct registry keys across every region.
func countApps(sources *structs.RegistrySources) int {
	seen := make(map[string]struct{})
	for _, regionSources := range *sources {
		for key := range regionSources {
			seen[key] = struct{}{}
		}
	}
	return len(seen)
}

// atomicWrite writes data to path through a temporary file in the same
// directory followed by a rename, so a reader never sees a half-written cache.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".regtool-sources-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// Windows refuses to rename over some existing files; drop the target
		// and try once more.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fmt.Errorf("replace %s: %w", path, err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
		}
	}
	tmpName = "" // Renamed into place; nothing left to clean up.
	return nil
}
