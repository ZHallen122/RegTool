package source

import (
	"context"
	"fmt"
	"regtool/source/localdata"
)

// Convert map[Name]Source
func convertLocalSources(ctx context.Context, sources map[string]string) (map[string]Source, error) {
	if SOURCES == nil {
		if _, err := GetRemoteSourcesMap(ctx); err != nil {
			return nil, fmt.Errorf("failed to load registry sources: %w", err)
		}
	}

	result := make(map[string]Source)
	for name, url := range sources {
		if SOURCES[url] != (Source{}) {
			result[name] = Source{
				Region: SOURCES[url].Region,
				Url:    SOURCES[url].Url,
				Name:   SOURCES[url].Name,
			}
		} else {
			result[name] = Source{
				Region: "local",
				Url:    url,
				Name:   name,
			}
		}
	}
	return result, nil
}

func GetLocalSourcesMap() (map[string]Source, error) {
	sources, err := localdata.ReadBackupFile()
	if err != nil {
		return nil, fmt.Errorf("failed to read backup file: %w", err)
	}
	return convertLocalSources(context.Background(), sources)
}
