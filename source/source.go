// Package source provides the list of registry mirrors regtool switches
// between. The list is maintained remotely; a local cache and the copy embedded
// in the binary stand behind it, so the tool keeps working with no network and
// on a machine that has never had one. See [Loader] for the whole chain.
package source

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/ZHallen122/RegTool/source/structs"
)

// defaultSourcesJSON is the bundled copy of the registry sources. It is used as
// a fallback whenever the remote sources cannot be fetched or parsed.
//
//go:embed sources.json
var defaultSourcesJSON []byte

// remoteSourcesURL points at the canonical, remotely maintained sources file.
// It is what [SourcesURLEnvVar] overrides, and it is a package var so tests can
// point it at a local httptest server.
var remoteSourcesURL = "https://gitee.com/Sma1lboyyy/registry-hub/raw/main/sources.json"

// httpClient is used for every remote sources fetch. It is a package var so
// tests can substitute a client of their own, and [Loader.Client] overrides it
// per loader.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// GetEmbeddedRegistrySources returns the sources bundled with the binary.
func GetEmbeddedRegistrySources() (*structs.RegistrySources, error) {
	var sources structs.RegistrySources
	if err := json.Unmarshal(defaultSourcesJSON, &sources); err != nil {
		return nil, fmt.Errorf("failed to parse embedded sources: %w", err)
	}
	return &sources, nil
}

// GetRemoteRegistrySources fetches the sources from the URL in effect and
// returns them. It is the unconditional fetch: no cache is read, none is
// written and nothing is fallen back to.
func GetRemoteRegistrySources(ctx context.Context) (*structs.RegistrySources, error) {
	sources, _, err := (&Loader{}).fetch(ctx, "")
	return sources, err
}

// OfflineEnvVar, when set to a non-empty value, makes LoadRegistrySources skip
// the remote fetch entirely and use the cached or embedded sources. Tests rely
// on it so they never touch the network.
const OfflineEnvVar = "REGTOOL_OFFLINE"

// SourcesFileEnvVar names a sources.json on disk to use instead of the remote,
// cached and embedded ones. It exists for the end-to-end tests, which need
// mirrors pointing at servers that only exist while the test runs, and it takes
// precedence over everything else.
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

// LoadRegistrySources returns the registry mirrors, using the environment to
// decide where they come from. It is [Loader] with the origin dropped, for the
// callers that only want the mirrors.
func LoadRegistrySources(ctx context.Context) (*structs.RegistrySources, error) {
	sources, _, err := NewLoader().Load(ctx)
	return sources, err
}
