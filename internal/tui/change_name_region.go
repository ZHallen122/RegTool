package tui

import (
	"fmt"

	"github.com/ZHallen122/RegTool/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type changeNameRegionModel struct {
	svc     *service.Service
	input   textinput.Model
	stage   int
	appName string
	region  string
	cursor  int
	summary string
}

func newChangeNameRegionModel(svc *service.Service) changeNameRegionModel {
	ti := textinput.New()
	ti.Placeholder = "Enter app name"
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 20
	return changeNameRegionModel{svc: svc, input: ti}
}

func (m changeNameRegionModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m changeNameRegionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			// Stage 0 is a text input, where 'q' is a character to type.
			if m.stage != 0 || msg.String() != "q" {
				return GetCommand(mainMenuName)
			}
		case "enter":
			switch m.stage {
			case 0:
				if m.input.Value() == "" {
					return m, nil
				}
				m.appName = m.input.Value()
				m.input.Reset()
				m.input.Blur()
				m.stage = 1
				return m, nil
			case 1:
				m.region = regions[m.cursor]
				m.stage = 2
				return m, nil
			case 2:
				m.stage = 3
				return m, changeCmd(m.svc, m.region, []string{m.appName})
			case 4:
				return GetCommand(mainMenuName)
			}
		case "up", "k":
			if m.stage == 1 && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.stage == 1 && m.cursor < len(regions)-1 {
				m.cursor++
			}
		}
	case changeDoneMsg:
		m.summary = msg.summary
		m.stage = 4
		return m, nil
	}

	if m.stage == 0 {
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

func (m changeNameRegionModel) View() string {
	switch m.stage {
	case 0:
		return fmt.Sprintf(
			"Change an App to Region\n\n%s\n\nPress 'enter' to confirm, 'esc' to go back.\n",
			m.input.View(),
		)
	case 1:
		return regionPicker("Change an App to Region", "App Name: "+m.appName, m.cursor)
	case 2:
		return fmt.Sprintf(
			"Change an App to Region\n\nApp Name: %s\nRegion: %s\n\nPress 'enter' to apply change, 'q' to go back.\n",
			m.appName,
			m.region,
		)
	case 3:
		return fmt.Sprintf("Change an App to Region\n\nSwitching %s to %s...\n", m.appName, m.region)
	case 4:
		return fmt.Sprintf("%s\n\nPress 'enter' to return to main menu.\n", m.summary)
	default:
		return "Unexpected stage."
	}
}
