package backend

import (
	"path/filepath"
	"strings"
)

// Yarn ships two incompatible configuration formats, so this package exposes
// two backends rather than guessing which one a machine uses:
//
//   - "yarn" edits ~/.yarnrc, the yarn 1 format, where the registry is a
//     `registry "https://…"` line.
//   - "yarn-berry" edits ~/.yarnrc.yml, the yarn 2+ format, where it is the
//     top-level YAML key `npmRegistryServer`.
//
// Each one reports Detect() only for its own file, so a caller that runs over
// All() configures exactly the yarn that is actually installed, and a caller
// that wants both can apply both plans.

// yarnBackend edits the yarn 1 ~/.yarnrc. The file is edited line by line, so
// every other setting and every comment survives untouched.
type yarnBackend struct{ env Env }

// NewYarn returns the yarn 1 backend, which edits ~/.yarnrc.
func NewYarn(env Env) Backend { return &yarnBackend{env: env} }

func (b *yarnBackend) Name() string { return "yarn" }

func (b *yarnBackend) ConfigPath() string { return filepath.Join(b.env.Home, ".yarnrc") }

func (b *yarnBackend) Detect() (bool, error) {
	return exists(b.ConfigPath()), nil
}

// yarnRegistryValue reports whether text is a yarn 1 `registry "url"` line.
func yarnRegistryValue(text string) (string, bool) {
	if isComment(text) {
		return "", false
	}
	trimmed := strings.TrimSpace(text)
	rest, ok := strings.CutPrefix(trimmed, "registry")
	if !ok || rest == "" {
		return "", false
	}
	// The key and the value are separated by whitespace, so "registry-foo" and
	// "registryX" must not match.
	if !strings.ContainsAny(rest[:1], " \t") {
		return "", false
	}
	return unquote(strings.TrimSpace(rest)), true
}

func (b *yarnBackend) Current() (string, error) {
	data, _, err := readConfig(b.ConfigPath())
	if err != nil {
		return "", err
	}
	return lastValue(splitPhysical(data), yarnRegistryValue), nil
}

func (b *yarnBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	lines := splitPhysical(before)
	from := lastValue(lines, yarnRegistryValue)

	match := func(text string) bool { _, ok := yarnRegistryValue(text); return ok }
	after := joinPhysical(replaceOrAppend(lines, match, `registry "`+target+`"`))

	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}

// yarnBerryBackend edits the yarn 2+ ~/.yarnrc.yml. The registry lives in the
// top-level scalar key npmRegistryServer, which is rewritten in place; every
// other key, nested block and comment is preserved byte for byte because the
// file is never reserialised.
type yarnBerryBackend struct{ env Env }

// NewYarnBerry returns the yarn 2+ backend, which edits ~/.yarnrc.yml.
func NewYarnBerry(env Env) Backend { return &yarnBerryBackend{env: env} }

func (b *yarnBerryBackend) Name() string { return "yarn-berry" }

func (b *yarnBerryBackend) ConfigPath() string { return filepath.Join(b.env.Home, ".yarnrc.yml") }

func (b *yarnBerryBackend) Detect() (bool, error) {
	return exists(b.ConfigPath()), nil
}

// berryRegistryValue reports whether text is the top-level npmRegistryServer
// key. Indented lines belong to a nested block and are ignored on purpose.
func berryRegistryValue(text string) (string, bool) {
	if isComment(text) || strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\t") {
		return "", false
	}
	key, value, ok := splitKeyValue(text, ":")
	if !ok || key != "npmRegistryServer" {
		return "", false
	}
	return unquote(value), true
}

func (b *yarnBerryBackend) Current() (string, error) {
	data, _, err := readConfig(b.ConfigPath())
	if err != nil {
		return "", err
	}
	return lastValue(splitPhysical(data), berryRegistryValue), nil
}

func (b *yarnBerryBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	lines := splitPhysical(before)
	from := lastValue(lines, berryRegistryValue)

	match := func(text string) bool { _, ok := berryRegistryValue(text); return ok }
	after := joinPhysical(replaceOrAppend(lines, match, `npmRegistryServer: "`+target+`"`))

	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
