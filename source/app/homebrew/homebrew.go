package source

import (
	"fmt"
	"github.com/ZHallen122/RegTool/common/alias"
	"github.com/ZHallen122/RegTool/shell"
	"github.com/ZHallen122/RegTool/source"
	"github.com/ZHallen122/RegTool/source/structs"
	"os/exec"
	"strings"
)

// HomebrewRegistryManager manages the Homebrew registry
type HomebrewRegistryManager struct{}

// GetCurrRegistry gets the current Homebrew registry
func (h HomebrewRegistryManager) GetCurrRegistry() (string, error) {
	cmd := exec.Command("brew", "config")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error running brew config: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "HOMEBREW_API_DOMAIN:") {
			return strings.TrimSpace(strings.Split(line, ":")[1]), nil
		}
	}
	return "", fmt.Errorf("HOMEBREW_API_DOMAIN not found in brew config")
}

// SetRegistry sets the Homebrew registry to the specified URLs from the given region
func (h HomebrewRegistryManager) SetRegistry(region structs.Region, sources *structs.RegistrySources) (string, error) {
	if sources == nil {
		return "", fmt.Errorf("sources is nil")
	}
	regionSources, ok := (*sources)[region]
	if !ok {
		return "", fmt.Errorf("unsupported region: %s", region)
	}

	shellManager, err := shell.NewShellManager()
	if err != nil {
		return "", fmt.Errorf("unsupported shell: %w", err)
	}

	envVars := []string{
		"HOMEBREW_API_DOMAIN",
		"HOMEBREW_BOTTLE_DOMAIN",
		"HOMEBREW_BREW_GIT_REMOTE",
		"HOMEBREW_CORE_GIT_REMOTE",
		"HOMEBREW_PIP_INDEX_URL",
	}

	for _, envVar := range envVars {
		urls, ok := regionSources[strings.ToLower(envVar)]
		if !ok || len(urls) == 0 {
			return "", fmt.Errorf("%s not found for region: %s", envVar, region)
		}

		if err := shellManager.SetEnv(envVar, urls[0]); err != nil {
			return "", fmt.Errorf("error setting %s: %w", envVar, err)
		}
	}

	cmd := exec.Command("brew", "update")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("error running brew update: %w\n%s", err, string(output))
	}

	return "Homebrew registry set successfully", nil
}

func (h HomebrewRegistryManager) IsExists() bool {
	err := exec.Command("brew", "config").Run()
	return err == nil
}

func init() {
	alias.RegisterAlias("homebrew", []string{"brew"})

	// Register other aliases here
	source.RegisterManager([]string{"homebrew", "brew"}, HomebrewRegistryManager{})
}
