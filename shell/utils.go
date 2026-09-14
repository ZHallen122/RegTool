package shell

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// rcPerm is the mode a shell rc file is created with.
const rcPerm fs.FileMode = 0o644

// RCPath returns the absolute path of a shell configuration file that lives in
// the user's home directory, for example ".zshrc".
func RCPath(filename string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to locate the home directory: %w", err)
	}
	return filepath.Join(home, filename), nil
}

// SetEnvVarToFile writes an `export KEY="value"` line to the given shell
// configuration file, replacing the existing export of the same key when there
// is one. A missing file is created.
func SetEnvVarToFile(filename, key, value string) error {
	path, err := RCPath(filename)
	if err != nil {
		return err
	}

	input, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	exportLine := fmt.Sprintf("export %s=%q", key, value)
	prefix := fmt.Sprintf("export %s=", key)

	var (
		lines   []string
		found   bool
		trimmed = strings.TrimRight(string(input), "\n")
	)
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = exportLine
			found = true
		}
	}
	if !found {
		lines = append(lines, exportLine)
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), rcPerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// GetEnvVarFromFile reads the value exported for key from the given shell
// configuration file.
func GetEnvVarFromFile(filename, key string) (string, error) {
	path, err := RCPath(filename)
	if err != nil {
		return "", err
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	prefix := fmt.Sprintf("export %s=", key)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.Trim(strings.TrimPrefix(line, prefix), `"`), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	return "", fmt.Errorf("environment variable %s is not set in %s", key, path)
}
