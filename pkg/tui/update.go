package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
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

	case tea.MouseMsg:
		return m.handleWheel(msg)

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

		// The Skills tab owns its keys while it has focus. None of the
		// workspace dialogs can be open behind it — there is no way to reach
		// one from here — so this sits above them rather than after.
		if m.tab == tabSkills {
			return m.updateSkills(msg)
		}

		// Confirming an adapter download before switching agent
		if m.agentInstallConfirm != nil {
			agent := *m.agentInstallConfirm
			switch msg.String() {
			case "y", "Y", "enter":
				m.agentInstallConfirm = nil
				m.agentPendingSelect = &agent
				m.agentSelecting = false
				return m, m.installAdapterCmd(agent)
			case "n", "esc", "q":
				m.agentInstallConfirm = nil
			}
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
				// Enter means "use this agent", so it does whatever that takes
				// — including fetching an adapter — rather than recording a
				// choice that cannot start.
				agent := agents[m.agentCursor]
				switch status, _ := m.readiness(agent); status {
				case agentNotFound:
					return m, m.transientErrCmd(fmt.Sprintf(
						"%s is not installed — install %s first", agent.Name, agent.Command))
				case agentAdapterMissing:
					// A download this size is asked about, not sprung.
					m.agentInstallConfirm = &agents[m.agentCursor]
					return m, nil
				}
				m.agentSelecting = false
				if errMsg := m.selectAgent(agent); errMsg != "" {
					return m, m.transientErrCmd(errMsg)
				}
			case "i":
				// Installing without switching to it, for preparing ahead.
				agent := agents[m.agentCursor]
				if len(agent.ACPInstallCommand()) == 0 {
					return m, m.transientErrCmd(fmt.Sprintf("%s needs no adapter", agent.Name))
				}
				if agent.ACPInstalled() {
					return m, m.transientErrCmd(fmt.Sprintf("%s is already installed", agent.ACPCommand()))
				}
				m.agentSelecting = false
				return m, m.installAdapterCmd(agent)
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
				if m.diffScrollOffset < m.maxDiffScroll() {
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
				return m, nil
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

		// Answering a chat agent's permission prompt
		if m.answering {
			return m.handleAnswerKey(msg)
		}

		// Sending a prompt to a chat agent without attaching
		if m.prompting {
			switch msg.String() {
			case "enter":
				text := strings.TrimSpace(m.input.Value())
				wsName := m.promptWs
				m.prompting, m.promptWs = false, ""
				m.input.Reset()
				if text == "" {
					return m, nil
				}
				return m, m.sendAgentCommand(wsName, "sent", chat.Command{
					Type: chat.CommandPrompt, Text: text,
				})
			case "esc":
				m.prompting, m.promptWs = false, ""
				m.input.Reset()
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// Normal mode
		visible := m.visibleWorkspaces()
		switch {
		case key.Matches(msg, m.keys.Tab):
			// Rescan on the way in: skills are edited by hand and by other
			// agents, so the tab shows what is on disk now rather than what was
			// there when opentree started.
			m.tab = tabSkills
			return m, m.scanSkillsCmd
		case msg.String() == "esc" && m.filterQuery != "":
			m.filterQuery = ""
			m.cursor = 0
			return m, nil
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
				return m, nil
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(visible)-1 {
				m.cursor++
				return m, nil
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
				if reason := ws.chatUnavailable(); reason != "" {
					return m, m.transientErrCmd(reason)
				}
				if ws.PRURL != "" {
					return m, m.sendReviewsCmd(ws)
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
			// The reshuffle alone is ambiguous about what changed; name it.
			return m, m.noticeCmd("sorted by " + sortModeNames[m.sortMode])
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
		case key.Matches(msg, m.keys.Answer):
			if len(visible) == 0 {
				return m, nil
			}
			ws := visible[m.cursor]
			if reason := ws.chatUnavailable(); reason != "" {
				return m, m.transientErrCmd(reason)
			}
			if ws.pendingPermission() == nil {
				return m, m.transientErrCmd(fmt.Sprintf("%s is not waiting on a permission", ws.Name))
			}
			return m.openAnswerDialog(ws), nil

		case key.Matches(msg, m.keys.Stop):
			if len(visible) == 0 {
				return m, nil
			}
			ws := visible[m.cursor]
			if reason := ws.chatUnavailable(); reason != "" {
				return m, m.transientErrCmd(reason)
			}
			if !ws.chatWorking() {
				return m, m.transientErrCmd(fmt.Sprintf("%s's agent is not working", ws.Name))
			}
			return m, m.sendAgentCommand(ws.Name, "interrupted",
				chat.Command{Type: chat.CommandInterrupt})

		case key.Matches(msg, m.keys.Msg):
			if len(visible) == 0 {
				return m, nil
			}
			if reason := visible[m.cursor].chatUnavailable(); reason != "" {
				return m, m.transientErrCmd(reason)
			}
			m.prompting = true
			m.promptWs = visible[m.cursor].Name
			m.input.Reset()
			m.input.Placeholder = "Message the agent"
			m.input.Focus()
			return m, textinput.Blink

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
		return m, nil

	case createdWorkspaceMsg:
		m.workspaceCreating = false
		m.workspaceCreatingName = ""
		if msg.wsName != "" {
			// A refresh that read state after AddWorkspace may already have
			// added the row; appending again would show it twice.
			exists := m.workspaceIndex(msg.wsName) >= 0
			if !exists && m.stateStore != nil {
				if ws, err := m.stateStore.GetWorkspace(msg.wsName); err == nil && ws != nil {
					item := WorkspaceItem{
						Workspace: ws,
						DiffStat:  "No changes",
					}
					m.workspaces = append(m.workspaces, item)
				}
			}
			// Creating a workspace means wanting to work in it: go straight
			// to the chat. Quitting the chat drops back to the list.
			return m, tea.Batch(
				m.checkBranchStatusCmd(msg.wsName, msg.branch, msg.worktreeDir, false),
				m.attachWorkspaceCmd(msg.wsName),
			)
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
		notice := fmt.Sprintf("deleted %s", strings.Join(msg.names, ", "))
		if len(msg.names) > 1 {
			notice = fmt.Sprintf("deleted %d workspaces", len(msg.names))
		}
		return m, tea.Batch(m.loadWorkspacesCmd, m.noticeCmd(notice))

	case refreshTickMsg:
		next := tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} })
		// Don't stack another load while one is still running (huge repo,
		// cold disk): each load spawns git subprocesses per workspace.
		if m.refreshing {
			return m, next
		}
		m.refreshing = true
		return m, tea.Batch(m.loadWorkspacesCmd, next)

	case prCreatedMsg:
		ws, err := m.stateStore.GetWorkspace(msg.wsName)
		if err == nil {
			ws.PRURL = msg.prURL
			ws.PRStatus = "open"
			_ = m.stateStore.UpdateWorkspace(ws)
		}
		var branch, worktreeDir string
		var wasPushed bool
		if i := m.workspaceIndex(msg.wsName); i >= 0 {
			branch = m.workspaces[i].Branch
			worktreeDir = m.workspaces[i].WorktreeDir
			wasPushed = m.workspaces[i].BranchPushed
		}
		// The URL is the deliverable of the whole flow — show it, and name the
		// key that opens it, rather than the dialog just closing.
		return m, tea.Batch(
			m.loadWorkspacesCmd,
			m.checkBranchStatusCmd(msg.wsName, branch, worktreeDir, wasPushed),
			m.noticeCmd(fmt.Sprintf("PR created: %s — o to open", truncate(msg.prURL, 60))),
		)

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
		if i := m.workspaceIndex(msg.wsName); i >= 0 {
			m.workspaces[i].PRURL = msg.prURL
			m.workspaces[i].PRStatus = msg.prStatus
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
		if i := m.workspaceIndex(msg.wsName); i >= 0 {
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
			return m, afterExec(m.scheduleErrClear())
		}
		return m, afterExec(m.loadWorkspacesCmd)

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

	case adapterInstalledMsg:
		pending := m.agentPendingSelect
		m.agentPendingSelect = nil
		if msg.err != nil {
			return m, afterExec(m.transientErrCmd(fmt.Sprintf("failed to install %s: %v", msg.adapter, msg.err)))
		}
		m.notice = fmt.Sprintf("installed %s", msg.adapter)
		// Enter meant "use this agent"; the install was only what stood in the
		// way, so finish the job.
		if pending != nil {
			if errMsg := m.selectAgent(*pending); errMsg != "" {
				return m, afterExec(m.transientErrCmd(errMsg))
			}
			m.notice += ", now using " + pending.Name
		}
		m.noticeSeq++
		seq := m.noticeSeq
		return m, afterExec(tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearNoticeMsg{seq: seq}
		}))

	case agentCommandSentMsg:
		m.notice = fmt.Sprintf("%s: %s", msg.wsName, msg.action)
		m.noticeSeq++
		seq := m.noticeSeq
		// Refresh straight away so the badge reflects the answer rather than
		// waiting out the poll interval.
		return m, tea.Batch(m.loadWorkspacesCmd, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearNoticeMsg{seq: seq}
		}))

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

	case skillsScannedMsg:
		m.skills = msg.skills
		// The list may have shrunk under the cursor — a deleted skill, or a
		// filter that no longer matches.
		if m.skillCursor >= len(m.visibleSkills()) {
			m.skillCursor = max(len(m.visibleSkills())-1, 0)
		}

	case skillEditedMsg:
		if msg.err != nil {
			return m, m.transientErrCmd("editor: " + msg.err.Error())
		}
		// The frontmatter is what may have changed, so the row is re-read.
		return m, m.scanSkillsCmd

	case skillsRelinkedMsg:
		var done []string
		if len(msg.bridged) > 0 {
			done = append(done, "created "+strings.Join(msg.bridged, ", "))
		}
		if msg.count > 0 {
			done = append(done, "linked skills into "+plural(msg.count, "workspace"))
		}
		if len(done) == 0 {
			return m, m.transientErrCmd("every agent and workspace already sees the repo's skills")
		}
		// Rescanned as well as reloaded: a bridge is a new tree, and the agent
		// reading it is a mark the rows do not carry yet.
		return m, tea.Batch(m.loadWorkspacesCmd, m.scanSkillsCmd,
			m.noticeCmd(strings.Join(done, " • ")))

	case skillClonedMsg:
		if msg.err != nil {
			return m, m.transientErrCmd(msg.err.Error())
		}
		return m, tea.Batch(m.scanSkillsCmd, m.noticeCmd("cloned into "+msg.name))

	case skillProbedMsg:
		m.skillProbing = false
		if msg.err != nil {
			return m, m.transientErrCmd(msg.agent + ": " + msg.err.Error())
		}
		m.skillProbe, m.skillProbed = msg.commands, msg.agent
		return m, m.noticeCmd(fmt.Sprintf("%s advertises %s",
			msg.agent, plural(len(msg.commands), "command")))

	case clearNoticeMsg:
		if msg.seq == m.noticeSeq {
			m.notice = ""
		}

	case browserOpenedMsg:
		return m, m.noticeCmd("opened " + truncate(msg.url, 60) + " in browser")
	}

	return m, cmd
}

