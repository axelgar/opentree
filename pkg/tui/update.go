package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m Model) isWorkspaceInFlight(name string) bool {
	return m.workspaceDeletingName == name || m.workspaceDeletingNames[name]
}

// Update is the top-level Bubble Tea dispatcher.
// Key messages route through m.mode to per-mode handlers in mode_*.go.
// Non-key messages (ticks, async results) stay here because they don't
// belong to any single mode.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		// Clamp diff scroll offset when terminal resizes while diff is open.
		if m.mode == ModeDiff {
			m.clampDiffScroll()
		}

	case tea.KeyMsg:
		switch m.mode {
		case ModeErrorLog:
			return updateErrorLog(m, msg)
		case ModeAgentSelect:
			return updateAgentSelect(m, msg)
		case ModeDiff:
			return updateDiff(m, msg)
		case ModeDelete:
			return updateDelete(m, msg)
		case ModePRCreating:
			return updatePRCreating(m, msg)
		case ModePRGenerating:
			return updatePRGenerating(m, msg)
		case ModeCreateFromRemote:
			return updateCreateFromRemote(m, msg)
		case ModeCreate, ModeCreateFromIssue:
			return updateCreate(m, msg)
		case ModeList:
			return updateList(m, msg)
		}

	case spinnerTickMsg:
		if m.workspaceCreating || m.workspaceDeleting {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTickCmd()
		}
		return m, nil

	case remoteBranchesLoadedMsg:
		if msg.err != nil {
			m.resetCreateMode()
			m.err = fmt.Errorf("failed to load remote branches: %w", msg.err)
			m.appendErrLog(m.err.Error())
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearErrorMsg{}
			})
		}
		m.create.remoteBranches = msg.branches
		m.create.filteredBranches = filterBranches(msg.branches, m.input.Value())
		m.create.branchSuggestionCursor = 0

	case loadedWorkspacesMsg:
		m.workspaces = msg.workspaces
		visible := m.visibleWorkspaces()
		if m.list.cursor >= len(visible) {
			m.list.cursor = max(0, len(visible)-1)
		}
		return m, m.capturePreviewCmd()

	case createdWorkspaceMsg:
		m.workspaceCreating = false
		m.workspaceCreatingName = ""
		if msg.wsName != "" {
			if m.stateStore != nil {
				if ws, err := m.stateStore.GetWorkspace(msg.wsName); err == nil && ws != nil {
					item := WorkspaceItem{
						Workspace: ws,
						DiffStat:  "No changes",
					}
					m.workspaces = append(m.workspaces, item)
				}
			}
			return m, m.checkBranchStatusCmd(msg.wsName, msg.branch, msg.worktreeDir, false)
		}
		return m, nil

	case deletedWorkspaceMsg:
		m.workspaceDeleting = false
		m.workspaceDeletingName = ""
		m.workspaceDeletingNames = make(map[string]bool)
		m.list.selected = make(map[string]bool)
		return m, m.loadWorkspacesCmd

	case capturePreviewMsg:
		m.agentPreview = msg.lines

	case refreshTickMsg:
		return m, tea.Batch(
			m.loadWorkspacesCmd,
			tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
		)

	case previewTickMsg:
		return m, tea.Batch(
			m.capturePreviewCmd(),
			tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return previewTickMsg{} }),
		)

	case prCreatedMsg:
		ws, err := m.stateStore.GetWorkspace(msg.wsName)
		if err == nil {
			ws.PRURL = msg.prURL
			ws.PRStatus = "open"
			_ = m.stateStore.UpdateWorkspace(ws)
		}
		var branch, worktreeDir string
		var wasPushed bool
		for _, item := range m.workspaces {
			if item.Name == msg.wsName {
				branch = item.Branch
				worktreeDir = item.WorktreeDir
				wasPushed = item.BranchPushed
				break
			}
		}
		return m, tea.Batch(m.loadWorkspacesCmd, m.checkBranchStatusCmd(msg.wsName, branch, worktreeDir, wasPushed))

	case prContentGeneratedMsg:
		// Bug C fix: drop the message if the user escaped out of prGenerating
		// before the async content arrived.
		if m.pr.cancelled {
			m.pr.cancelled = false
			return m, nil
		}
		m.mode = ModePRCreating
		m.pr.step = 0
		m.pr.bodyPrefill = msg.body
		m.focusInput("PR title", msg.title)
		return m, textinput.Blink

	case prStatusTickMsg:
		cmds := []tea.Cmd{
			tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		}
		for _, ws := range m.workspaces {
			// Skip workspaces that are fully done (merged PR and remote branch gone).
			if ws.PRStatus == "merged" && ws.RemoteDeleted {
				continue
			}
			cmds = append(cmds, m.checkBranchStatusCmd(ws.Name, ws.Branch, ws.WorktreeDir, ws.BranchPushed))
		}
		return m, tea.Batch(cmds...)

	case prStatusCheckedMsg:
		ws, err := m.stateStore.GetWorkspace(msg.wsName)
		if err == nil {
			ws.PRURL = msg.prURL
			ws.PRStatus = msg.prStatus
			_ = m.stateStore.UpdateWorkspace(ws)
		}
		for i, item := range m.workspaces {
			if item.Name == msg.wsName {
				m.workspaces[i].PRURL = msg.prURL
				m.workspaces[i].PRStatus = msg.prStatus
				break
			}
		}

	case ciStatusCheckedMsg:
		if m.ciStatus == nil {
			m.ciStatus = make(map[string]string)
		}
		m.ciStatus[msg.wsName] = msg.ciStatus

	case branchStatusCheckedMsg:
		ws, err := m.stateStore.GetWorkspace(msg.wsName)
		if err == nil {
			if !msg.status.RemoteCheckFailed {
				ws.BranchPushed = msg.status.Pushed
				ws.RemoteDeleted = msg.status.RemoteDeleted
			}
			ws.MergeConflicts = msg.status.MergeConflicts
			if msg.status.PRURL != "" {
				ws.PRURL = msg.status.PRURL
			}
			if msg.status.PRState != "" {
				ws.PRStatus = msg.status.PRState
			}
			_ = m.stateStore.UpdateWorkspace(ws)
		}
		for i, item := range m.workspaces {
			if item.Name == msg.wsName {
				if !msg.status.RemoteCheckFailed {
					m.workspaces[i].BranchPushed = msg.status.Pushed
					m.workspaces[i].RemoteDeleted = msg.status.RemoteDeleted
				}
				m.workspaces[i].MergeConflicts = msg.status.MergeConflicts
				if msg.status.PRURL != "" {
					m.workspaces[i].PRURL = msg.status.PRURL
				}
				if msg.status.PRState != "" {
					m.workspaces[i].PRStatus = msg.status.PRState
				}
				break
			}
		}
		if msg.status.CIStatus != "" {
			if m.ciStatus == nil {
				m.ciStatus = make(map[string]string)
			}
			m.ciStatus[msg.wsName] = msg.status.CIStatus
		}

	case attachFinishedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.appendErrLog(msg.err.Error())
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearErrorMsg{}
			})
		}
		return m, m.loadWorkspacesCmd

	case errMsg:
		// Bug A fix: exhaustively return to the list and zero every per-mode
		// sub-struct. The old handler reset only `creating`, `filtering`,
		// `prCreating`, leaving deletion/diff/agent-select/error-log/PR-generating
		// flags hanging if an error fired while those modes were active.
		m.workspaceCreating = false
		m.workspaceDeleting = false
		m.workspaceDeletingNames = make(map[string]bool)
		m.resetToList()
		m.err = msg.err
		m.appendErrLog(msg.err.Error())
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearErrorMsg{}
		})

	case diffLoadedMsg:
		m.mode = ModeDiff
		m.diff.content = msg.content
		m.diff.scrollOffset = 0
		m.diff.wsName = msg.wsName

	case clearErrorMsg:
		m.err = nil

	case reviewsSentMsg:
		if msg.count == 0 {
			m.err = fmt.Errorf("no review comments found for %q", msg.wsName)
			m.appendErrLog(m.err.Error())
		}
	}

	return m, nil
}

func (m *Model) clampDiffScroll() {
	lines := len(strings.Split(m.diff.content, "\n"))
	availHeight := m.height - 8
	if availHeight < 5 {
		availHeight = 5
	}
	maxScroll := lines - availHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.diff.scrollOffset > maxScroll {
		m.diff.scrollOffset = maxScroll
	}
}

// resetCreateMode zeroes the create sub-struct and returns to the list view.
// Used when a create dialog is cancelled or completes.
func (m *Model) resetCreateMode() {
	m.mode = ModeList
	m.create = createState{}
	m.input.SetValue("")
	m.input.Placeholder = "New branch name"
}

func (m *Model) transientErrCmd(msg string) tea.Cmd {
	m.err = fmt.Errorf("%s", msg)
	m.appendErrLog(msg)
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearErrorMsg{}
	})
}

func (m *Model) appendErrLog(msg string) {
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s", ts, msg)
	m.errorLog.entries = append(m.errorLog.entries, entry)
	if len(m.errorLog.entries) > 20 {
		m.errorLog.entries = m.errorLog.entries[len(m.errorLog.entries)-20:]
	}
}
