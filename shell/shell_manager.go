// Package shell reads and writes `export KEY="value"` lines in the user's
// shell configuration file. Homebrew is configured through environment
// variables rather than a configuration file of its own, so it is the only
// backend that needs this.
package shell

import (
	"fmt"
	"os"
	"strings"
)

// ShellManager reads and writes environment variables in one shell's
// configuration file.
type ShellManager interface {
	// SetEnv exports key with the given value, replacing an existing export.
	SetEnv(key, value string) error
	// GetEnv returns the value exported for key.
	GetEnv(key string) (string, error)
	// Path is the configuration file the manager edits. It is empty when the
	// path cannot be resolved.
	Path() string
}

// ShellFactory is a function type that returns a new ShellManager.
type ShellFactory func() ShellManager

// shellFactories holds a map of shell names to their corresponding factory functions.
var shellFactories = map[string]ShellFactory{}

// RegisterShell registers a new shell factory.
func RegisterShell(name string, factory ShellFactory) {
	shellFactories[name] = factory
}

// NewShellManager returns a manager for the shell named by $SHELL.
func NewShellManager() (ShellManager, error) {
	shell := os.Getenv("SHELL")
	for name, factory := range shellFactories {
		if strings.Contains(shell, name) {
			return factory(), nil
		}
	}
	return nil, fmt.Errorf("unsupported shell: %q", shell)
}
