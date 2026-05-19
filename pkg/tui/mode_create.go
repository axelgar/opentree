package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/gitutil"
)

// updateCreate handles both ModeCreate and ModeCreateFromIssue. Their key
// semantics diverge only inside the enter branch.
func updateCreate(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := m.input.Value()
		if val == "" {
			return m, nil
		}
		if m.mode == ModeCreateFromIssue {
			m.resetCreateMode()
			m.workspaceCreating = true
			m.workspaceCreatingName = "issue " + val
			return m, tea.Batch(m.createWorkspaceFromIssueCmd(val), spinnerTickCmd())
		}
		if m.create.step == 0 {
			if err := gitutil.ValidateBranchName(val); err != nil {
				m.err = err
				m.appendErrLog(err.Error())
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
					return clearErrorMsg{}
				})
			}
			m.create.newBranchName = val
			m.create.step = 1
			m.focusInput("Base branch", m.cfg.Worktree.DefaultBase)
			return m, textinput.Blink
		}
		branchName := m.create.newBranchName
		baseBranch := val
		m.resetCreateMode()
		m.workspaceCreating = true
		m.workspaceCreatingName = branchName
		return m, tea.Batch(m.createWorkspaceCmd(branchName, baseBranch), spinnerTickCmd())
	case "esc":
		m.resetCreateMode()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func viewCreate(m Model) string {
	var stepLabel string
	if m.create.step == 0 {
		stepLabel = "Step 1/2 — Branch name"
	} else {
		stepLabel = fmt.Sprintf("Step 2/2 — Base branch  (branching from: %s)", m.create.newBranchName)
	}
	return appStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
		titleStyle.Render("Create New Workspace"),
		stepLabelStyle.Render(stepLabel),
		m.input.View(),
		helpStyle.Render("Enter to continue • Esc to cancel"),
	))
}

func viewCreateFromIssue(m Model) string {
	return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
		titleStyle.Render("Create Workspace from GitHub Issue"),
		m.input.View(),
		helpStyle.Render("Enter issue number • Esc to cancel"),
	))
}
