package backend

import (
	"path/filepath"
	"strings"
)

// goBackend edits the Go environment file that `go env -w` writes, without
// running the go command.
//
// The file is a plain list of KEY=value lines and is edited line by line, so
// every other variable (GOFLAGS, GOSUMDB, GOPRIVATE …) survives byte for byte.
// The written value is "<target>,direct" so that modules missing from the
// mirror still resolve; Current strips that ",direct" suffix again so callers
// display the mirror URL alone.
type goBackend struct{ env Env }

// NewGo returns the Go module proxy backend. It edits $GOENV when that variable
// is set to a path, and <os.UserConfigDir>/go/env otherwise.
func NewGo(env Env) Backend { return &goBackend{env: env} }

func (b *goBackend) Name() string { return "go" }

func (b *goBackend) ConfigPath() string {
	// GOENV=off disables the file entirely; the default location is still the
	// honest answer to "which file would be edited".
	if p := b.env.getenv("GOENV"); p != "" && p != "off" {
		return p
	}
	return filepath.Join(b.env.configDir(), "go", "env")
}

func (b *goBackend) Detect() (bool, error) {
	if exists(b.ConfigPath()) {
		return true, nil
	}
	// A GOPATH directory is the other cheap marker of a Go installation.
	return exists(filepath.Join(b.env.configDir(), "go")) || exists(filepath.Join(b.env.Home, "go")), nil
}

// goProxyRaw reports whether text sets GOPROXY and returns the raw value.
func goProxyRaw(text string) (string, bool) {
	if isComment(text) {
		return "", false
	}
	key, value, ok := splitKeyValue(text, "=")
	if !ok || key != "GOPROXY" {
		return "", false
	}
	return value, true
}

// goProxyDisplay strips the ",direct" fallback from a GOPROXY value so that the
// mirror URL can be shown on its own.
func goProxyDisplay(raw string) string {
	return strings.TrimSuffix(raw, ",direct")
}

// goProxyWritten builds the value stored in the env file. A target that already
// spells out a fallback list, or that is one of the magic values, is kept as is.
func goProxyWritten(target string) string {
	switch {
	case target == "", target == "direct", target == "off":
		return target
	case strings.Contains(target, ","):
		return target
	default:
		return target + ",direct"
	}
}

func (b *goBackend) Current() (string, error) {
	data, _, err := readConfig(b.ConfigPath())
	if err != nil {
		return "", err
	}
	return goProxyDisplay(lastValue(splitPhysical(data), goProxyRaw)), nil
}

func (b *goBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	lines := splitPhysical(before)
	from := goProxyDisplay(lastValue(lines, goProxyRaw))

	match := func(text string) bool { _, ok := goProxyRaw(text); return ok }
	after := joinPhysical(replaceOrAppend(lines, match, "GOPROXY="+goProxyWritten(target)))

	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
