package backend

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// gemBackend edits ~/.gemrc.
//
// Unlike the line-based backends this one round-trips the file through a YAML
// parser, because :sources: is a list and not a single line. Top-level keys
// such as :update_sources: or gem: keep their value and their order, and the
// :sources: list is replaced wholesale by a single entry, which is what
// pointing gem at one mirror means. Reserialising is not byte-exact: quoting,
// indentation and some comments may be rewritten or lost.
type gemBackend struct{ env Env }

// NewGem returns the RubyGems backend, which edits ~/.gemrc.
func NewGem(env Env) Backend { return &gemBackend{env: env} }

// gemSourcesKey is the Ruby symbol used as the sources key in .gemrc.
const gemSourcesKey = ":sources"

func (b *gemBackend) Name() string { return "gem" }

func (b *gemBackend) ConfigPath() string { return filepath.Join(b.env.Home, ".gemrc") }

func (b *gemBackend) Detect() (bool, error) {
	return exists(b.ConfigPath()) || exists(filepath.Join(b.env.Home, ".gem")), nil
}

// gemRoot parses data into the top-level mapping of a .gemrc, returning an
// empty mapping for a missing or empty file.
func gemRoot(data []byte) (*yaml.Node, error) {
	empty := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(bytes.TrimSpace(data)) == 0 {
		return empty, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return empty, nil
	}
	root := doc.Content[0]
	switch {
	case root.Kind == yaml.MappingNode:
		return root, nil
	case root.Tag == "!!null":
		return empty, nil
	default:
		return nil, fmt.Errorf("expected a YAML mapping, got %s", root.Tag)
	}
}

// gemSourcesNode returns the index of the :sources: value node in the mapping,
// or -1 when the key is absent.
func gemSourcesNode(root *yaml.Node) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == gemSourcesKey {
			return i + 1
		}
	}
	return -1
}

// gemCurrentSource returns the first entry of the :sources: list.
func gemCurrentSource(root *yaml.Node) string {
	i := gemSourcesNode(root)
	if i < 0 {
		return ""
	}
	value := root.Content[i]
	if value.Kind == yaml.SequenceNode && len(value.Content) > 0 {
		return value.Content[0].Value
	}
	if value.Kind == yaml.ScalarNode {
		return value.Value
	}
	return ""
}

// gemSetSource replaces the :sources: list with the single target.
func gemSetSource(root *yaml.Node, target string) {
	seq := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: target},
		},
	}
	if i := gemSourcesNode(root); i >= 0 {
		old := root.Content[i]
		seq.HeadComment, seq.LineComment, seq.FootComment = old.HeadComment, old.LineComment, old.FootComment
		root.Content[i] = seq
		return
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: gemSourcesKey},
		seq,
	)
}

// gemEncode serialises the mapping back to .gemrc bytes, keeping the leading
// document marker that gem itself writes.
func gemEncode(root *yaml.Node, before []byte) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	hadMarker := bytes.HasPrefix(bytes.TrimLeft(before, " \t\r\n"), []byte("---"))
	if !bytes.HasPrefix(out, []byte("---")) && (hadMarker || len(bytes.TrimSpace(before)) == 0) {
		out = append([]byte("---\n"), out...)
	}
	return out, nil
}

func (b *gemBackend) Current() (string, error) {
	data, _, err := readConfig(b.ConfigPath())
	if err != nil {
		return "", err
	}
	root, err := gemRoot(data)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", b.ConfigPath(), err)
	}
	return gemCurrentSource(root), nil
}

func (b *gemBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	root, err := gemRoot(before)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	from := gemCurrentSource(root)
	gemSetSource(root, target)
	after, err := gemEncode(root, before)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
