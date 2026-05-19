package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func updateErrorLog(m Model, _ tea.KeyMsg) (Model, tea.Cmd) {
	// Any key closes the overlay.
	m.mode = ModeList
	return m, nil
}

func viewErrorLog(m Model) string {
	var sb strings.Builder
	sb.WriteString(errLogTitleStyle.Render("Error Log") + "\n\n")
	if len(m.errorLog.entries) == 0 {
		sb.WriteString(errLogLineStyle.Render("No errors recorded."))
	} else {
		for _, entry := range m.errorLog.entries {
			sb.WriteString(errLogLineStyle.Render(entry))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n" + helpStyle.Render("Any key to close"))
	return appStyle.Render(sb.String())
}
