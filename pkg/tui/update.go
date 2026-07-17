package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
)

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m Model) isWorkspaceInFlight(name string) bool {
	return m.workspaceDeletingName == name || m.workspaceDeletingNames[name]
}

// markDeleting adds names to the in-flight delete set (without clobbering
// deletes already in flight) and refreshes the spinner label.
func (m *Model) markDeleting(names ...string) {
	if m.workspaceDeletingNames == nil {
		m.workspaceDeletingNames = make(map[string]bool)
	}
	for _, name := range names {
		m.workspaceDeletingNames[name] = true
	}
	m.workspaceDeleting = true
	if len(m.workspaceDeletingNames) == 1 {
		for name := range m.workspaceDeletingNames {
			m.workspaceDeletingName = name
		}
	} else {
		m.workspaceDeletingName = fmt.Sprintf("%d workspaces", len(m.workspaceDeletingNames))
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		// Clamp diff scroll offset when terminal resizes while diff is open.
		if m.diffViewing {
			m.clampDiffScroll()
		}

	case tea.KeyMsg:
		// ctrl+c always quits, even inside dialogs and text inputs where
		// other keys are captured as text.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		// Error log overlay swallows all keys
		if m.showErrLog {
			m.showErrLog = false
			return m, nil
		}

		// Agent selection mode
		if m.agentSelecting {
			agents := config.PredefinedAgents
			switch msg.String() {
			case "up", "k":
				if m.agentCursor > 0 {
					m.agentCursor--
				}
			case "down", "j":
				if m.agentCursor < len(agents)-1 {
					m.agentCursor++
				}
			case "enter":
				agent := agents[m.agentCursor]
				m.cfg.Agent.Command = agent.Command
				if agent.Args != nil {
					m.cfg.Agent.Args = agent.Args
				} else {
					m.cfg.Agent.Args = []string{}
				}
				m.agentSelecting = false
				// Persist only the agent keys (not the merged config), and
				// surface failures instead of silently losing the selection.
				if err := config.SetKeys(config.FindConfigFile(), map[string]any{
					"agent.command": m.cfg.Agent.Command,
					"agent.args":    m.cfg.Agent.Args,
				}); err != nil {
					return m, m.transientErrCmd(fmt.Sprintf("failed to save agent selection: %v", err))
				}
			case "esc", "q":
				m.agentSelecting = false
			}
			return m, nil
		}

		// Diff view mode
		if m.diffViewing {
			switch msg.String() {
			case "esc", "q":
				m.diffViewing = false
				m.diffContent = ""
				m.diffScrollOffset = 0
				m.diffWsName = ""
			case "up", "k":
				if m.diffScrollOffset > 0 {
					m.diffScrollOffset--
				}
			case "down", "j":
				availHeight := m.height - 8
				if availHeight < 5 {
					availHeight = 5
				}
				maxScroll := len(strings.Split(m.diffContent, "\n")) - availHeight
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.diffScrollOffset < maxScroll {
					m.diffScrollOffset++
				}
			}
			return m, nil
		}

		// PR content generation in progress: swallow keys so they don't act
		// on the list hidden behind the "Generating…" screen (esc cancels).
		if m.prGenerating {
			if msg.String() == "esc" {
				m.prGenerating = false
				m.prWsName = ""
				m.prBranch = ""
				m.prBase = ""
			}
			return m, nil
		}

		// Delete confirmation mode
		if m.deleting {
			switch msg.String() {
			case "y", "Y":
				if m.deleteTarget != "" {
					target := m.deleteTarget
					m.deleting = false
					m.deleteTarget = ""
					m.markDeleting(target)
					return m, tea.Batch(m.deleteWorkspaceCmd(target), spinnerTickCmd())
				}
				// batch delete
				targets := make([]string, 0, len(m.selected))
				for name := range m.selected {
					targets = append(targets, name)
				}
				m.deleting = false
				m.deleteTarget = ""
				m.markDeleting(targets...)
				m.selected = make(map[string]bool)
				return m, tea.Batch(m.batchDeleteWorkspaceCmd(targets), spinnerTickCmd())
			case "n", "esc":
				m.deleting = false
				m.deleteTarget = ""
			}
			return m, nil
		}

		// PR creation dialog
		if m.prCreating {
			switch msg.String() {
			case "enter":
				val := m.input.Value()
				if m.prStep == 0 {
					m.prTitle = val
					m.prStep = 1
					m.input.Placeholder = "PR body (optional)"
					m.input.SetValue(m.prBodyPrefill)
					return m, textinput.Blink
				}
				// step 1: body confirmed
				wsName := m.prWsName
				title := m.prTitle
				body := val
				m.prCreating = false
				m.prStep = 0
				m.prTitle = ""
				m.prBodyPrefill = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				return m, m.createPRCmd(wsName, title, body)
			case "esc":
				m.prCreating = false
				m.prStep = 0
				m.prTitle = ""
				m.prBodyPrefill = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// Filter mode
		if m.filtering {
			switch msg.String() {
			case "esc", "enter":
				m.filtering = false
				m.cursor = 0
				return m, m.capturePreviewCmd()
			case "backspace":
				if len(m.filterQuery) > 0 {
					m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
				}
				m.cursor = 0
			default:
				if len(msg.String()) == 1 {
					m.filterQuery += msg.String()
					m.cursor = 0
				}
			}
			return m, nil
		}

		// Two-step workspace create / issue / remote branch mode
		if m.creating {
			// Remote branch mode: handle suggestion navigation before input
			if m.remoteBranchMode {
				switch msg.String() {
				case "up":
					if m.branchSuggestionCursor > 0 {
						m.branchSuggestionCursor--
					}
					return m, nil
				case "down":
					if m.branchSuggestionCursor < len(m.filteredBranches)-1 {
						m.branchSuggestionCursor++
					}
					return m, nil
				case "tab":
					if len(m.filteredBranches) > 0 {
						m.input.SetValue(m.filteredBranches[m.branchSuggestionCursor])
						m.filteredBranches = filterBranches(m.remoteBranches, m.input.Value())
						m.branchSuggestionCursor = 0
					}
					return m, nil
				case "enter":
					var branchName string
					typed := m.input.Value()
					if m.branchSuggestionCursor < len(m.filteredBranches) {
						branchName = m.filteredBranches[m.branchSuggestionCursor]
					} else {
						branchName = typed
					}
					// An exactly-typed branch name beats the highlighted
					// suggestion: typing "dev" and pressing enter must not
					// silently create "develop".
					for _, b := range m.remoteBranches {
						if b == typed {
							branchName = typed
							break
						}
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
				default:
					m.input, cmd = m.input.Update(msg)
					m.filteredBranches = filterBranches(m.remoteBranches, m.input.Value())
					m.branchSuggestionCursor = 0
					return m, cmd
				}
			}

			switch msg.String() {
			case "enter":
				val := m.input.Value()
				if val == "" {
					return m, nil
				}
				if m.issueMode {
					m.resetCreateMode()
					m.workspaceCreating = true
					m.workspaceCreatingName = "issue " + val
					return m, tea.Batch(m.createWorkspaceFromIssueCmd(val), spinnerTickCmd())
				}
				if m.createStep == 0 {
					if err := gitutil.ValidateBranchName(val); err != nil {
						m.err = err
						m.appendErrLog(err.Error())
						return m, m.scheduleErrClear()
					}
					m.newBranchName = val
					m.createStep = 1
					m.input.Placeholder = "Base branch"
					m.input.SetValue(m.cfg.Worktree.DefaultBase)
					return m, textinput.Blink
				}
				branchName := m.newBranchName
				baseBranch := val
				m.resetCreateMode()
				m.workspaceCreating = true
				m.workspaceCreatingName = branchName
				return m, tea.Batch(m.createWorkspaceCmd(branchName, baseBranch), spinnerTickCmd())
			case "esc":
				m.resetCreateMode()
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// Normal mode
		visible := m.visibleWorkspaces()
		switch {
		case msg.String() == "esc" && m.filterQuery != "":
			m.filterQuery = ""
			m.cursor = 0
			return m, m.capturePreviewCmd()
		case key.Matches(msg, m.keys.Quit):
			// Quitting mid create/delete would orphan a half-built workspace
			// (worktree and window exist, state entry not yet written).
			if m.workspaceCreating || m.workspaceDeleting {
				return m, m.transientErrCmd("an operation is in progress — ctrl+c to force quit")
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				return m, m.capturePreviewCmd()
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(visible)-1 {
				m.cursor++
				return m, m.capturePreviewCmd()
			}
		case key.Matches(msg, m.keys.New):
			m.creating = true
			m.createStep = 0
			m.input.Placeholder = "New branch name"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case key.Matches(msg, m.keys.Issue):
			m.creating = true
			m.issueMode = true
			m.input.Placeholder = "GitHub issue number"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case key.Matches(msg, m.keys.Remote):
			m.creating = true
			m.remoteBranchMode = true
			m.remoteBranches = nil
			m.filteredBranches = nil
			m.branchSuggestionCursor = 0
			m.input.Placeholder = "Remote branch name"
			m.input.SetValue("")
			m.input.Focus()
			return m, tea.Batch(textinput.Blink, m.loadRemoteBranchesCmd())
		case key.Matches(msg, m.keys.Enter):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				return m, m.attachWorkspaceCmd(ws.Name)
			}
		case key.Matches(msg, m.keys.Diff):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				return m, m.loadDiffCmd(ws)
			}
		case key.Matches(msg, m.keys.PR):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				m.prGenerating = true
				m.prWsName = ws.Name
				m.prBranch = ws.Branch
				m.prBase = ws.BaseBranch
				return m, m.generatePRContentCmd(ws)
			}
		case key.Matches(msg, m.keys.Open):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				if ws.PRURL != "" {
					return m, openURLCmd(ws.PRURL)
				}
				return m, m.transientErrCmd(fmt.Sprintf("no PR for %q — create one with 'p'", ws.Name))
			}
		case key.Matches(msg, m.keys.Review):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				if ws.PRURL != "" {
					return m, m.sendReviewsCmd(ws.Name)
				}
				return m, m.transientErrCmd(fmt.Sprintf("no PR for %q — create one first with 'p'", ws.Name))
			}
		case key.Matches(msg, m.keys.Select):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				if m.selected[ws.Name] {
					delete(m.selected, ws.Name)
				} else {
					m.selected[ws.Name] = true
				}
				// Advance cursor
				if m.cursor < len(visible)-1 {
					m.cursor++
				}
			}
		case key.Matches(msg, m.keys.Delete):
			if len(m.selected) > 0 {
				// batch delete confirmation
				m.deleting = true
				m.deleteTarget = ""
			} else if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
				}
				m.deleting = true
				m.deleteTarget = ws.Name
			}
		case key.Matches(msg, m.keys.Filter):
			m.filtering = true
			m.filterQuery = ""
			m.cursor = 0
		case key.Matches(msg, m.keys.Sort):
			m.sortMode = (m.sortMode + 1) % 4
			m.cursor = 0
		case key.Matches(msg, m.keys.Agent):
			m.agentSelecting = true
			m.agentCursor = 0
			// Position cursor on the currently active agent
			for i, a := range config.PredefinedAgents {
				if a.IsActive(m.cfg) {
					m.agentCursor = i
					break
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.ErrLog):
			m.showErrLog = !m.showErrLog
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		}

	case spinnerTickMsg:
		if m.workspaceCreating || m.workspaceDeleting {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTickCmd()
		}
		return m, nil

	case remoteBranchesLoadedMsg:
		// A stale load (user already esc'd out of remote-branch mode) must
		// not reset whatever dialog is open now.
		if !m.remoteBranchMode {
			return m, nil
		}
		if msg.err != nil {
			m.resetCreateMode()
			m.err = fmt.Errorf("failed to load remote branches: %w", msg.err)
			m.appendErrLog(m.err.Error())
			return m, m.scheduleErrClear()
		}
		m.remoteBranches = msg.branches
		m.filteredBranches = filterBranches(msg.branches, m.input.Value())
		m.branchSuggestionCursor = 0

	case loadedWorkspacesMsg:
		m.refreshing = false
		// Keep the cursor on the same workspace by name: refreshes can
		// reorder rows (activity changes, deletions), and a stale index
		// would point destructive keys at whatever moved under the cursor.
		prev := m.currentWorkspaceName()
		m.workspaces = msg.workspaces
		visible := m.visibleWorkspaces()
		if prev != "" {
			for i, ws := range visible {
				if ws.Name == prev {
					m.cursor = i
					break
				}
			}
		}
		if m.cursor >= len(visible) {
			m.cursor = max(0, len(visible)-1)
		}
		return m, m.capturePreviewCmd()

	case createdWorkspaceMsg:
		m.workspaceCreating = false
		m.workspaceCreatingName = ""
		if msg.wsName != "" {
			// A refresh that read state after AddWorkspace may already have
			// added the row; appending again would show it twice.
			exists := false
			for _, item := range m.workspaces {
				if item.Name == msg.wsName {
					exists = true
					break
				}
			}
			if !exists && m.stateStore != nil {
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
		// Clear only the finished deletes: another delete may still be in
		// flight, and a batch-confirm dialog may be open over m.selected.
		for _, name := range msg.names {
			delete(m.workspaceDeletingNames, name)
			delete(m.selected, name)
		}
		if len(m.workspaceDeletingNames) == 0 {
			m.workspaceDeleting = false
			m.workspaceDeletingName = ""
		}
		return m, m.loadWorkspacesCmd

	case capturePreviewMsg:
		// Drop captures for a workspace the cursor has since left.
		if msg.wsName == m.currentWorkspaceName() {
			m.agentPreview = msg.lines
		}

	case refreshTickMsg:
		next := tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} })
		// Don't stack another load while one is still running (huge repo,
		// cold disk): each load spawns git subprocesses per workspace.
		if m.refreshing {
			return m, next
		}
		m.refreshing = true
		return m, tea.Batch(m.loadWorkspacesCmd, next)

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
		// Only accept the generation we are waiting for; a stale result
		// (user cancelled, or pressed 'p' on another workspace) must not
		// open a dialog with the wrong workspace's content.
		if !m.prGenerating || msg.wsName != m.prWsName {
			return m, nil
		}
		m.prGenerating = false
		m.prCreating = true
		m.prStep = 0
		m.prBodyPrefill = msg.body
		m.input.Placeholder = "PR title"
		m.input.SetValue(msg.title)
		m.input.Focus()
		return m, textinput.Blink

	case prStatusTickMsg:
		cmds := []tea.Cmd{
			tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		}
		// If the previous round of gh/git calls hasn't finished (slow or
		// hanging network), don't pile another round on top of it.
		if m.statusChecksInFlight > 0 {
			return m, cmds[0]
		}
		for _, ws := range m.workspaces {
			// Skip workspaces that are fully done (merged PR and remote branch gone).
			if ws.PRStatus == "merged" && ws.RemoteDeleted {
				continue
			}
			cmds = append(cmds, m.checkBranchStatusCmd(ws.Name, ws.Branch, ws.WorktreeDir, ws.BranchPushed))
		}
		m.statusChecksInFlight = len(cmds) - 1 // minus the re-armed tick
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
		m.statusChecksInFlight = max(0, m.statusChecksInFlight-1)
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

	case statusCheckErrMsg:
		m.statusChecksInFlight = max(0, m.statusChecksInFlight-1)
		// Background status polls fail as a group (auth expired, offline, ...);
		// log without the transient banner so a 30s tick can't flash N errors.
		m.appendErrLog(fmt.Sprintf("PR status check: %v", msg.err))

	case attachFinishedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.appendErrLog(msg.err.Error())
			return m, m.scheduleErrClear()
		}
		return m, m.loadWorkspacesCmd

	case errMsg:
		m.workspaceCreating = false
		m.workspaceDeleting = false
		m.workspaceDeletingName = ""
		m.workspaceDeletingNames = make(map[string]bool)
		m.resetCreateMode()
		m.filtering = false
		m.prGenerating = false
		m.prCreating = false
		m.err = msg.err
		m.appendErrLog(msg.err.Error())
		return m, m.scheduleErrClear()

	case diffLoadedMsg:
		// Don't pop the diff overlay over an open dialog (delete confirm,
		// create, PR): its keys would land in the hidden dialog.
		if m.deleting || m.creating || m.prCreating || m.agentSelecting {
			return m, nil
		}
		m.diffViewing = true
		m.diffContent = msg.content
		m.diffScrollOffset = 0
		m.diffWsName = msg.wsName

	case clearErrorMsg:
		if msg.seq == m.errSeq {
			m.err = nil
		}

	case reviewsSentMsg:
		if msg.count == 0 {
			return m, m.transientErrCmd(fmt.Sprintf("no review comments found for %q", msg.wsName))
		}
		m.notice = fmt.Sprintf("sent %d review comment(s) to %s", msg.count, msg.wsName)
		m.noticeSeq++
		seq := m.noticeSeq
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearNoticeMsg{seq: seq}
		})

	case clearNoticeMsg:
		if msg.seq == m.noticeSeq {
			m.notice = ""
		}
	}

	return m, cmd
}

