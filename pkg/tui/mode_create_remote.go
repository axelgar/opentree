package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func updateCreateFromRemote(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.create.branchSuggestionCursor > 0 {
			m.create.branchSuggestionCursor--
		}
		return m, nil
	case "down":
		if m.create.branchSuggestionCursor < len(m.create.filteredBranches)-1 {
			m.create.branchSuggestionCursor++
		}
		return m, nil
	case "tab":
		if len(m.create.filteredBranches) > 0 {
			m.input.SetValue(m.create.filteredBranches[m.create.branchSuggestionCursor])
			m.create.filteredBranches = filterBranches(m.create.remoteBranches, m.input.Value())
			m.create.branchSuggestionCursor = 0
		}
		return m, nil
	case "enter":
		var branchName string
		if m.create.branchSuggestionCursor < len(m.create.filteredBranches) {
			branchName = m.create.filteredBranches[m.create.branchSuggestionCursor]
		} else {
			branchName = m.input.Value()
		}
		if branchName == "" {
			return m, nil
		}
		m.resetCreateMode()
		m.workspaceCreating = true
		m.workspaceCreatingName = branchName
		return m, tea.Batch(m.createWorkspaceFromRemoteCmd(branchName), spinnerTickCmd())
	case "esc":
		m.resetCreateMode()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.create.filteredBranches = filterBranches(m.create.remoteBranches, m.input.Value())
	m.create.branchSuggestionCursor = 0
	return m, cmd
}

func viewCreateFromRemote(m Model) string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Create Workspace from Remote Branch"))
	sb.WriteString("\n\n")
	sb.WriteString(m.input.View())
	sb.WriteString("\n")
	if len(m.create.filteredBranches) > 0 {
		sb.WriteString("\n")
		for i, b := range m.create.filteredBranches {
			if i == m.create.branchSuggestionCursor {
				sb.WriteString(selectedItemStyle.Render("▶ " + b))
			} else {
				sb.WriteString(itemStyle.Render("  " + b))
			}
			sb.WriteString("\n")
		}
	} else if len(m.create.remoteBranches) == 0 {
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("  loading branches…"))
		sb.WriteString("\n")
	} else {
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("  no branches match"))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render("↑/↓ navigate • Tab select • Enter confirm • Esc cancel"))
	return appStyle.Render(sb.String())
}
