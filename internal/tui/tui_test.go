package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ZHallen122/RegTool/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

// key builds the key message a single keystroke produces.
func key(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// registerTestPages wires the pages up the way Run does, against a service with
// no backends at all.
func registerTestPages(t *testing.T) *service.Service {
	t.Helper()

	svc := service.New(nil, nil, nil)
	registerPages(svc)
	RegisterCommand(mainMenuName, "Main Menu", newMainMenuModel())
	return svc
}

func TestMainMenuListsEveryPage(t *testing.T) {
	registerTestPages(t)

	want := []string{
		"Change All to Region",
		"Change an App to Region",
		"List All Registry",
		"List Registry by App Name",
		"Init/Update All Apps Recording",
	}
	got := ListCommandDescriptions()
	if len(got) != len(want) {
		t.Fatalf("the main menu offers %v, want %v", got, want)
	}
	for i, description := range want {
		if got[i] != description {
			t.Errorf("menu entry %d is %q, want %q", i, got[i], description)
		}
	}
}

func TestMainMenuQuitsOnQ(t *testing.T) {
	registerTestPages(t)

	_, cmd := newMainMenuModel().Update(key("q"))
	if cmd == nil {
		t.Fatal("pressing q returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("pressing q produced %T, want tea.QuitMsg", cmd())
	}
}

func TestMainMenuOpensTheSelectedPage(t *testing.T) {
	registerTestPages(t)

	model, _ := newMainMenuModel().Update(key("enter"))
	if _, ok := model.(changeAllModel); !ok {
		t.Fatalf("enter opened %T, want the change-all page", model)
	}
}

func TestChangeAllPageWalksItsStages(t *testing.T) {
	svc := registerTestPages(t)

	var model tea.Model = newChangeAllModel(svc)
	if !strings.Contains(model.View(), "Select a region") {
		t.Errorf("the change-all page opens on %q, want the region picker", model.View())
	}

	model, _ = model.Update(key("j"))
	model, _ = model.Update(key("enter"))
	if view := model.View(); !strings.Contains(view, regions[1]) {
		t.Errorf("the confirmation step shows %q, want region %q", view, regions[1])
	}

	// Confirming hands off to the service; the page renders the outcome when
	// the message comes back.
	model, cmd := model.Update(key("enter"))
	if cmd == nil {
		t.Fatal("confirming did not start the change")
	}
	if !strings.Contains(model.View(), "Switching every installed app") {
		t.Errorf("the page shows %q while working, want a progress line", model.View())
	}

	model, _ = model.Update(cmd())
	if view := model.View(); !strings.Contains(view, "no supported package manager") {
		t.Errorf("the page shows %q, want the empty-machine summary", view)
	}
}

func TestRenderChange(t *testing.T) {
	tests := []struct {
		name   string
		result *service.UseResult
		err    error
		want   string
	}{
		{
			name:   "a validation failure has no result to show",
			result: nil,
			err:    errors.New("unknown region \"xx\""),
			want:   "unknown region",
		},
		{
			name: "a change reports the mirror and the snapshot",
			result: &service.UseResult{
				Changes:    []service.ChangeResult{{App: "npm", To: "https://registry.npmmirror.com"}},
				SnapshotID: "20260914T000000.000Z-abcdef",
			},
			want: "20260914T000000.000Z-abcdef",
		},
		{
			name: "a no-op says so",
			result: &service.UseResult{
				Changes: []service.ChangeResult{{App: "npm", To: "https://registry.npmmirror.com", Noop: true}},
			},
			want: "already on",
		},
		{
			name: "a failed app is reported without hiding the others",
			result: &service.UseResult{
				Changes: []service.ChangeResult{
					{App: "npm", Err: errors.New("npm is broken")},
					{App: "gem", To: "https://gems.example.com"},
				},
			},
			err:  errors.New("npm is broken"),
			want: "npm is broken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderChange("cn", tt.result, tt.err)
			if !strings.Contains(got, tt.want) {
				t.Errorf("renderChange() = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

func TestRenderEntriesGroupsByApp(t *testing.T) {
	lines := renderEntries([]service.RegistryEntry{
		{App: "npm", Region: "cn", URL: "https://registry.npmmirror.com"},
		{App: "npm", Region: "us", URL: "https://registry.npmjs.org"},
		{App: "pip", Region: "us", URL: "https://pypi.org/simple"},
	}, 80)

	joined := strings.Join(lines, "\n")
	for _, want := range []string{"npm", "pip", "https://registry.npmmirror.com", "https://pypi.org/simple"} {
		if !strings.Contains(joined, want) {
			t.Errorf("renderEntries() dropped %q:\n%s", want, joined)
		}
	}
	// One heading per app plus a blank line between the groups.
	if len(lines) != 6 {
		t.Errorf("renderEntries() produced %d lines, want 6:\n%s", len(lines), joined)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("https://example.com", 80); got != "https://example.com" {
		t.Errorf("truncate() shortened a URL that fits: %q", got)
	}
	if got := truncate("https://example.com", 10); got != "https://e…" {
		t.Errorf("truncate() = %q, want it cut to the width", got)
	}
}
