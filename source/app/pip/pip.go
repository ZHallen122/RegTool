package pip

import (
	"errors"
	"fmt"
	"github.com/ZHallen122/RegTool/common/alias"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"
	"os/exec"
	"strings"
)

type PipRegistryManager struct{}

// runPip runs pip with the given arguments and returns its trimmed stdout.
// On failure the error carries the command line and pip's stderr.
func runPip(args ...string) (string, error) {
	output, err := exec.Command("pip", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("pip %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("pip %s failed: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (n PipRegistryManager) GetCurrRegistry() (string, error) {
	return runPip("config", "get", "global.index-url")
}

func (n PipRegistryManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	if sources == nil {
		return "", fmt.Errorf("sources is nil")
	}
	regionSources, ok := (*sources)[region]
	if !ok {
		return "", fmt.Errorf("unsupported region: %s", region)
	}

	pipSources, ok := regionSources["pip"]
	if !ok || len(pipSources) == 0 {
		return "", fmt.Errorf("pip sources not found for region: %s", region)
	}

	res := pipSources[0]

	if _, err := runPip("config", "set", "global.index-url", res); err != nil {
		return "", err
	}
	return res, nil
}
func (n PipRegistryManager) IsExists() bool {

	_, err := exec.Command("pip", "config", "get", "global.index-url").Output()

	return err == nil
}

func init() {
	alias.RegisterAlias("pip", []string{})
	source.RegisterManager([]string{"pip"}, PipRegistryManager{})
}
