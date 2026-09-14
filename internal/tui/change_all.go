package tui

import (
	"context"
	"fmt"

	"github.com/ZHallen122/RegTool/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fatih/color"
)

type changeAllModel struct {
	svc     *service.Service
	cursor  int
	region  string
	stage   int
	summary string
}

func newChangeAllModel(svc *service.Service) changeAllModel {
	return changeAllModel{svc: svc}
}

func (m changeAllModel) Init() tea.Cmd {
	return nil
}

func (m changeAllModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return GetCommand(mainMenuName)
		case "enter":
			switch m.stage {
			case 0:
				m.region = regions[m.cursor]
				m.stage = 1
				return m, nil
			case 1:
				// Stage 2 renders "working" until the result arrives.
				m.stage = 2
				return m, changeCmd(m.svc, m.region, nil)
			case 3:
				return GetCommand(mainMenuName)
			}
		case "up", "k":
			if m.stage == 0 && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.stage == 0 && m.cursor < len(regions)-1 {
				m.cursor++
			}
		}
	case changeDoneMsg:
		m.summary = msg.summary
		m.stage = 3
		return m, nil
	}
	return m, nil
}

func (m changeAllModel) View() string {
	switch m.stage {
	case 0:
		return regionPicker("Change All to Region", "", m.cursor)
	case 1:
		return fmt.Sprintf(
			"Change All to Region\n\nSelected Region: %s\n\nPress 'enter' to apply change to all apps, 'q' to go back.\n",
			m.region,
		)
	case 2:
		return fmt.Sprintf("Change All to Region\n\nSwitching every installed app to %s...\n", m.region)
	case 3:
		return fmt.Sprintf("%s\n\nPress 'enter' to return to main menu.\n", m.summary)
	default:
		return "Unexpected stage."
	}
}

// regionPicker renders the region list shared by the two change pages.
func regionPicker(title, subtitle string, cursor int) string {
	s := title + "\n\n"
	if subtitle != "" {
		s += subtitle + "\n\n"
	}
	s += "Select a region:\n\n"
	for i, region := range regions {
		marker := " "
		line := region
		if cursor == i {
			marker = ">"
			line = color.New(color.FgBlue).Add(color.Bold).Sprint(region)
		}
		s += fmt.Sprintf("%s %s\n", marker, line)
	}
	return s + "\nPress 'enter' to confirm, 'q' to go back.\n"
}

// changeDoneMsg carries the rendered outcome of a change back to the page.
type changeDoneMsg struct{ summary string }

// changeCmd runs a change in the background and reports what happened. apps of
// nil means every installed app.
func changeCmd(svc *service.Service, region string, apps []string) tea.Cmd {
	return func() tea.Msg {
		result, err := svc.Use(context.Background(), region, apps, false)
		return changeDoneMsg{summary: renderChange(region, result, err)}
	}
}

// renderChange turns a use result into the few lines the pages display.
func renderChange(region string, result *service.UseResult, err error) string {
	if result == nil {
		return GetErrorText(fmt.Sprintf("Error: %s", err))
	}

	summary := ""
	for _, change := range result.Changes {
		switch {
		case change.Err != nil:
			summary += GetErrorText(fmt.Sprintf("  %s: %s", change.App, change.Err)) + "\n"
		case change.Noop:
			summary += GetInfoText(fmt.Sprintf("  %s: already on %s", change.App, change.To)) + "\n"
		default:
			summary += GetSuccessText(fmt.Sprintf("  %s: %s", change.App, change.To)) + "\n"
		}
	}
	if summary == "" {
		summary = GetInfoText("  no supported package manager was found on this machine") + "\n"
	}

	header := GetSuccessText(fmt.Sprintf("Switched to region %s", region))
	if err != nil {
		header = GetErrorText(fmt.Sprintf("Some apps could not be switched to region %s", region))
	}
	footer := ""
	if result.SnapshotID != "" {
		footer = "\n" + GetInfoText("Snapshot "+result.SnapshotID+": run 'regtool undo' to put it back")
	}
	return header + "\n" + summary + footer
}
