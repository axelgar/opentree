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

// maxScrollbackOffset caps how far back the user can scroll in the terminal
// pane's scrollback buffer. This prevents the offset counter from growing
// unbounded when the user holds the scroll-up key.
const maxScrollbackOffset = 5000

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m Model) isWorkspaceInFlight(name string) bool {
	return m.workspaceDeletingName == name || m.workspaceDeletingNames[name]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = m.leftPaneWidth()
		if m.diffViewing {
			m.clampDiffScroll()
		}
		m.resizeActiveTerminal()

	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			if m.diffViewing {
				if m.diffScrollOffset > 0 {
					m.diffScrollOffset--
				}
			} else {
				// Scroll up = go back in history = increase offset
				m.termScrollOffset += 3
				// Cap at scrollback buffer size to prevent unbounded growth.
				if m.termScrollOffset > maxScrollbackOffset {
					m.termScrollOffset = maxScrollbackOffset
				}
			}
		case tea.MouseWheelDown:
			if m.diffViewing {
				if m.diffScrollOffset < m.maxDiffScroll() {
					m.diffScrollOffset++
				}
			} else {
				// Scroll down = go toward live view = decrease offset
				m.termScrollOffset -= 3
				if m.termScrollOffset < 0 {
					m.termScrollOffset = 0
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		// Terminal-focused mode: forward keys to the agent PTY
		if m.terminalFocused {
			// Ctrl+] exits terminal focus. We avoid Esc because terminal apps
			// (vim, fzf, etc.) use it heavily.
			if msg.String() == "ctrl+]" {
				m.terminalFocused = false
				return m, nil
			}
			if m.termPM != nil {
				visible := m.visibleWorkspaces()
				if m.cursor < len(visible) {
					ws := visible[m.cursor]
					// If the process has exited, Enter restarts the agent.
					if !m.terminalRunning {
						if msg.String() == "enter" {
							agentCmd := ws.Agent
							if agentCmd == "" {
								agentCmd = m.cfg.Agent.Command
							}
							if agentCmd == "" {
								return m, m.transientErrCmd("no agent configured — press 'A' to choose one")
							}
							rightCols, rightRows := m.rightPaneSize()
							if err := m.termPM.CreateWindowSized(ws.Name, ws.WorktreeDir, agentCmd, rightCols, rightRows, m.cfg.Agent.Args...); err != nil {
								return m, m.transientErrCmd(fmt.Sprintf("failed to restart terminal for %q: %v", ws.Name, err))
							}
							m.terminalRunning = true
							m.termScrollOffset = 0
							return m, terminalTickCmd()
						}
						// Block all other keys from going to the dead PTY.
						return m, nil
					}
					// Process is running: forward keypress.
					data := keyToBytes(msg)
					if len(data) > 0 {
						_ = m.termPM.WriteInput(ws.Name, data)
						m.termScrollOffset = 0 // snap to live view on input
					}
				}
			}
			return m, nil
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
				cfgPath := config.FindConfigFile()
				_ = config.Save(m.cfg, cfgPath)
				m.agentSelecting = false
			case "esc", "q":
				m.agentSelecting = false
			}
			return m, nil
		}

		// Config overlay mode
		if m.showConfig {
			switch msg.String() {
			case "esc", "c", "q":
				m.showConfig = false
			}
			return m, nil
		}

		// Diff view mode
		if m.diffViewing {
			switch msg.String() {
			case "esc", "q":
				m.diffViewing = false
				m.diffContent = ""
				m.diffLines = nil
				m.diffScrollOffset = 0
				m.diffWsName = ""
			case "up", "k":
				if m.diffScrollOffset > 0 {
					m.diffScrollOffset--
				}
			case "down", "j":
				if m.diffScrollOffset < m.maxDiffScroll() {
					m.diffScrollOffset++
				}
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
					m.workspaceDeleting = true
					m.workspaceDeletingName = target
					return m, tea.Batch(m.deleteWorkspaceCmd(target), spinnerTickCmd())
				}
				// batch delete
				targets := make([]string, 0, len(m.selected))
				for name := range m.selected {
					targets = append(targets, name)
				}
				m.deleting = false
				m.deleteTarget = ""
				m.workspaceDeletingNames = make(map[string]bool)
				for _, name := range targets {
					m.workspaceDeletingNames[name] = true
				}
				m.selected = make(map[string]bool)
				m.workspaceDeleting = true
				m.workspaceDeletingName = fmt.Sprintf("%d workspaces", len(targets))
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
				return m, nil
			case "backspace":
				if len(m.filterQuery) > 0 {
					m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
				}
				m.cursor = 0
				m.invalidateViewCache()
			default:
				if len(msg.String()) == 1 {
					m.filterQuery += msg.String()
					m.cursor = 0
					m.invalidateViewCache()
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
					if m.branchSuggestionCursor < len(m.filteredBranches) {
						branchName = m.filteredBranches[m.branchSuggestionCursor]
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
						return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
							return clearErrorMsg{}
						})
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
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.termScrollOffset = 0
				m.updateTerminalRunning()
				m.resizeActiveTerminal()
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(visible)-1 {
				m.cursor++
				m.termScrollOffset = 0
				m.updateTerminalRunning()
				m.resizeActiveTerminal()
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
				m.terminalFocused = true
				if m.termPM != nil {
					// Lazily start the PTY window if it doesn't exist yet.
					if !m.termPM.IsRunning(ws.Name) {
						agentCmd := ws.Agent
						if agentCmd == "" {
							agentCmd = m.cfg.Agent.Command
						}
						if agentCmd == "" {
							m.terminalFocused = false
							return m, m.transientErrCmd("no agent configured — press 'a' to choose one")
						}
						rightCols, rightRows := m.rightPaneSize()
						if err := m.termPM.CreateWindowSized(ws.Name, ws.WorktreeDir, agentCmd, rightCols, rightRows, m.cfg.Agent.Args...); err != nil {
							m.terminalFocused = false
							return m, m.transientErrCmd(fmt.Sprintf("failed to start terminal for %q: %v", ws.Name, err))
						}
						m.terminalRunning = true
					} else {
						m.terminalRunning = true
						rightCols, rightRows := m.rightPaneSize()
						_ = m.termPM.ResizeWindow(ws.Name, rightCols, rightRows)
					}
				}
				return m, terminalTickCmd()
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
			m.invalidateViewCache()
		case key.Matches(msg, m.keys.Sort):
			m.sortMode = (m.sortMode + 1) % 4
			m.cursor = 0
			m.invalidateViewCache()
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
		case key.Matches(msg, m.keys.Config):
			m.showConfig = true
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
		if msg.err != nil {
			m.resetCreateMode()
			m.err = fmt.Errorf("failed to load remote branches: %w", msg.err)
			m.appendErrLog(m.err.Error())
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearErrorMsg{}
			})
		}
		m.remoteBranches = msg.branches
		m.filteredBranches = filterBranches(msg.branches, m.input.Value())
		m.branchSuggestionCursor = 0

	case terminalTickMsg:
		wsName := m.activeWorkspaceName()
		if m.termPM != nil && wsName != "" {
			m.terminalRunning = m.termPM.IsRunning(wsName)
		} else {
			m.terminalRunning = false
		}
		if m.terminalFocused {
			return m, terminalTickCmd()
		}
		// Only keep ticking if a workspace is selected and terminal is running.
		if wsName != "" && m.terminalRunning {
			return m, terminalTickSlowCmd()
		}
		return m, nil

	case loadedWorkspacesMsg:
		m.workspaces = msg.workspaces
		m.invalidateViewCache()
		visible := m.visibleWorkspaces()
		if m.cursor >= len(visible) {
			m.cursor = max(0, len(visible)-1)
		}
		// Start a terminal tick so the right pane renders live output
		// for the active workspace even before the user presses Enter.
		if m.termPM != nil && len(visible) > 0 {
			return m, terminalTickSlowCmd()
		}

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
					m.invalidateViewCache()
				}
			}
			return m, m.checkBranchStatusCmd(msg.wsName, msg.branch, msg.worktreeDir, false)
		}
		return m, nil

	case deletedWorkspaceMsg:
		m.workspaceDeleting = false
		m.workspaceDeletingName = ""
		m.workspaceDeletingNames = make(map[string]bool)
		m.selected = make(map[string]bool)
		m.terminalFocused = false
		return m, m.loadWorkspacesCmd

	case refreshTickMsg:
		return m, tea.Batch(
			m.loadWorkspacesCmd,
			tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
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
		// Collect workspaces needing a status check.
		var toCheck []WorkspaceItem
		for _, ws := range m.workspaces {
			if ws.PRStatus == "merged" && ws.RemoteDeleted {
				continue
			}
			toCheck = append(toCheck, ws)
		}
		// Bound concurrency: check at most 3 workspaces per tick, rotating
		// through the list across ticks via m.prCheckOffset.
		const maxPerTick = 3
		if len(toCheck) > maxPerTick {
			start := m.prCheckOffset % len(toCheck)
			var batch []WorkspaceItem
			for i := 0; i < maxPerTick; i++ {
				batch = append(batch, toCheck[(start+i)%len(toCheck)])
			}
			m.prCheckOffset = start + maxPerTick
			toCheck = batch
		}
		for _, ws := range toCheck {
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
				m.invalidateViewCache()
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
				m.invalidateViewCache()
				break
			}
		}
		if msg.status.CIStatus != "" {
			if m.ciStatus == nil {
				m.ciStatus = make(map[string]string)
			}
			m.ciStatus[msg.wsName] = msg.status.CIStatus
		}

	case errMsg:
		m.workspaceCreating = false
		m.workspaceDeleting = false
		m.workspaceDeletingNames = make(map[string]bool)
		m.creating = false
		m.filtering = false
		m.prCreating = false
		m.err = msg.err
		m.appendErrLog(msg.err.Error())
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearErrorMsg{}
		})

	case diffLoadedMsg:
		m.diffViewing = true
		m.diffContent = msg.content
		m.diffLines = strings.Split(msg.content, "\n")
		m.diffScrollOffset = 0
		m.diffWsName = msg.wsName

	case clearErrorMsg:
		m.err = nil

	case reviewsSentMsg:
		if msg.count == 0 {
			m.err = fmt.Errorf("no review comments found for %q", msg.wsName)
			m.appendErrLog(m.err.Error())
		}
	}

	return m, cmd
}

