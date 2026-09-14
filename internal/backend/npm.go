package backend

import (
	"path/filepath"
	"strings"
)

// npmBackend edits the user-level .npmrc.
//
// The file is an ini-like list of key=value lines. Only the line holding the
// bare "registry" key is rewritten; comments, blank lines, auth tokens and
// scoped keys such as "@acme:registry" are preserved byte for byte.
type npmBackend struct{ env Env }

// NewNPM returns the npm backend. It edits $NPM_CONFIG_USERCONFIG when that
// variable is set, and ~/.npmrc otherwise.
func NewNPM(env Env) Backend { return &npmBackend{env: env} }

func (b *npmBackend) Name() string { return "npm" }

func (b *npmBackend) ConfigPath() string {
	if p := b.env.getenv("NPM_CONFIG_USERCONFIG"); p != "" {
		return p
	}
	return filepath.Join(b.env.Home, ".npmrc")
}

func (b *npmBackend) Detect() (bool, error) {
	// Either the config file or npm's cache directory is enough of a marker.
	return exists(b.ConfigPath()) || exists(filepath.Join(b.env.Home, ".npm")), nil
}

// npmRegistryValue reports whether text sets the bare "registry" key.
func npmRegistryValue(text string) (string, bool) {
	if isComment(text) {
		return "", false
	}
	key, value, ok := splitKeyValue(text, "=")
	if !ok || key != "registry" {
		return "", false
	}
	return unquote(value), true
}

func (b *npmBackend) Current() (string, error) {
	data, _, err := readConfig(b.ConfigPath())
	if err != nil {
		return "", err
	}
	return lastValue(splitPhysical(data), npmRegistryValue), nil
}

func (b *npmBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	lines := splitPhysical(before)
	from := lastValue(lines, npmRegistryValue)

	match := func(text string) bool { _, ok := npmRegistryValue(text); return ok }
	after := joinPhysical(replaceOrAppend(lines, match, "registry="+target))

	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
