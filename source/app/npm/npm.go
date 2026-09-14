// source/npm.go
package npm

import (
	"errors"
	"fmt"
	"github.com/ZHallen122/RegTool/common/alias"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"
	"os/exec"
	"strings"
)

type NpmRegistryManager struct{}

// runNpm runs npm with the given arguments and returns its trimmed stdout.
// On failure the error carries the command line and npm's stderr.
func runNpm(args ...string) (string, error) {
	output, err := exec.Command("npm", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("npm %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("npm %s failed: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (n NpmRegistryManager) GetCurrRegistry() (string, error) {
	return runNpm("config", "get", "registry")
}

func (n NpmRegistryManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	if sources == nil {
		return "", fmt.Errorf("sources is nil")
	}
	regionSources, ok := (*sources)[region]
	if !ok {
		return "", fmt.Errorf("unsupported region: %s", region)
	}

	npmSources, ok := regionSources["npm"]
	if !ok || len(npmSources) == 0 {
		return "", fmt.Errorf("npm sources not found for region: %s", region)
	}

	res := npmSources[0]

	if _, err := runNpm("config", "set", "registry", res); err != nil {
		return "", err
	}
	return res, nil
}
func (n NpmRegistryManager) IsExists() bool {

	_, err := exec.Command("npm", "config", "get", "registry").Output()

	return err == nil
}

func init() {
	alias.RegisterAlias("npm", []string{})
	source.RegisterManager([]string{"npm"}, NpmRegistryManager{})
}
