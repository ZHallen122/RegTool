package tui

import (
	"github.com/ZHallen122/RegTool/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type listRegistryModel struct {
	svc     *service.Service
	input   textinput.Model
	appName string
	asking  bool
	loading bool
	lines   []string
	err     error
	scroll  int
	height  int
	width   int
}

func newListRegistryModel(svc *service.Service) listRegistryModel {
	ti := textinput.New()
	ti.Placeholder = "App Name"
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 20

	return listRegistryModel{svc: svc, input: ti, asking: true, height: 20, width: 80}
}

func (m listRegistryModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m listRegistryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.asking {
			switch msg.String() {
			case "esc", "ctrl+c":
				return GetCommand(mainMenuName)
			case "enter":
				if m.input.Value() == "" {
					return m, nil
				}
				m.appName = m.input.Value()
				m.input.Blur()
				m.asking, m.loading = false, true
				return m, listCmd(m.svc, m.appName)
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

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

func (m listRegistryModel) View() string {
	if m.asking {
		return "List Registry by App Name\n\nEnter the app name:\n" + m.input.View() +
			"\n\nPress 'enter' to confirm, 'esc' to go back.\n"
	}
	return scrollView("Registry List: "+m.appName, m.lines, m.scroll, m.height, m.loading, m.err,
		"Press 'q' to go back, use j/k to scroll")
}
