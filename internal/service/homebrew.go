package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ZHallen122/RegTool/shell"

	// Blank imports so that the bash and zsh managers register themselves.
	_ "github.com/ZHallen122/RegTool/shell/bash"
	_ "github.com/ZHallen122/RegTool/shell/zsh"

	"github.com/ZHallen122/RegTool/source/structs"
)

// homebrewName is the switcher name and the prefix of its keys in the sources
// file.
const homebrewName = "homebrew"

// homebrewBottleKey is the sources file key whose URL stands in for homebrew as
// a whole: homebrew has no single registry, but the bottle domain is the one
// users recognise.
const homebrewBottleKey = "homebrew_bottle_domain"

// homebrewEnvVars are the variables homebrew reads to find its mirrors. Each
// one is the upper-case spelling of its key in the sources file.
var homebrewEnvVars = []string{
	"HOMEBREW_API_DOMAIN",
	"HOMEBREW_BOTTLE_DOMAIN",
	"HOMEBREW_BREW_GIT_REMOTE",
	"HOMEBREW_CORE_GIT_REMOTE",
	"HOMEBREW_PIP_INDEX_URL",
}

// homebrewSwitcher points homebrew at a region's mirrors.
//
// Unlike every other switcher this one still shells out. Homebrew has no
// configuration file to rewrite: it is configured entirely through environment
// variables, which live in the user's shell rc file, and picking up a new set
// of mirrors means running `brew update` afterwards.
type homebrewSwitcher struct{}

func newHomebrewSwitcher() switcher { return homebrewSwitcher{} }

func (homebrewSwitcher) Name() string { return homebrewName }

// Detect reports whether brew is on the PATH.
func (homebrewSwitcher) Detect() (bool, error) {
	_, err := exec.LookPath("brew")
	return err == nil, nil
}

// Current returns the bottle domain homebrew is using, preferring what brew
// itself reports over what the environment says.
func (homebrewSwitcher) Current() (string, error) {
	output, err := exec.Command("brew", "config").Output()
	if err != nil {
		return "", fmt.Errorf("brew config failed: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "HOMEBREW_BOTTLE_DOMAIN" {
			continue
		}
		return strings.TrimSpace(value), nil
	}
	// brew only prints the variables that are set, so an absent line means the
	// default is in use; the environment is the next best answer.
	return os.Getenv("HOMEBREW_BOTTLE_DOMAIN"), nil
}

// Plan works out which exports would have to change in the shell rc file.
func (homebrewSwitcher) Plan(url string, regionSources structs.RegistryRegionSources) (change, error) {
	manager, err := shell.NewShellManager()
	if err != nil {
		return nil, fmt.Errorf("homebrew stores its mirrors in the shell configuration: %w", err)
	}

	result := &homebrewChange{manager: manager, path: manager.Path(), to: url}
	for _, name := range homebrewEnvVars {
		urls := regionSources[strings.ToLower(name)]
		if len(urls) == 0 {
			return nil, fmt.Errorf("the sources file has no %s entry for this region", strings.ToLower(name))
		}
		// A variable that is not exported yet simply reads as empty; that is
		// not a failure, it is the reason the change exists.
		old, _ := manager.GetEnv(name)
		result.vars = append(result.vars, homebrewVar{Key: name, Old: old, New: urls[0]})
		if name == "HOMEBREW_BOTTLE_DOMAIN" {
			result.from = old
		}
	}
	return result, nil
}

// homebrewVar is one environment variable and the value it would get.
type homebrewVar struct {
	Key string
	Old string
	New string
}

// homebrewChange is a set of exports to write to the shell rc file, followed by
// a `brew update` so homebrew picks the new mirrors up.
type homebrewChange struct {
	manager  shell.ShellManager
	path     string
	vars     []homebrewVar
	from, to string
}

func (c *homebrewChange) From() string { return c.from }
func (c *homebrewChange) To() string   { return c.to }

// Paths is the shell rc file, so that it is snapshotted like any other
// configuration file this tool edits.
func (c *homebrewChange) Paths() []string {
	if c.path == "" {
		return nil
	}
	return []string{c.path}
}

func (c *homebrewChange) IsNoop() bool {
	for _, v := range c.vars {
		if v.Old != v.New {
			return false
		}
	}
	return true
}

// Diff lists the exports that would change. It is not a diff of the rc file
// itself, because where in the file each export ends up is not decided until
// the change is applied.
func (c *homebrewChange) Diff() string {
	if c.IsNoop() {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", c.path, c.path)
	for _, v := range c.vars {
		if v.Old == v.New {
			continue
		}
		if v.Old != "" {
			fmt.Fprintf(&out, "-export %s=%q\n", v.Key, v.Old)
		}
		fmt.Fprintf(&out, "+export %s=%q\n", v.Key, v.New)
	}
	return out.String()
}

func (c *homebrewChange) Apply() error {
	for _, v := range c.vars {
		if v.Old == v.New {
			continue
		}
		if err := c.manager.SetEnv(v.Key, v.New); err != nil {
			return fmt.Errorf("failed to export %s: %w", v.Key, err)
		}
	}
	if output, err := exec.Command("brew", "update").CombinedOutput(); err != nil {
		return fmt.Errorf("brew update failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