// afterExec wraps whatever a tea.ExecProcess callback wants to do next with the
// one thing every such callback must do. ExecProcess's RestoreTerminal re-enters
// the alt screen but drops mouse mode, and mouse mode is what keeps the wheel
// inside the app instead of scrolling the terminal's own scrollback. Getting
// this wrong is invisible until someone scrolls, so it is a helper rather than a
// rule to remember at each of the handler's return paths.
func afterExec(cmds ...tea.Cmd) tea.Cmd {
	return tea.Batch(append([]tea.Cmd{tea.EnableMouseCellMotion}, cmds...)...)
}

// wheelLines is how far one notch moves a body of text. Matches the viewport's
// own default so the chat and the diff scroll at the same rate.
const wheelLines = 3

// busyWithDialog reports whether something in front of the list owns the
// keyboard. The wheel stays out of those: the cursor it would move is not
// visible, so the change would only be discovered later as a surprise.
func (m Model) busyWithDialog() bool {
	return m.creating || m.deleting || m.filtering || m.prCreating || m.prGenerating ||
		m.agentSelecting || m.agentInstallConfirm != nil || m.answering || m.prompting ||
		m.showErrLog
}

// handleWheel drives whatever is scrollable underneath the pointer. The mouse
// is captured so the terminal stops scrolling its own scrollback out from under
// the alt screen; having taken it, the wheel owes the user a response, or the
// dashboard reads as frozen rather than as focused.
//
// Only the wheel is acted on. Clicks and motion arrive too — cell motion is the
// mode that suppresses the terminal's scrollback — and the list has nothing to
// do with them.
func (m Model) handleWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var delta int
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		delta = -1
	case tea.MouseButtonWheelDown:
		delta = 1
	default:
		return m, nil
	}

	if m.diffViewing {
		m.diffScrollOffset = min(max(m.diffScrollOffset+delta*wheelLines, 0), m.maxDiffScroll())
		return m, nil
	}
	if m.busyWithDialog() {
		return m, nil
	}

	// Anywhere else the wheel walks the selection, which is the only thing the
	// list has to move.
	next := min(max(m.cursor+delta, 0), max(len(m.visibleWorkspaces())-1, 0))
	if next == m.cursor {
		return m, nil
	}
	m.cursor = next
	return m, nil
}

// maxDiffScroll is the furthest the diff can scroll before the last line is on
// screen. Shared so the keys, the wheel and the resize clamp cannot disagree.
func (m Model) maxDiffScroll() int {
	availHeight := max(m.height-8, 5)
	return max(len(strings.Split(m.diffContent, "\n"))-availHeight, 0)
}

func (m *Model) clampDiffScroll() {
	if maxScroll := m.maxDiffScroll(); m.diffScrollOffset > maxScroll {
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

// noticeCmd raises a success banner and arms its 3s auto-clear. The sequence
// number is what stops an older banner's timer from wiping a newer one.
func (m *Model) noticeCmd(msg string) tea.Cmd {
	m.notice = msg
	m.noticeSeq++
	seq := m.noticeSeq
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearNoticeMsg{seq: seq}
	})
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
