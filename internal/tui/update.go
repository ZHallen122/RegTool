package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZHallen122/RegTool/internal/service"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// updateModel is the page that records what every installed app currently
// points at, so a later change has something to be compared against.
type updateModel struct {
	svc      *service.Service
	spinner  spinner.Model
	running  bool
	done     bool
	statuses []service.AppStatus
	err      error
}

func newUpdateModel(svc *service.Service) updateModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Align(lipgloss.Center)
	return updateModel{svc: svc, spinner: s}
}

func (m updateModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m updateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if !m.running && !m.done {
				m.running = true
				return m, tea.Batch(m.spinner.Tick, refreshCmd(m.svc))
			}
			if m.done {
				return GetCommand(mainMenuName)
			}
		case "q", "esc", "ctrl+c":
			return GetCommand(mainMenuName)
		}
	case refreshDoneMsg:
		m.running, m.done = false, true
		m.statuses, m.err = msg.statuses, msg.err
		return m, nil
	}

	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m updateModel) View() string {
	doc := strings.Builder{}
	doc.WriteString(GetStyledTitle("Init/Update All Apps Recording") + "\n")

	switch {
	case m.running:
		doc.WriteString(m.spinner.View() + "Reading every installed package manager...\n")
	case m.done:
		if m.err != nil {
			doc.WriteString(GetErrorText("Error: "+m.err.Error()) + "\n")
		} else {
			doc.WriteString(GetSuccessText("Recorded the current registries.") + "\n")
		}
		for _, status := range m.statuses {
			doc.WriteString(fmt.Sprintf(" - %s %s\n",
				GetInfoText(status.App), GetSuccessText(status.URL)))
		}
		doc.WriteString("\nPress 'enter' to return to the main menu.\n")
	default:
		doc.WriteString("Record what every installed app currently points at?\n" +
			"Press 'enter' to confirm, 'q' or 'esc' to go back.\n")
	}
	return doc.String()
}

// refreshDoneMsg carries the outcome of a refresh back to the page.
type refreshDoneMsg struct {
	statuses []service.AppStatus
	err      error
}

// refreshCmd records the current registries and reads them back for display.
func refreshCmd(svc *service.Service) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := svc.Refresh(ctx); err != nil {
			return refreshDoneMsg{err: err}
		}
		statuses, err := svc.Status(ctx)
		return refreshDoneMsg{statuses: statuses, err: err}
	}
}
