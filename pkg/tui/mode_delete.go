package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func updateDelete(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.del.target != "" {
			target := m.del.target
			m.mode = ModeList
			m.del = deleteState{}
			m.workspaceDeleting = true
			m.workspaceDeletingName = target
			return m, tea.Batch(m.deleteWorkspaceCmd(target), spinnerTickCmd())
		}
		// batch delete
		targets := make([]string, 0, len(m.list.selected))
		for name := range m.list.selected {
			targets = append(targets, name)
		}
		m.mode = ModeList
		m.del = deleteState{}
		m.workspaceDeletingNames = make(map[string]bool)
		for _, name := range targets {
			m.workspaceDeletingNames[name] = true
		}
		m.list.selected = make(map[string]bool)
		m.workspaceDeleting = true
		m.workspaceDeletingName = fmt.Sprintf("%d workspaces", len(targets))
		return m, tea.Batch(m.batchDeleteWorkspaceCmd(targets), spinnerTickCmd())
	case "n", "esc":
		m.mode = ModeList
		m.del = deleteState{}
	}
	return m, nil
}

func viewDelete(m Model) string {
	var titleMsg string
	if m.del.target != "" {
		titleMsg = fmt.Sprintf("Delete workspace %q?", m.del.target)
	} else {
		names := make([]string, 0, len(m.list.selected))
		for name := range m.list.selected {
			names = append(names, name)
		}
		sort.Strings(names)
		titleMsg = fmt.Sprintf("Delete %d workspaces: %s?", len(names), strings.Join(names, ", "))
	}
	footer := fmt.Sprintf("%s %s  •  %s %s",
		confirmKeyStyle.Render("y"), confirmLabelStyle.Render("confirm"),
		confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel"),
	)
	content := fmt.Sprintf("%s\n\n%s\n\n%s",
		dangerStyle.Render(titleMsg),
		confirmLabelStyle.Render("The worktree, tmux window, and all local changes will be removed."),
		footer,
	)
	return appStyle.Render(deleteDialogStyle.Render(content))
}
