package yarn

import (
	"errors"
	"fmt"
	"github.com/ZHallen122/RegTool/common/alias"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"
	"os/exec"
	"strings"
)

type YarnRegistryManager struct{}

// runYarn runs yarn with the given arguments and returns its trimmed stdout.
// On failure the error carries the command line and yarn's stderr.
func runYarn(args ...string) (string, error) {
	output, err := exec.Command("yarn", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("yarn %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("yarn %s failed: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (n YarnRegistryManager) GetCurrRegistry() (string, error) {
	return runYarn("config", "get", "registry")
}

func (n YarnRegistryManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	if sources == nil {
		return "", fmt.Errorf("sources is nil")
	}
	regionSources, ok := (*sources)[region]
	if !ok {
		return "", fmt.Errorf("unsupported region: %s", region)
	}

	// yarn shares the npm registry protocol, so fall back to the npm entry when
	// the region has no yarn-specific source.
	yarnSources, ok := regionSources["yarn"]
	if !ok || len(yarnSources) == 0 {
		yarnSources, ok = regionSources["npm"]
		if !ok || len(yarnSources) == 0 {
			return "", fmt.Errorf("yarn sources not found for region: %s", region)
		}
	}

	res := yarnSources[0]

	if _, err := runYarn("config", "set", "registry", res); err != nil {
		return "", err
	}
	return res, nil
}
func (n YarnRegistryManager) IsExists() bool {

	_, err := exec.Command("yarn", "config", "get", "registry").Output()

	return err == nil
}

func init() {
	alias.RegisterAlias("yarn", []string{})
	source.RegisterManager([]string{"yarn"}, YarnRegistryManager{})
}
