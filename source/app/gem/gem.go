package gem

import (
	"errors"
	"fmt"
	"os/exec"
	"regtool/common/alias"
	"regtool/console"
	"regtool/source"
	"regtool/source/structs"
	"strings"
)

type GemRegistryManager struct{}

// runGem runs gem with the given arguments and returns its stdout.
// On failure the error carries the command line and gem's stderr.
func runGem(args ...string) (string, error) {
	output, err := exec.Command("gem", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("gem %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("gem %s failed: %w", strings.Join(args, " "), err)
	}
	return string(output), nil
}

func (g GemRegistryManager) GetCurrRegistry() (string, error) {
	sources, err := g.getCurrentSources()
	if err != nil {
		return "", err
	}
	if len(sources) == 0 {
		return "", fmt.Errorf("no valid source found")
	}
	return sources[0], nil
}

func (g GemRegistryManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	if sources == nil {
		return "", fmt.Errorf("sources is nil")
	}
	regionSources, ok := (*sources)[region]
	if !ok {
		return "", fmt.Errorf("unsupported region: %s", region)
	}
	gemSources, ok := regionSources["gem"]
	if !ok || len(gemSources) == 0 {
		return "", fmt.Errorf("gem sources not found for region: %s", region)
	}
	newSource := gemSources[0]

	// Get current sources
	currentSources, err := g.getCurrentSources()
	if err != nil {
		return "", fmt.Errorf("error getting current sources: %w", err)
	}

	// Remove all current sources. A source that refuses to be removed is not
	// fatal: the new source is still added below.
	for _, src := range currentSources {
		if _, err := runGem("sources", "--remove", src); err != nil {
			console.Warning("Could not remove gem source", src+":", err.Error())
		}
	}

	// Add the new source
	if _, err := runGem("sources", "--add", newSource); err != nil {
		return "", fmt.Errorf("failed to add gem source %s: %w", newSource, err)
	}

	return newSource, nil
}

func (g GemRegistryManager) IsExists() bool {
	_, err := exec.Command("gem", "sources", "-l").Output()
	return err == nil
}

func (g GemRegistryManager) getCurrentSources() ([]string, error) {
	output, err := runGem("sources", "-l")
	if err != nil {
		return nil, err
	}

	var sources []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "https://") {
			sources = append(sources, strings.TrimSpace(line))
		}
	}

	return sources, nil
}

func init() {
	alias.RegisterAlias("gem", []string{"rubygems"})
	source.RegisterManager([]string{"gem", "rubygems"}, GemRegistryManager{})
}
