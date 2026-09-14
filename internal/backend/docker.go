package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// dockerBackend edits the Docker daemon's daemon.json.
//
// Docker has no registry setting as such: pulls always go to Docker Hub, and a
// mirror is inserted in front of it with the registry-mirrors array. This
// backend manages that array. [dockerBackend.Current] reports the first mirror,
// or "" when there is none, which is what talking to Hub directly looks like;
// [dockerBackend.Plan] replaces the array with the one target, except for the
// canonical Hub URL itself, which removes the key altogether.
//
// JSON has no comments, so the file is decoded into a map and re-encoded with
// two-space indentation and sorted keys. Every other key keeps its value, but
// the key order and the original formatting are not preserved.
//
// The daemon only reads this file at start-up, so a change takes effect after
// `systemctl restart docker` or a restart of Docker Desktop.
type dockerBackend struct{ env Env }

// DockerHubURL is the canonical Docker Hub registry. Planning towards it means
// "no mirror", so it removes the registry-mirrors key instead of writing it.
const DockerHubURL = "https://registry-1.docker.io"

// DockerDaemonJSONEnvVar overrides the path of daemon.json. The system-wide
// file lives under /etc on Linux, which a test must never touch and a rootless
// installation does not use at all.
const DockerDaemonJSONEnvVar = "REGTOOL_DOCKER_DAEMON_JSON"

// dockerMirrorsKey is the daemon.json key holding the registry mirrors.
const dockerMirrorsKey = "registry-mirrors"

// NewDocker returns the docker backend. It honours $REGTOOL_DOCKER_DAEMON_JSON
// and otherwise edits /etc/docker/daemon.json on Linux, which usually needs
// root, and ~/.docker/daemon.json on macOS and Windows, where Docker Desktop
// keeps the daemon's configuration in the user's own directory.
func NewDocker(env Env) Backend { return &dockerBackend{env: env} }

func (b *dockerBackend) Name() string { return "docker" }

func (b *dockerBackend) ConfigPath() string {
	if p := b.env.getenv(DockerDaemonJSONEnvVar); p != "" {
		return p
	}
	switch b.env.goosName() {
	case "darwin", "windows":
		return filepath.Join(b.env.Home, ".docker", "daemon.json")
	default:
		return filepath.Join("/etc", "docker", "daemon.json")
	}
}

func (b *dockerBackend) Detect() (bool, error) {
	return exists(b.ConfigPath()) || exists(filepath.Join(b.env.Home, ".docker")), nil
}

// dockerDoc parses data into the top-level JSON object, returning an empty
// object for a missing or empty file. Numbers are kept as [json.Number] so that
// re-encoding cannot turn a large integer into floating point notation.
func dockerDoc(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("expected a single JSON object, got trailing content")
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object, got %s", jsonKind(raw))
	}
	return doc, nil
}

// jsonKind names the JSON type of a decoded value for an error message.
func jsonKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case json.Number, float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	default:
		return "something else"
	}
}

// dockerCurrentMirror returns the first configured registry mirror.
func dockerCurrentMirror(doc map[string]any) string {
	list, ok := doc[dockerMirrorsKey].([]any)
	if !ok || len(list) == 0 {
		return ""
	}
	first, _ := list[0].(string)
	return first
}

// dockerIsHub reports whether target is Docker Hub itself rather than a mirror
// in front of it.
func dockerIsHub(target string) bool {
	return strings.TrimRight(target, "/") == DockerHubURL
}

// dockerSetMirror rewrites registry-mirrors, or removes it when the target is
// Hub itself.
func dockerSetMirror(doc map[string]any, target string) {
	if dockerIsHub(target) {
		delete(doc, dockerMirrorsKey)
		return
	}
	doc[dockerMirrorsKey] = []any{target}
}

// dockerEncode serialises the object back to daemon.json bytes: two-space
// indentation, keys sorted by encoding/json and a trailing newline.
func dockerEncode(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *dockerBackend) Current() (string, error) {
	path := b.ConfigPath()
	data, _, err := readConfig(path)
	if err != nil {
		return "", err
	}
	doc, err := dockerDoc(data)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return dockerCurrentMirror(doc), nil
}

func (b *dockerBackend) Plan(target string) (*Plan, error) {
	target = strings.TrimSpace(target)
	path := b.ConfigPath()
	before, existed, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	doc, err := dockerDoc(before)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	from := dockerCurrentMirror(doc)
	dockerSetMirror(doc, target)
	after, err := dockerEncode(doc)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	return newPlan(b.Name(), path, before, existed, after, from, target), nil
}
