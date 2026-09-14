// Package source provides the list of registry mirrors regtool switches
// between: a remotely maintained sources.json with the copy embedded in the
// binary as a fallback.
package source

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/ZHallen122/RegTool/console"
	"github.com/ZHallen122/RegTool/source/structs"
)

// defaultSourcesJSON is the bundled copy of the registry sources. It is used as
// a fallback whenever the remote sources cannot be fetched or parsed.
//
//go:embed sources.json
var defaultSourcesJSON []byte

// remoteSourcesURL points at the canonical, remotely maintained sources file.
// It is a package var so tests can point it at a local httptest server.
var remoteSourcesURL = "https://gitee.com/Sma1lboyyy/registry-hub/raw/main/sources.json"

// httpClient is used for every remote sources fetch. It is a package var so
// tests can substitute a client of their own.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// GetEmbeddedRegistrySources returns the sources bundled with the binary.
func GetEmbeddedRegistrySources() (*structs.RegistrySources, error) {
	var sources structs.RegistrySources
	if err := json.Unmarshal(defaultSourcesJSON, &sources); err != nil {
		return nil, fmt.Errorf("failed to parse embedded sources: %w", err)
	}
	return &sources, nil
}

// GetRemoteRegistrySources fetches the remote sources and returns them.
func GetRemoteRegistrySources(ctx context.Context) (*structs.RegistrySources, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteSourcesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for %s: %w", remoteSourcesURL, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch remote sources from %s: %w", remoteSourcesURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch remote sources from %s: unexpected status %s", remoteSourcesURL, resp.Status)
	}

	var sources structs.RegistrySources
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		return nil, fmt.Errorf("failed to decode remote sources from %s: %w", remoteSourcesURL, err)
	}
	return &sources, nil
}

// OfflineEnvVar, when set to a non-empty value, makes LoadRegistrySources skip
// the remote fetch entirely and use the embedded sources. Tests rely on it so
// they never touch the network.
const OfflineEnvVar = "REGTOOL_OFFLINE"

// SourcesFileEnvVar names a sources.json on disk to use instead of the remote
// and embedded ones. It exists for the end-to-end tests, which need mirrors
// pointing at servers that only exist while the test runs, and it takes
// precedence over OfflineEnvVar.
const SourcesFileEnvVar = "REGTOOL_SOURCES_FILE"

// GetFileRegistrySources reads a sources.json from disk.
func GetFileRegistrySources(path string) (*structs.RegistrySources, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read the sources file %s: %w", path, err)
	}
	var sources structs.RegistrySources
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, fmt.Errorf("failed to parse the sources file %s: %w", path, err)
	}
	return &sources, nil
}

// LoadRegistrySources returns the remote sources when they are reachable and
// falls back to the embedded copy otherwise.
func LoadRegistrySources(ctx context.Context) (*structs.RegistrySources, error) {
	if path := os.Getenv(SourcesFileEnvVar); path != "" {
		return GetFileRegistrySources(path)
	}
	if os.Getenv(OfflineEnvVar) != "" {
		return GetEmbeddedRegistrySources()
	}

	sources, err := GetRemoteRegistrySources(ctx)
	if err == nil {
		return sources, nil
	}

	console.Warning("Falling back to built-in sources:", err.Error())

	embedded, embedErr := GetEmbeddedRegistrySources()
	if embedErr != nil {
		return nil, errors.Join(err, embedErr)
	}
	return embedded, nil
}
