// Package tui implements the interactive bubbletea interface. Every page is a
// bubbletea model that registers itself with the package level command
// registry in its init function; Run starts the main menu.
package tui

import (
	"fmt"
	"strings"

	"github.com/ZHallen122/RegTool/source/structs"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	// regions is derived from structs so the menu can never offer a region the
	// rest of the tool does not understand.
	regions = structs.AllRegionStrings()
)

type mainMenuModel struct {
	cursor  int
	choices []string
	names   []string
	width   int
}

func newMainMenuModel() mainMenuModel {
	return mainMenuModel{
		choices: ListCommandDescriptions(),
		names:   ListCommandNames(),
		width:   80,
	}
}

func (m mainMenuModel) Init() tea.Cmd {
	return nil
}

func (m mainMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(m.choices) - 1
			}
		case "down", "j":
			m.cursor++
			if m.cursor >= len(m.choices) {
				m.cursor = 0
			}
		case "enter", " ":
			cmd, initCmd := GetCommand(m.names[m.cursor])
			if cmd != nil {
				return cmd, initCmd
			}
		}
	}
	return m, nil
}

func (m mainMenuModel) View() string {
	doc := strings.Builder{}

	// Title
	doc.WriteString(GetStyledTitle("RegistryHub") + "\n")

	// Menu options
	for i, choice := range m.choices {
		doc.WriteString(GetStyledOption(choice, m.cursor == i) + "\n")
	}

	// Quit instruction
	doc.WriteString("\n" + GetStyledQuitText())

	return borderedBox(doc.String())
}

// Run starts the interactive interface and blocks until the user quits.
func Run() error {
	RegisterCommand(mainMenuName, "Main Menu", newMainMenuModel())
	p := tea.NewProgram(newMainMenuModel())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run the interactive interface: %w", err)
	}
	return nil
}
