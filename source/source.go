package source

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regtool/common/alias"
	"regtool/console"
	"regtool/source/structs"
	"time"
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

// LoadRegistrySources returns the remote sources when they are reachable and
// falls back to the embedded copy otherwise.
func LoadRegistrySources(ctx context.Context) (*structs.RegistrySources, error) {
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

// ConvertSources converts sources to a map of package managers to sources
func ConvertSources(sources *structs.RegistrySources) map[string]Source {
	result := make(map[string]Source)
	for region, registryRegion := range *sources {
		for packageManager, urls := range registryRegion {
			result[packageManager] = Source{
				Region: string(region),
				Url:    urls[0],
				Name:   packageManager,
			}
		}
	}
	return result
}

var SOURCES map[string]Source

func GetRemoteSourcesMap(ctx context.Context) (map[string]Source, error) {
	sources, err := LoadRegistrySources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load registry sources: %w", err)
	}
	SOURCES = ConvertSources(sources)
	return SOURCES, nil
}

type Source struct {
	Region string
	Url    string
	Name   string
}

var registryManagers = map[string]AppManager{}

// RegisterManager registers a manager for the given names
func RegisterManager(names []string, manager AppManager) {
	for _, name := range names {
		registryManagers[name] = manager
	}
}

func UpdateRegistry(region string, app string) error {
	regionValue, ok := structs.StringToRegion(region)
	if !ok {
		return fmt.Errorf("unknown region: %s", region)
	}

	ctx := context.Background()

	rs, err := LoadRegistrySources(ctx)
	if err != nil {
		return fmt.Errorf("failed to load registry sources: %w", err)
	}

	primaryApp := alias.GetPrimary(app)
	names := append([]string{primaryApp}, alias.GetAllAliases(primaryApp)...)

	var errs []error
	for _, name := range names {
		registryManager, ok := registryManagers[name]
		if !ok {
			errs = append(errs, fmt.Errorf("no registry manager registered for %q", name))
			continue
		}

		if _, err := registryManager.SetRegistry(regionValue, rs); err != nil {
			errs = append(errs, fmt.Errorf("failed to set %s registry to region %s: %w", name, region, err))
		}
	}

	return errors.Join(errs...)
}

// Get All Registered
func GetAllRegisteredApp() map[string]AppManager {

	res := make(map[string]AppManager)
	for _, appName := range alias.GetAllPrimary() {
		if manager, ok := registryManagers[appName]; ok {
			if !manager.IsExists() {
				continue
			}
			res[appName] = manager
		}
	}
	return res
}
