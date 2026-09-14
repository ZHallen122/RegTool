package backend

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// The helpers in this file are shared by the backends whose configuration file
// is YAML with a structure too rich to edit line by line: gem's :sources: list
// and helm's repositories list. They work on the document tree rather than on
// text, so every key the backend does not touch keeps its value and its
// position. Reserialising is not byte-exact: quoting, indentation and comments
// on the rewritten parts may be rewritten or lost.

// yamlMappingRoot parses data into its top-level mapping node, returning an
// empty mapping for a missing, empty or explicitly null document. A document
// whose root is anything but a mapping is an error, because the backends have
// nowhere to put a key in it.
func yamlMappingRoot(data []byte) (*yaml.Node, error) {
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

// yamlScalar builds a plain string scalar node.
func yamlScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// yamlValueIndex returns the index of the value node stored under key in a
// mapping node, or -1 when the key is absent.
func yamlValueIndex(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

// yamlValue returns the value node stored under key in a mapping node, or nil
// when the key is absent.
func yamlValue(mapping *yaml.Node, key string) *yaml.Node {
	if i := yamlValueIndex(mapping, key); i >= 0 {
		return mapping.Content[i]
	}
	return nil
}

// yamlEncode serialises a node back to bytes with two-space indentation.
func yamlEncode(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
