package backend

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// helmBackend edits helm's repositories.yaml.
//
// Helm is the odd one out: it has no single registry, it has a list of named
// chart repositories. There is nothing to point "at a mirror" in general, so
// this backend manages exactly one entry of that list, the conventional
// "stable" repository. [helmBackend.Current] reports the URL of that entry and
// [helmBackend.Plan] rewrites it, adding the entry when it is missing.
//
// Everything else in the file survives: the other repositories, their caFile,
// username, password and TLS fields, and the top-level apiVersion and generated
// keys. The file is round-tripped through a YAML parser, so comments are
// dropped and quoting may be rewritten.
type helmBackend struct{ env Env }

// The keys this backend reads and writes in repositories.yaml.
const (
	// helmStableRepo is the name of the repository entry the backend manages.
	helmStableRepo = "stable"
	// helmRepositoriesKey is the top-level list of repository entries.
	helmRepositoriesKey = "repositories"
	// helmNameKey and helmURLKey are the fields of one entry that matter here.
	helmNameKey = "name"
	helmURLKey  = "url"
	// helmAPIVersionKey is written into a file this backend creates, because
	// that is what helm itself puts at the top of a fresh repositories.yaml.
	helmAPIVersionKey = "apiVersion"
	helmAPIVersion    = "v1"
)

// NewHelm returns the helm backend. It honours $HELM_REPOSITORY_CONFIG and
// otherwise looks for repositories.yaml in helm's configuration home:
// $HELM_CONFIG_HOME, else $XDG_CONFIG_HOME/helm, else ~/.config/helm on Linux,
// ~/Library/Preferences/helm on macOS and %APPDATA%\helm on Windows.
func NewHelm(env Env) Backend { return &helmBackend{env: env} }

func (b *helmBackend) Name() string { return "helm" }

// configHome is the directory helm keeps its own files in.
func (b *helmBackend) configHome() string {
	if p := b.env.getenv("HELM_CONFIG_HOME"); p != "" {
		return p
	}
	if xdg := b.env.getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "helm")
	}
	switch b.env.goosName() {
	case "windows":
		base := b.env.getenv("APPDATA")
		if base == "" {
			base = b.env.configDir()
		}
		return filepath.Join(base, "helm")
	case "darwin":
		return filepath.Join(b.env.Home, "Library", "Preferences", "helm")
	default:
		return filepath.Join(b.env.Home, ".config", "helm")
	}
}

func (b *helmBackend) ConfigPath() string {
	if p := b.env.getenv("HELM_REPOSITORY_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(b.configHome(), "repositories.yaml")
}

func (b *helmBackend) Detect() (bool, error) {
	return exists(b.ConfigPath()) || exists(b.configHome()), nil
}

// helmRepositories returns the repositories sequence node. It is nil when the
// key is absent or explicitly null, which both mean "no repositories yet"; a
// key holding anything other than a list is an error, because overwriting it
// would throw away a file this backend does not understand.
func helmRepositories(root *yaml.Node) (*yaml.Node, error) {
	value := yamlValue(root, helmRepositoriesKey)
	switch {
	case value == nil, value.Tag == "!!null":
		return nil, nil
	case value.Kind == yaml.SequenceNode:
		return value, nil
	default:
		return nil, fmt.Errorf("%s is not a list", helmRepositoriesKey)
	}
}

// helmEntry returns the repository entry named name, or nil when the list has
// no such entry.
func helmEntry(repos *yaml.Node, name string) *yaml.Node {
	if repos == nil {
		return nil
	}
	for _, entry := range repos.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		if n := yamlValue(entry, helmNameKey); n != nil && n.Value == name {
			return entry
		}
	}
	return nil
}

// helmCurrentURL returns the URL of the managed repository entry.
func helmCurrentURL(root *yaml.Node) (string, error) {
	repos, err := helmRepositories(root)
	if err != nil {
		return "", err
	}
	entry := helmEntry(repos, helmStableRepo)
	if entry == nil {
		return "", nil
	}
	url := yamlValue(entry, helmURLKey)
	if url == nil || url.Kind != yaml.ScalarNode {
		return "", nil
	}
	return url.Value, nil
}

// helmSetURL points the managed repository entry at target, creating the entry
// and the repositories list when they are missing.
func helmSetURL(root *yaml.Node, target string) error {
	repos, err := helmRepositories(root)
	if err != nil {
		return err
	}
	if repos == nil {
		repos = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if i := yamlValueIndex(root, helmRepositoriesKey); i >= 0 {
			root.Content[i] = repos
		} else {
			if len(root.Content) == 0 {
				// A file this backend creates gets the header helm writes.
				root.Content = append(root.Content,
					yamlScalar(helmAPIVersionKey), yamlScalar(helmAPIVersion))
			}
			root.Content = append(root.Content, yamlScalar(helmRepositoriesKey), repos)
		}
	}

	entry := helmEntry(repos, helmStableRepo)
	if entry == nil {
		repos.Content = append(repos.Content, &yaml.Node{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
			Content: []*yaml.Node{
				yamlScalar(helmNameKey), yamlScalar(helmStableRepo),
				yamlScalar(helmURLKey), yamlScalar(target),
			},
		})
		return nil
	}

	// Keep the node itself so its comment and quoting style survive; only the
	// value changes.
	if url := yamlValue(entry, helmURLKey); url != nil {
		url.Kind, url.Tag, url.Value = yaml.ScalarNode, "!!str", target
		url.Content = nil
		return nil
	}
	entry.Content = append(entry.Content, yamlScalar(helmURLKey), yamlScalar(target))
	return nil
}

func (b *helmBackend) Current() (string, error) {
	path := b.ConfigPath()
	data, _, err := readConfig(path)
	if err != nil {
		return "", err
	}
	root, err := yamlMappingRoot(data)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	current, err := helmCurrentURL(root)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return current, nil
}

func (b *helmBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	root, err := yamlMappingRoot(before)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	from, err := helmCurrentURL(root)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := helmSetURL(root, target); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	after, err := yamlEncode(root)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
