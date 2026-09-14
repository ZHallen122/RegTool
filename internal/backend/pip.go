package backend

import (
	"path/filepath"
	"slices"
	"strings"
)

// pipBackend edits pip's per-user configuration file.
//
// The file is an ini file and the registry is index-url in the [global]
// section. Editing is line based: other sections such as [install], other keys
// such as trusted-host, and all comments are preserved byte for byte. When
// [global] is missing it is appended; when it exists but has no index-url the
// key is inserted directly under the section header.
type pipBackend struct{ env Env }

// NewPip returns the pip backend. It honours $PIP_CONFIG_FILE and otherwise
// uses the per-OS location: %APPDATA%\pip\pip.ini on Windows,
// ~/Library/Application Support/pip/pip.conf on macOS (unless the XDG-style
// ~/.config/pip/pip.conf already exists) and ~/.config/pip/pip.conf elsewhere.
func NewPip(env Env) Backend { return &pipBackend{env: env} }

func (b *pipBackend) Name() string { return "pip" }

func (b *pipBackend) ConfigPath() string {
	if p := b.env.getenv("PIP_CONFIG_FILE"); p != "" {
		return p
	}
	switch b.env.goosName() {
	case "windows":
		base := b.env.getenv("APPDATA")
		if base == "" {
			base = b.env.configDir()
		}
		return filepath.Join(base, "pip", "pip.ini")
	case "darwin":
		if xdg := filepath.Join(b.env.Home, ".config", "pip", "pip.conf"); exists(xdg) {
			return xdg
		}
		return filepath.Join(b.env.Home, "Library", "Application Support", "pip", "pip.conf")
	default:
		return filepath.Join(b.env.Home, ".config", "pip", "pip.conf")
	}
}

func (b *pipBackend) Detect() (bool, error) {
	path := b.ConfigPath()
	return exists(path) || exists(filepath.Dir(path)), nil
}

// iniSection reports whether text is a section header and returns its name.
func iniSection(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if len(t) >= 2 && strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// pipIndexURL reports whether text sets index-url. pip accepts both dashes and
// underscores in key names, so both spellings are recognised.
func pipIndexURL(text string) (string, bool) {
	if isComment(text) {
		return "", false
	}
	key, value, ok := splitKeyValue(text, "=:")
	if !ok {
		return "", false
	}
	if strings.ToLower(strings.ReplaceAll(key, "_", "-")) != "index-url" {
		return "", false
	}
	return unquote(value), true
}

// pipGlobalIndexURL returns the effective index-url of the [global] section.
func pipGlobalIndexURL(lines []physLine) string {
	value := ""
	inGlobal := false
	for _, l := range lines {
		if name, ok := iniSection(l.text); ok {
			inGlobal = name == "global"
			continue
		}
		if !inGlobal {
			continue
		}
		if v, ok := pipIndexURL(l.text); ok {
			value = v
		}
	}
	return value
}

// pipSetIndexURL rewrites the effective index-url of the [global] section.
func pipSetIndexURL(lines []physLine, target string) []physLine {
	newText := "index-url = " + target
	eol := defaultEOL(lines)

	sectionIdx := -1
	var matches []int
	inGlobal := false
	for i, l := range lines {
		if name, ok := iniSection(l.text); ok {
			inGlobal = name == "global"
			if inGlobal && sectionIdx < 0 {
				sectionIdx = i
			}
			continue
		}
		if inGlobal {
			if _, ok := pipIndexURL(l.text); ok {
				matches = append(matches, i)
			}
		}
	}

	switch {
	case len(matches) > 0:
		last := matches[len(matches)-1]
		out := make([]physLine, 0, len(lines))
		for i, l := range lines {
			switch {
			case i == last:
				l.text = newText
				if l.eol == "" {
					l.eol = eol
				}
				out = append(out, l)
			case slices.Contains(matches, i):
				// drop the shadowed duplicate
			default:
				out = append(out, l)
			}
		}
		return out

	case sectionIdx >= 0:
		out := make([]physLine, 0, len(lines)+1)
		out = append(out, lines[:sectionIdx+1]...)
		out[len(out)-1].eol = eol
		out = append(out, physLine{text: newText, eol: eol})
		return append(out, lines[sectionIdx+1:]...)

	default:
		out := make([]physLine, len(lines), len(lines)+3)
		copy(out, lines)
		if n := len(out); n > 0 {
			out[n-1].eol = eol
			if strings.TrimSpace(out[n-1].text) != "" {
				out = append(out, physLine{eol: eol})
			}
		}
		return append(out,
			physLine{text: "[global]", eol: eol},
			physLine{text: newText, eol: eol},
		)
	}
}

func (b *pipBackend) Current() (string, error) {
	data, _, err := readConfig(b.ConfigPath())
	if err != nil {
		return "", err
	}
	return pipGlobalIndexURL(splitPhysical(data)), nil
}

func (b *pipBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	lines := splitPhysical(before)
	from := pipGlobalIndexURL(lines)
	after := joinPhysical(pipSetIndexURL(lines, target))

	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