func (m *Model) clampDiffScroll() {
	if m.diffScrollOffset > m.maxDiffScroll() {
		m.diffScrollOffset = m.maxDiffScroll()
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

func (m Model) activeWorkspaceName() string {
	visible := m.visibleWorkspaces()
	if m.cursor < len(visible) {
		return visible[m.cursor].Name
	}
	return ""
}

func (m *Model) transientErrCmd(msg string) tea.Cmd {
	m.err = fmt.Errorf("%s", msg)
	m.appendErrLog(msg)
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearErrorMsg{}
	})
}

// invalidateViewCache marks the sorted/filtered workspace cache as stale
// and recomputes it. Call this whenever m.workspaces, m.sortMode, or
// m.filterQuery change.
func (m *Model) invalidateViewCache() {
	m.viewCacheDirty = true
	// Eagerly recompute so subsequent reads in the same Update cycle
	// (e.g. cursor clamping) see consistent data.
	sorted := m.sortedWorkspaces()
	if m.filterQuery == "" {
		m.cachedVisible = sorted
	} else {
		q := strings.ToLower(m.filterQuery)
		var out []WorkspaceItem
		for _, ws := range sorted {
			if strings.Contains(strings.ToLower(ws.Name), q) {
				out = append(out, ws)
			}
		}
		m.cachedVisible = out
	}
	m.cachedSorted = sorted
	m.viewCacheDirty = false
}

