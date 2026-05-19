package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func updatePRCreating(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := m.input.Value()
		if m.pr.step == 0 {
			m.pr.title = val
			m.pr.step = 1
			m.focusInput("PR body (optional)", m.pr.bodyPrefill)
			return m, textinput.Blink
		}
		// step 1: body confirmed
		wsName := m.pr.wsName
		title := m.pr.title
		body := val
		m.mode = ModeList
		m.pr = prState{}
		m.input.SetValue("")
		m.input.Placeholder = "New branch name"
		return m, m.createPRCmd(wsName, title, body)
	case "esc":
		m.mode = ModeList
		m.pr = prState{}
		m.input.SetValue("")
		m.input.Placeholder = "New branch name"
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func viewPRCreating(m Model) string {
	var stepLabel string
	if m.pr.step == 0 {
		stepLabel = "Step 1/2 — PR title"
	} else {
		stepLabel = fmt.Sprintf("Step 2/2 — PR body  (title: %s)", m.pr.title)
	}
	return appStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
		titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.pr.branch, m.pr.base)),
		stepLabelStyle.Render(stepLabel),
		m.input.View(),
		helpStyle.Render("Enter to continue • Esc to cancel"),
	))
}
