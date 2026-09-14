package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGemDetectFindsGemDirectory(t *testing.T) {
	home := t.TempDir()
	b := NewGem(Env{Home: home})
	if ok, _ := b.Detect(); ok {
		t.Fatal("Detect = true on an empty home")
	}
	if err := os.MkdirAll(filepath.Join(home, ".gem"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Detect(); err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil once ~/.gem exists", ok, err)
	}
}

func TestGemSourcesHandling(t *testing.T) {
	tests := []struct {
		name      string
		before    string
		wantOld   string
		wantKeep  []string
		wantFirst string
	}{
		{
			name:      "missing file",
			wantFirst: "---",
		},
		{
			name:      "empty file",
			before:    "\n",
			wantFirst: "---",
		},
		{
			name:      "document marker only",
			before:    "---\n",
			wantFirst: "---",
		},
		{
			name:      "no document marker is not added",
			before:    ":update_sources: true\n",
			wantKeep:  []string{":update_sources: true"},
			wantFirst: ":update_sources: true",
		},
		{
			name:      "existing sources list is replaced",
			before:    "---\n:sources:\n- https://old.example/\n- https://second.example/\n:verbose: true\n",
			wantOld:   "https://old.example/",
			wantKeep:  []string{":verbose: true"},
			wantFirst: "---",
		},
		{
			name:      "sources given as a scalar",
			before:    "---\n:sources: https://old.example/\n",
			wantOld:   "https://old.example/",
			wantFirst: "---",
		},
		{
			name:     "other keys keep their order",
			before:   "---\n:backtrace: true\n:sources:\n- https://old.example/\n:bulk_threshold: 1000\n",
			wantOld:  "https://old.example/",
			wantKeep: []string{":backtrace: true", ":bulk_threshold: 1000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewGem(Env{Home: t.TempDir()})
			if tt.before != "" {
				seed(t, b, tt.before)
			}
			if got, _ := b.Current(); got != tt.wantOld {
				t.Errorf("Current = %q; want %q", got, tt.wantOld)
			}
			plan, err := b.Plan(target)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			after := string(plan.After)
			if !strings.Contains(after, target) {
				t.Errorf("After does not mention the target:\n%s", after)
			}
			if strings.Contains(after, "old.example") || strings.Contains(after, "second.example") {
				t.Errorf("After still lists the old sources:\n%s", after)
			}
			for _, keep := range tt.wantKeep {
				if !strings.Contains(after, keep) {
					t.Errorf("After dropped %q:\n%s", keep, after)
				}
			}
			if tt.wantFirst != "" {
				if first, _, _ := strings.Cut(after, "\n"); first != tt.wantFirst {
					t.Errorf("first line = %q; want %q", first, tt.wantFirst)
				}
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got, _ := b.Current(); got != target {
				t.Errorf("Current after apply = %q; want %q", got, target)
			}
		})
	}
}

func TestGemRejectsNonMappingAndBrokenYAML(t *testing.T) {
	tests := []struct {
		name   string
		before string
	}{
		{name: "top level sequence", before: "---\n- one\n- two\n"},
		{name: "top level scalar", before: "---\njust a string\n"},
		{name: "not yaml at all", before: "\t- : : [\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewGem(Env{Home: t.TempDir()})
			seed(t, b, tt.before)
			if _, err := b.Plan(target); err == nil {
				t.Error("Plan succeeded; want an error")
			}
			if _, err := b.Current(); err == nil {
				t.Error("Current succeeded; want an error")
			}
		})
	}
}