func (m *Model) appendErrLog(msg string) {
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s", ts, msg)
	m.errLog = append(m.errLog, entry)
	if len(m.errLog) > 20 {
		m.errLog = m.errLog[len(m.errLog)-20:]
	}
}

// leftPaneWidth returns the width of the left (dashboard) pane.
func (m Model) leftPaneWidth() int {
	w := m.width * 35 / 100
	if w < 30 {
		w = 30
	}
	if w > 60 {
		w = 60
	}
	return w
}

// rightPaneSize returns (cols, rows) for the right (terminal) pane.
func (m Model) rightPaneSize() (int, int) {
	left := m.leftPaneWidth()
	cols := m.width - left - 5 // borders + separator
	if cols < 20 {
		cols = 20
	}
	rows := m.height - 4 // header + footer
	if rows < 5 {
		rows = 5
	}
	return cols, rows
}

// updateTerminalRunning checks the actual PTY state for the active workspace.
func (m *Model) updateTerminalRunning() {
	if m.termPM != nil {
		m.terminalRunning = m.termPM.IsRunning(m.activeWorkspaceName())
	} else {
		m.terminalRunning = false
	}
}

// maxDiffScroll returns the maximum valid scroll offset for the current diff.
func (m Model) maxDiffScroll() int {
	availHeight := m.height - 8
	if availHeight < 5 {
		availHeight = 5
	}
	lineCount := len(m.diffLines)
	if lineCount == 0 && m.diffContent != "" {
		lineCount = len(strings.Split(m.diffContent, "\n"))
	}
	maxScroll := lineCount - availHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	return maxScroll
}

// resizeActiveTerminal resizes the currently selected workspace's PTY window
// to the current right pane dimensions.
func (m *Model) resizeActiveTerminal() {
	if m.termPM == nil {
		return
	}
	name := m.activeWorkspaceName()
	if name == "" {
		return
	}
	cols, rows := m.rightPaneSize()
	_ = m.termPM.ResizeWindow(name, cols, rows)
}

