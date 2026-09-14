package backend

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// cargoBackend edits cargo's user-level config.toml.
//
// Cargo has no single "registry" key: a mirror is expressed as a source
// replacement, so this backend writes
//
//	[source.crates-io]
//	replace-with = "mirror"
//
//	[source.mirror]
//	registry = "<target>"
//
// A target that already carries the "sparse+https://" scheme is written
// verbatim, which is how cargo's sparse protocol is selected.
//
// TOML cannot be edited reliably line by line, so the file is round-tripped
// through a TOML library. Every other table and key keeps its value, but
// comments are lost and keys come back in sorted order.
type cargoBackend struct{ env Env }

// cargoMirrorName is the name of the replacement source this backend manages.
const cargoMirrorName = "mirror"

// NewCargo returns the cargo backend. It edits $CARGO_HOME/config.toml,
// defaulting to ~/.cargo/config.toml, and falls back to the extension-less
// legacy ~/.cargo/config when only that file exists.
func NewCargo(env Env) Backend { return &cargoBackend{env: env} }

func (b *cargoBackend) Name() string { return "cargo" }

// cargoHome is $CARGO_HOME or ~/.cargo.
func (b *cargoBackend) cargoHome() string {
	if p := b.env.getenv("CARGO_HOME"); p != "" {
		return p
	}
	return filepath.Join(b.env.Home, ".cargo")
}

func (b *cargoBackend) ConfigPath() string {
	home := b.cargoHome()
	modern := filepath.Join(home, "config.toml")
	if !exists(modern) {
		if legacy := filepath.Join(home, "config"); exists(legacy) {
			return legacy
		}
	}
	return modern
}

func (b *cargoBackend) Detect() (bool, error) {
	return exists(b.ConfigPath()) || exists(b.cargoHome()), nil
}

// cargoDoc parses data into a nested map, returning an empty document for a
// missing or empty file.
func cargoDoc(data []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// cargoTable returns the nested table at key, creating it when create is set.
func cargoTable(parent map[string]any, key string, create bool) map[string]any {
	if t, ok := parent[key].(map[string]any); ok {
		return t
	}
	if !create {
		return nil
	}
	t := map[string]any{}
	parent[key] = t
	return t
}

// cargoCurrentRegistry follows source.crates-io.replace-with to the replacement
// source and returns its registry URL.
func cargoCurrentRegistry(doc map[string]any) string {
	src := cargoTable(doc, "source", false)
	if src == nil {
		return ""
	}
	name, _ := cargoTable(src, "crates-io", false)["replace-with"].(string)
	if name == "" {
		return ""
	}
	repl := cargoTable(src, name, false)
	if repl == nil {
		return ""
	}
	registry, _ := repl["registry"].(string)
	return registry
}

// cargoSetRegistry points crates-io at the managed mirror source.
func cargoSetRegistry(doc map[string]any, target string) {
	src := cargoTable(doc, "source", true)
	cargoTable(src, "crates-io", true)["replace-with"] = cargoMirrorName
	cargoTable(src, cargoMirrorName, true)["registry"] = target
}

func (b *cargoBackend) Current() (string, error) {
	path := b.ConfigPath()
	data, _, err := readConfig(path)
	if err != nil {
		return "", err
	}
	doc, err := cargoDoc(data)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return cargoCurrentRegistry(doc), nil
}

func (b *cargoBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	doc, err := cargoDoc(before)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	from := cargoCurrentRegistry(doc)
	cargoSetRegistry(doc, target)
	after, err := toml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
