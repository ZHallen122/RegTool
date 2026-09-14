package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZHallen122/RegTool/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

// entriesMsg carries the mirrors a page asked for, or the error that stopped
// them from arriving.
type entriesMsg struct {
	app     string
	entries []service.RegistryEntry
	err     error
}

// listCmd loads the mirrors of one app, or of every app when app is empty.
func listCmd(svc *service.Service, app string) tea.Cmd {
	return func() tea.Msg {
		entries, err := svc.List(context.Background(), app)
		return entriesMsg{app: app, entries: entries, err: err}
	}
}

// renderEntries lays the mirrors out grouped by app, and returns the lines so
// the caller can scroll them.
func renderEntries(entries []service.RegistryEntry, width int) []string {
	lines := make([]string, 0, len(entries)*2)
	app := ""
	for _, entry := range entries {
		if entry.App != app {
			app = entry.App
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, GetStyledTitle(app))
		}
		lines = append(lines, GetInfoText(fmt.Sprintf("  %-4s", entry.Region))+" "+GetSuccessText(truncate(entry.URL, width)))
	}
	return lines
}

// truncate shortens a URL that would not fit the terminal.
func truncate(text string, width int) string {
	if width <= 4 || len(text) <= width {
		return text
	}
	return text[:width-1] + "…"
}

// scrollView renders the visible slice of lines with the page's chrome.
func scrollView(title string, lines []string, scroll, height int, loading bool, err error, footer string) string {
	var out strings.Builder
	out.WriteString(GetStyledTitle(title) + "\n\n")

	switch {
	case err != nil:
		out.WriteString(GetErrorText("Error: "+err.Error()) + "\n")
	case loading:
		out.WriteString(GetInfoText("Loading...") + "\n")
	default:
		end := min(scroll+height, len(lines))
		for _, line := range lines[min(scroll, end):end] {
			out.WriteString(line + "\n")
		}
	}

	out.WriteString("\n" + GetInfoText(footer) + "\n")
	return out.String()
}

type listAllRegistryModel struct {
	svc     *service.Service
	lines   []string
	err     error
	loading bool
	scroll  int
	height  int
	width   int
}

func newListAllRegistryModel(svc *service.Service) listAllRegistryModel {
	return listAllRegistryModel{svc: svc, loading: true, height: 20, width: 80}
}

func (m listAllRegistryModel) Init() tea.Cmd {
	return listCmd(m.svc, "")
}

func (m listAllRegistryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return GetCommand(mainMenuName)
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			if m.scroll < len(m.lines)-m.height {
				m.scroll++
			}
		case "r":
			if !m.loading {
				m.loading, m.lines, m.err, m.scroll = true, nil, nil, 0
				return m, listCmd(m.svc, "")
			}
		}
	case tea.WindowSizeMsg:
		m.height = max(msg.Height-6, 1)
		m.width = max(msg.Width-4, 20)
	case entriesMsg:
		m.loading = false
		m.err = msg.err
		m.lines = renderEntries(msg.entries, m.width)
	}
	return m, nil
}

func (m listAllRegistryModel) View() string {
	return scrollView("Registry List", m.lines, m.scroll, m.height, m.loading, m.err,
		"Press 'q' to go back, 'r' to reload, use j/k to scroll")
}
