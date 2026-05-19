package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/state"
)

// Shared test helpers used across all mode_*_test.go files in this package.

// newTestModel builds a Model with no external dependencies. Tests that only
// exercise in-process logic (state transitions, View rendering, pure functions)
// should use this instead of NewModel, which requires a real git repo and tmux.
func newTestModel(workspaces ...WorkspaceItem) Model {
	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.CharLimit = 50
	ti.Width = 30
	return Model{
		cfg:        config.Default(),
		input:      ti,
		help:       help.New(),
		keys:       keys,
		workspaces: workspaces,
		width:      120,
		height:     40,
		list:       listState{selected: make(map[string]bool)},
	}
}

func testWS(name string) WorkspaceItem {
	return WorkspaceItem{
		Workspace: &state.Workspace{
			Name:       name,
			Branch:     "feature/" + name,
			BaseBranch: "main",
		},
		DiffStat: "2 files changed",
	}
}

func testWSWithPR(name, prURL string) WorkspaceItem {
	ws := testWS(name)
	ws.PRURL = prURL
	ws.PRStatus = "open"
	return ws
}

func testWSWithWindow(name string) WorkspaceItem {
	ws := testWS(name)
	ws.WindowID = "@1"
	return ws
}

func testWSWithIssue(name string, issueNumber int, issueTitle string) WorkspaceItem {
	ws := testWS(name)
	ws.IssueNumber = issueNumber
	ws.IssueTitle = issueTitle
	return ws
}

// applyUpdate calls m.Update and casts the result back to Model.
func applyUpdate(m Model, msg tea.Msg) (Model, tea.Cmd) {
	newM, cmd := m.Update(msg)
	return newM.(Model), cmd
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}
