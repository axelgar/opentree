package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func updatePRGenerating(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	// Bug B fix: gate keys while generating; esc cancels (Bug C fix).
	if msg.String() == "esc" {
		m.mode = ModeList
		m.pr.cancelled = true
		m.pr.wsName = ""
		m.pr.branch = ""
		m.pr.base = ""
	}
	return m, nil
}

func viewPRGenerating(m Model) string {
	return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
		titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.pr.branch, m.pr.base)),
		helpStyle.Render("Generating title and description from commits…"),
		helpStyle.Render("Esc to cancel"),
	))
}
