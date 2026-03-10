package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/config"
)

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
		m.help.Width = msg.Width

	case tea.KeyMsg:
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
				m.diffScrollOffset++
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
					if m.branchSuggestionCursor < len(m.filteredBranches) {
						branchName = m.filteredBranches[m.branchSuggestionCursor]
					} else {
						branchName = m.input.Value()
					}
					if branchName == "" {
						return m, nil
					}
					m.creating = false
					m.remoteBranchMode = false
					m.remoteBranches = nil
					m.filteredBranches = nil
					m.branchSuggestionCursor = 0
					m.input.SetValue("")
					m.input.Placeholder = "New branch name"
					m.workspaceCreating = true
					m.workspaceCreatingName = branchName
					return m, tea.Batch(m.createWorkspaceFromRemoteCmd(branchName), spinnerTickCmd())
				case "esc":
					m.creating = false
					m.remoteBranchMode = false
					m.remoteBranches = nil
					m.filteredBranches = nil
					m.branchSuggestionCursor = 0
					m.input.SetValue("")
					m.input.Placeholder = "New branch name"
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
					m.creating = false
					m.issueMode = false
					m.input.SetValue("")
					m.input.Placeholder = "New branch name"
					m.workspaceCreating = true
					m.workspaceCreatingName = "issue " + val
					return m, tea.Batch(m.createWorkspaceFromIssueCmd(val), spinnerTickCmd())
				}
				if m.createStep == 0 {
					m.newBranchName = val
					m.createStep = 1
					m.input.Placeholder = "Base branch"
					m.input.SetValue(m.cfg.Worktree.DefaultBase)
					return m, textinput.Blink
				}
				branchName := m.newBranchName
				baseBranch := val
				m.creating = false
				m.createStep = 0
				m.newBranchName = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				m.workspaceCreating = true
				m.workspaceCreatingName = branchName
				return m, tea.Batch(m.createWorkspaceCmd(branchName, baseBranch), spinnerTickCmd())
			case "esc":
				m.creating = false
				m.issueMode = false
				m.createStep = 0
				m.newBranchName = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
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
					return m, nil
				}
				return m, m.attachWorkspaceCmd(ws.Name)
			}
		case key.Matches(msg, m.keys.Diff):
			if len(visible) > 0 {
				if m.isWorkspaceInFlight(visible[m.cursor].Name) {
					return m, nil
				}
				return m, m.loadDiffCmd(visible[m.cursor])
			}
		case key.Matches(msg, m.keys.PR):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, nil
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
					return m, nil
				}
				if ws.PRURL != "" {
					return m, openURLCmd(ws.PRURL)
				}
			}
		case key.Matches(msg, m.keys.Select):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.isWorkspaceInFlight(ws.Name) {
					return m, nil
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
					return m, nil
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
		m.remoteBranches = msg.branches
		m.filteredBranches = filterBranches(msg.branches, m.input.Value())
		m.branchSuggestionCursor = 0

	case loadedWorkspacesMsg:
		m.workspaces = msg.workspaces
		visible := m.visibleWorkspaces()
		if m.cursor >= len(visible) {
			m.cursor = max(0, len(visible)-1)
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
		m.selected = make(map[string]bool)
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
			ws.BranchPushed = msg.status.Pushed
			ws.RemoteDeleted = msg.status.RemoteDeleted
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
				m.workspaces[i].BranchPushed = msg.status.Pushed
				m.workspaces[i].RemoteDeleted = msg.status.RemoteDeleted
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
		m.workspaceCreating = false
		m.workspaceDeleting = false
		m.workspaceDeletingNames = make(map[string]bool)
		m.err = msg.err
		m.appendErrLog(msg.err.Error())
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearErrorMsg{}
		})

	case diffLoadedMsg:
		m.diffViewing = true
		m.diffContent = msg.content
		m.diffScrollOffset = 0
		m.diffWsName = msg.wsName

	case clearErrorMsg:
		m.err = nil
	}

	return m, cmd
}

func (m *Model) appendErrLog(msg string) {
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s", ts, msg)
	m.errLog = append(m.errLog, entry)
	if len(m.errLog) > 20 {
		m.errLog = m.errLog[len(m.errLog)-20:]
	}
}
