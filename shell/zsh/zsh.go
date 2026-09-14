// Package zsh edits ~/.zshrc.
package zsh

import "github.com/ZHallen122/RegTool/shell"

// rcFile is the zsh configuration file, relative to the home directory.
const rcFile = ".zshrc"

// Zsh edits the zsh configuration file.
type Zsh struct{}

// SetEnv exports key in ~/.zshrc.
func (z Zsh) SetEnv(key, value string) error {
	return shell.SetEnvVarToFile(rcFile, key, value)
}

// GetEnv reads the value exported for key from ~/.zshrc.
func (z Zsh) GetEnv(key string) (string, error) {
	return shell.GetEnvVarFromFile(rcFile, key)
}

// Path is the absolute path of ~/.zshrc.
func (z Zsh) Path() string {
	path, err := shell.RCPath(rcFile)
	if err != nil {
		return ""
	}
	return path
}

// init registers the Zsh shell manager.
func init() {
	shell.RegisterShell("zsh", func() shell.ShellManager {
		return Zsh{}
	})
}