func (m *Model) clampDiffScroll() {
	lines := len(strings.Split(m.diffContent, "\n"))
	availHeight := m.height - 8
	if availHeight < 5 {
		availHeight = 5
	}
	maxScroll := lines - availHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.diffScrollOffset > maxScroll {
		m.diffScrollOffset = maxScroll
	}
}

func (m *Model) resetCreateMode() {
	m.creating = false
	m.remoteBranchMode = false
	m.remoteBranches = nil
	m.filteredBranches = nil
	m.branchSuggestionCursor = 0
	m.issueMode = false
	m.createStep = 0
	m.newBranchName = ""
	m.input.SetValue("")
	m.input.Placeholder = "New branch name"
}

// scheduleErrClear arms the 3s auto-clear for the current error banner.
// The sequence number ensures an older banner's timer can't wipe a newer one.
func (m *Model) scheduleErrClear() tea.Cmd {
	m.errSeq++
	seq := m.errSeq
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearErrorMsg{seq: seq}
	})
}

func (m *Model) transientErrCmd(msg string) tea.Cmd {
	m.err = fmt.Errorf("%s", msg)
	m.appendErrLog(msg)
	return m.scheduleErrClear()
}

func (m *Model) appendErrLog(msg string) {
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s", ts, msg)
	if n := len(m.errLog); n > 0 && strings.HasSuffix(m.errLog[n-1], "] "+msg) {
		m.errLog[n-1] = entry // refresh timestamp instead of flooding the log
		return
	}
	m.errLog = append(m.errLog, entry)
	if len(m.errLog) > 20 {
		m.errLog = m.errLog[len(m.errLog)-20:]
	}
}
