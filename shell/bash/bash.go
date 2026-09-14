// Package bash edits ~/.bashrc.
package bash

import "github.com/ZHallen122/RegTool/shell"

// rcFile is the bash configuration file, relative to the home directory.
const rcFile = ".bashrc"

// Bash edits the bash configuration file.
type Bash struct{}

// SetEnv exports key in ~/.bashrc.
func (b Bash) SetEnv(key, value string) error {
	return shell.SetEnvVarToFile(rcFile, key, value)
}

// GetEnv reads the value exported for key from ~/.bashrc.
func (b Bash) GetEnv(key string) (string, error) {
	return shell.GetEnvVarFromFile(rcFile, key)
}

// Path is the absolute path of ~/.bashrc.
func (b Bash) Path() string {
	path, err := shell.RCPath(rcFile)
	if err != nil {
		return ""
	}
	return path
}

// init registers the Bash shell manager.
func init() {
	shell.RegisterShell("bash", func() shell.ShellManager {
		return Bash{}
	})
}
