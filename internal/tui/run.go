// Package tui implements the interactive bubbletea interface. Every page is a
// bubbletea model that talks to the same [service.Service] the CLI does, so the
// two front ends can never drift apart; Run wires the pages up and starts the
// main menu.
package tui

import (
	"fmt"
	"strings"

	"github.com/ZHallen122/RegTool/internal/service"
	"github.com/ZHallen122/RegTool/source/structs"

	tea "github.com/charmbracelet/bubbletea"
)

// regions is derived from structs so the menu can never offer a region the
// rest of the tool does not understand.
var regions = structs.AllRegionStrings()

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
	doc.WriteString(GetStyledTitle("RegTool") + "\n")

	// Menu options
	for i, choice := range m.choices {
		doc.WriteString(GetStyledOption(choice, m.cursor == i) + "\n")
	}

	// Quit instruction
	doc.WriteString("\n" + GetStyledQuitText())

	return borderedBox(doc.String())
}

// Run starts the interactive interface and blocks until the user quits. Every
// page runs against svc, which must not be nil.
func Run(svc *service.Service) error {
	if svc == nil {
		return fmt.Errorf("the interactive interface needs a service to talk to")
	}

	registerPages(svc)
	RegisterCommand(mainMenuName, "Main Menu", newMainMenuModel())

	if _, err := tea.NewProgram(newMainMenuModel()).Run(); err != nil {
		return fmt.Errorf("failed to run the interactive interface: %w", err)
	}
	return nil
}

// registerPages installs every page, in menu order. Registering here rather
// than in each file's init keeps the service out of package level state.
func registerPages(svc *service.Service) {
	RegisterCommand("changeAll", "Change All to Region", newChangeAllModel(svc))
	RegisterCommand("changeNameRegion", "Change an App to Region", newChangeNameRegionModel(svc))
	RegisterCommand("listAllRegistry", "List All Registry", newListAllRegistryModel(svc))
	RegisterCommand("listRegistry", "List Registry by App Name", newListRegistryModel(svc))
	RegisterCommand("update", "Init/Update All Apps Recording", newUpdateModel(svc))
}
