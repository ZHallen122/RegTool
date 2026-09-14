package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFlagsDefaults(t *testing.T) {
	// Not parallel: the env fallbacks are process state.
	clearHubEnv(t)

	cfg, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	want := config{
		addr:          ":8080",
		sourcesPath:   "",
		dbPath:        "hub.db",
		checkInterval: 5 * time.Minute,
		checkTimeout:  5 * time.Second,
		retention:     7 * 24 * time.Hour,
		logLevel:      "info",
	}
	if cfg != want {
		t.Errorf("parseFlags(nil) = %+v, want %+v", cfg, want)
	}
}

func TestParseFlagsOverrides(t *testing.T) {
	clearHubEnv(t)

	cfg, err := parseFlags([]string{
		"--addr", "127.0.0.1:9000",
		"--sources", "/etc/sources.json",
		"--db", "/data/hub.db",
		"--check-interval", "30s",
		"--check-timeout", "1s",
		"--retention", "2d",
		"--log-level", "debug",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	if cfg.addr != "127.0.0.1:9000" || cfg.sourcesPath != "/etc/sources.json" || cfg.dbPath != "/data/hub.db" {
		t.Errorf("paths and address parsed as %+v", cfg)
	}
	if cfg.checkInterval != 30*time.Second || cfg.checkTimeout != time.Second {
		t.Errorf("durations parsed as %s / %s", cfg.checkInterval, cfg.checkTimeout)
	}
	// "2d" is the whole reason the duration flag is not a plain
	// flag.DurationVar: time.ParseDuration has no day unit.
	if cfg.retention != 48*time.Hour {
		t.Errorf("--retention 2d = %s, want 48h", cfg.retention)
	}
	if cfg.logLevel != "debug" {
		t.Errorf("--log-level = %q", cfg.logLevel)
	}
}

func TestParseFlagsEnvFallback(t *testing.T) {
	clearHubEnv(t)
	t.Setenv("REGTOOL_HUB_ADDR", ":9999")
	t.Setenv("REGTOOL_HUB_DB", "/var/hub.db")
	t.Setenv("REGTOOL_HUB_RETENTION", "1d")
	t.Setenv("REGTOOL_HUB_LOG_LEVEL", "warn")

	cfg, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.addr != ":9999" || cfg.dbPath != "/var/hub.db" || cfg.retention != 24*time.Hour || cfg.logLevel != "warn" {
		t.Errorf("the environment was not picked up: %+v", cfg)
	}

	// An explicit flag still beats the environment.
	cfg, err = parseFlags([]string{"--addr", ":1234"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.addr != ":1234" {
		t.Errorf("--addr = %q, want the flag to win over the environment", cfg.addr)
	}
}

func TestParseFlagsRejectsBadInput(t *testing.T) {
	clearHubEnv(t)

	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--nope"}},
		{"stray argument", []string{"serve"}},
		{"bad duration", []string{"--check-interval", "soon"}},
		{"zero interval", []string{"--check-interval", "0s"}},
		{"negative timeout", []string{"--check-timeout", "-1s"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseFlags(test.args, io.Discard); err == nil {
				t.Errorf("parseFlags(%v) returned no error", test.args)
			}
		})
	}
}

func TestParseFlagsRejectsBadEnvDuration(t *testing.T) {
	clearHubEnv(t)
	t.Setenv("REGTOOL_HUB_CHECK_INTERVAL", "whenever")

	if _, err := parseFlags(nil, io.Discard); err == nil {
		t.Error("an unparseable REGTOOL_HUB_CHECK_INTERVAL was accepted")
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want time.Duration
		bad  bool
	}{
		{raw: "5m", want: 5 * time.Minute},
		{raw: "1500ms", want: 1500 * time.Millisecond},
		{raw: "7d", want: 7 * 24 * time.Hour},
		{raw: "0.5d", want: 12 * time.Hour},
		{raw: " 2d ", want: 48 * time.Hour},
		{raw: "d", bad: true},
		{raw: "", bad: true},
		{raw: "seven days", bad: true},
	}
	for _, test := range tests {
		got, err := parseDuration(test.raw)
		if test.bad {
			if err == nil {
				t.Errorf("parseDuration(%q) = %s, want an error", test.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q): %v", test.raw, err)
			continue
		}
		if got != test.want {
			t.Errorf("parseDuration(%q) = %s, want %s", test.raw, got, test.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"debug", "info", "warn", "error", "INFO"} {
		if _, err := parseLevel(name); err != nil {
			t.Errorf("parseLevel(%q): %v", name, err)
		}
	}
	if _, err := parseLevel("chatty"); err == nil {
		t.Error("parseLevel accepted an unknown level")
	}
}

func TestLoadSourcesEmbedded(t *testing.T) {
	t.Parallel()

	body, sources, err := loadSources("")
	if err != nil {
		t.Fatalf("loadSources(\"\"): %v", err)
	}
	if sources == nil || len(*sources) == 0 {
		t.Fatal("the embedded sources parsed as empty")
	}

	// The body must round trip as a sources.json, because it is what the CLI
	// will be unmarshalling on the other end.
	var decoded map[string]map[string][]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the served body is not a sources.json: %v", err)
	}
	if got := decoded["cn"]["npm"]; len(got) == 0 {
		t.Errorf("cn.npm is missing from the served body: %v", decoded["cn"])
	}

	// Every mirror in it is something the checker can probe.
	if len(hubTargets(t, body)) == 0 {
		t.Error("the embedded sources produced no check targets")
	}
}

func TestLoadSourcesFromAFile(t *testing.T) {
	t.Parallel()

	raw := `{"cn": {"npm": ["https://registry.npmmirror.com"]}}`
	path := filepath.Join(t.TempDir(), "sources.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	body, sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	// A file is served byte for byte, so whoever maintains it controls exactly
	// what the CLI sees.
	if string(body) != raw {
		t.Errorf("body = %q, want the file verbatim", body)
	}
	if got := (*sources)["cn"]["npm"]; len(got) != 1 || got[0] != "https://registry.npmmirror.com" {
		t.Errorf("parsed cn.npm as %v", got)
	}
}

func TestLoadSourcesRejectsBadFiles(t *testing.T) {
	t.Parallel()

	if _, _, err := loadSources(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing sources file was accepted")
	}

	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := loadSources(path); err == nil {
		t.Error("an unparseable sources file was accepted")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("the error %q does not name the file", err)
	}
}

// hubTargets is a small stand-in for the wiring in run: it checks that what
// loadSources returns is usable by the checker.
func hubTargets(t *testing.T, body []byte) []string {
	t.Helper()

	var decoded map[string]map[string][]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var urls []string
	for _, apps := range decoded {
		for _, mirrors := range apps {
			urls = append(urls, mirrors...)
		}
	}
	return urls
}

// clearHubEnv unsets every REGTOOL_HUB_* variable for the duration of a test,
// so a developer's own environment cannot change the answer.
func clearHubEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		"REGTOOL_HUB_ADDR",
		"REGTOOL_HUB_SOURCES",
		"REGTOOL_HUB_DB",
		"REGTOOL_HUB_CHECK_INTERVAL",
		"REGTOOL_HUB_CHECK_TIMEOUT",
		"REGTOOL_HUB_RETENTION",
		"REGTOOL_HUB_LOG_LEVEL",
	} {
		t.Setenv(name, "")
	}
}
