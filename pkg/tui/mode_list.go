package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
)

const (
	defaultPreviewWidth = 60
	minPreviewWidth     = 20
)

// updateList handles both the filter sub-state and the normal list keys.
// Filter is a sub-state of ModeList — the list renders underneath the prompt.
func updateList(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.list.filtering {
		switch msg.String() {
		case "esc", "enter":
			m.list.filtering = false
			m.list.cursor = 0
			return m, m.capturePreviewCmd()
		case "backspace":
			if len(m.list.filterQuery) > 0 {
				m.list.filterQuery = m.list.filterQuery[:len(m.list.filterQuery)-1]
			}
			m.list.cursor = 0
		default:
			if len(msg.String()) == 1 {
				m.list.filterQuery += msg.String()
				m.list.cursor = 0
			}
		}
		return m, nil
	}

	visible := m.visibleWorkspaces()
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up):
		if m.list.cursor > 0 {
			m.list.cursor--
			return m, m.capturePreviewCmd()
		}
	case key.Matches(msg, m.keys.Down):
		if m.list.cursor < len(visible)-1 {
			m.list.cursor++
			return m, m.capturePreviewCmd()
		}
	case key.Matches(msg, m.keys.New):
		m.mode = ModeCreate
		m.create.step = 0
		m.focusInput("New branch name", "")
		return m, textinput.Blink
	case key.Matches(msg, m.keys.Issue):
		m.mode = ModeCreateFromIssue
		m.focusInput("GitHub issue number", "")
		return m, textinput.Blink
	case key.Matches(msg, m.keys.Remote):
		m.mode = ModeCreateFromRemote
		m.create.remoteBranches = nil
		m.create.filteredBranches = nil
		m.create.branchSuggestionCursor = 0
		m.focusInput("Remote branch name", "")
		return m, tea.Batch(textinput.Blink, m.loadRemoteBranchesCmd())
	case key.Matches(msg, m.keys.Enter):
		if len(visible) > 0 {
			ws := visible[m.list.cursor]
			if m.isWorkspaceInFlight(ws.Name) {
				return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
			}
			return m, m.attachWorkspaceCmd(ws.Name)
		}
	case key.Matches(msg, m.keys.Diff):
		if len(visible) > 0 {
			ws := visible[m.list.cursor]
			if m.isWorkspaceInFlight(ws.Name) {
				return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
			}
			return m, m.loadDiffCmd(ws)
		}
	case key.Matches(msg, m.keys.PR):
		if len(visible) > 0 {
			ws := visible[m.list.cursor]
			if m.isWorkspaceInFlight(ws.Name) {
				return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
			}
			m.mode = ModePRGenerating
			m.pr.wsName = ws.Name
			m.pr.branch = ws.Branch
			m.pr.base = ws.BaseBranch
			return m, m.generatePRContentCmd(ws)
		}
	case key.Matches(msg, m.keys.Open):
		if len(visible) > 0 {
			ws := visible[m.list.cursor]
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
			ws := visible[m.list.cursor]
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
			ws := visible[m.list.cursor]
			if m.isWorkspaceInFlight(ws.Name) {
				return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
			}
			if m.list.selected[ws.Name] {
				delete(m.list.selected, ws.Name)
			} else {
				m.list.selected[ws.Name] = true
			}
			if m.list.cursor < len(visible)-1 {
				m.list.cursor++
			}
		}
	case key.Matches(msg, m.keys.Delete):
		if len(m.list.selected) > 0 {
			m.mode = ModeDelete
			m.del.target = ""
		} else if len(visible) > 0 {
			ws := visible[m.list.cursor]
			if m.isWorkspaceInFlight(ws.Name) {
				return m, m.transientErrCmd(fmt.Sprintf("workspace %q has a pending operation", ws.Name))
			}
			m.mode = ModeDelete
			m.del.target = ws.Name
		}
	case key.Matches(msg, m.keys.Filter):
		m.list.filtering = true
		m.list.filterQuery = ""
		m.list.cursor = 0
	case key.Matches(msg, m.keys.Sort):
		m.list.sortMode = (m.list.sortMode + 1) % 4
		m.list.cursor = 0
	case key.Matches(msg, m.keys.Agent):
		m.mode = ModeAgentSelect
		m.agentSel.cursor = 0
		for i, a := range config.PredefinedAgents {
			if a.IsActive(m.cfg) {
				m.agentSel.cursor = i
				break
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.ErrLog):
		m.mode = ModeErrorLog
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
	}
	return m, nil
}

func viewList(m Model) string {
	var s strings.Builder

	s.WriteString(renderLogo())
	s.WriteString("\n\n")

	s.WriteString(titleStyle.Render("Workspaces"))
	s.WriteString("\n\n")

	if m.list.filtering {
		prompt := filterPromptStyle.Render("/") + " " + m.list.filterQuery + "█"
		s.WriteString(prompt + "\n\n")
	} else if m.list.filterQuery != "" {
		s.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.list.filterQuery)) + "\n\n")
	}

	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	visible := m.visibleWorkspaces()

	if len(visible) == 0 {
		if m.list.filterQuery != "" {
			s.WriteString(itemStyle.Render("No workspaces match the filter."))
		} else {
			s.WriteString(itemStyle.Render("No workspaces found. Press 'n' to create one."))
		}
		s.WriteString("\n")
	} else {
		for i, ws := range visible {
			isDeleting := m.workspaceDeletingName == ws.Name || m.workspaceDeletingNames[ws.Name]
			if isDeleting {
				spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
				row := spinner + " " + ws.Name + "  " + pendingLabelStyle.Render("deleting…")
				s.WriteString(pendingItemStyle.Render(row))
				s.WriteString("\n")
				continue
			}

			style := itemStyle
			if i == m.list.cursor {
				style = selectedItemStyle
			}

			status := "○"
			statusColor := stoppedStyle
			if ws.Active {
				status = "●"
				statusColor = activeStyle
			} else if ws.WindowID != "" {
				status = "◎"
				statusColor = idleStyle
			}

			selectMark := "  "
			if m.list.selected[ws.Name] {
				selectMark = selectedMarkStyle.Render("✓ ")
			}

			title := selectMark + fmt.Sprintf("%s %s", statusColor.Render(status), ws.Name)

			if ws.IssueNumber > 0 {
				title += "  " + issueBadgeStyle.Render(fmt.Sprintf("#%d", ws.IssueNumber))
			}
			switch {
			case ws.PRStatus == "merged":
				title += "  " + mergedBadgeStyle.Render("merged · ready to delete")
			case ws.PRStatus == "closed":
				title += "  " + closedBadgeStyle.Render("PR closed")
			case ws.RemoteDeleted:
				title += "  " + remoteDeletedBadgeStyle.Render("remote deleted")
			case ws.PRStatus == "open" && ws.MergeConflicts:
				title += "  " + conflictsBadgeStyle.Render("PR open · conflicts")
				if ci, ok := m.ciStatus[ws.Name]; ok {
					title += renderCIBadge(ci)
				}
			case ws.PRStatus == "open":
				title += "  " + prOpenBadgeStyle.Render("PR open")
				if ci, ok := m.ciStatus[ws.Name]; ok {
					title += renderCIBadge(ci)
				}
			case ws.BranchPushed:
				title += "  " + pushedBadgeStyle.Render("pushed")
			default:
				title += "  " + notPushedBadgeStyle.Render("not pushed")
			}

			if ws.AgentStatus != nil {
				switch ws.AgentStatus.Status {
				case "success":
					title += "  " + agentSuccessStyle.Render("done")
				case "failure":
					title += "  " + agentFailureStyle.Render("failed")
				case "error":
					title += "  " + agentErrorStyle.Render("error")
				case "in_progress":
					title += "  " + agentInProgressStyle.Render("working...")
				}
			}

			branchDisplay := ws.Branch
			if ws.BaseBranch != "" {
				branchDisplay += " ← " + ws.BaseBranch
			}
			descParts := []string{branchDisplay, ws.DiffStat, "created " + formatAge(ws.CreatedAt)}

			if ws.UncommittedCount > 0 {
				descParts = append(descParts, uncommittedStyle.Render(fmt.Sprintf("~%d uncommitted", ws.UncommittedCount)))
			}

			if !ws.LastActivity.IsZero() {
				descParts = append(descParts, "active "+formatAge(ws.LastActivity))
			}

			if ws.AgentStatus != nil && ws.AgentStatus.Message != "" {
				descParts = append(descParts, ws.AgentStatus.Message)
			}

			desc := "  " + strings.Join(descParts, " • ")

			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
			s.WriteString("\n")

			if ws.PRStatus == "merged" && i == m.list.cursor {
				s.WriteString(mergedHintStyle.Render("  → Press x to clean up this merged workspace"))
				s.WriteString("\n")
			}
		}

		if m.list.cursor < len(visible) {
			ws := visible[m.list.cursor]
			if len(ws.FileChanges) > 0 {
				previewWidth := m.width - 8
				if previewWidth < 20 {
					previewWidth = 60
				}
				content := m.renderFileChanges(ws.FileChanges, previewWidth)
				s.WriteString(fileChangesBoxStyle.Width(previewWidth).Render(content))
				s.WriteString("\n")
			}
		}

		if m.agentPreview != "" && m.list.cursor < len(visible) {
			wsName := visible[m.list.cursor].Name
			previewWidth := m.width - 8
			if previewWidth < minPreviewWidth {
				previewWidth = defaultPreviewWidth
			}
			content := previewTitleStyle.Render("Agent Output: "+wsName) + "\n" +
				previewLineStyle.Render(m.agentPreview)
			s.WriteString(previewBoxStyle.Width(previewWidth).Render(content))
			s.WriteString("\n")
		}
	}

	if m.workspaceCreating {
		spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		s.WriteString(pendingItemStyle.Render(fmt.Sprintf(
			"  %s %s  %s",
			spinner,
			m.workspaceCreatingName,
			pendingLabelStyle.Render("creating…"),
		)))
		s.WriteString("\n")
	}

	s.WriteString("\n")
	s.WriteString(m.statusBar())
	s.WriteString("\n")

	s.WriteString(m.help.View(m.keys))

	return appStyle.Render(s.String())
}

func (m Model) statusBar() string {
	total := len(m.workspaces)
	active := 0
	openPRs := 0
	doneCount := 0
	for _, ws := range m.workspaces {
		if ws.Active {
			active++
		}
		if ws.PRStatus == "open" {
			openPRs++
		}
		if ws.AgentStatus != nil && (ws.AgentStatus.Status == "success" || ws.AgentStatus.Status == "failure" || ws.AgentStatus.Status == "error") {
			doneCount++
		}
	}
	parts := []string{
		fmt.Sprintf("%d workspaces", total),
		fmt.Sprintf("%d active", active),
		fmt.Sprintf("%d open PRs", openPRs),
		"sort: " + sortModeNames[m.list.sortMode],
	}
	if doneCount > 0 {
		parts = append(parts, fmt.Sprintf("%d done", doneCount))
	}
	if len(m.list.selected) > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", len(m.list.selected)))
	}
	if len(m.errorLog.entries) > 0 {
		parts = append(parts, fmt.Sprintf("%d errors (E)", len(m.errorLog.entries)))
	}
	return statusBarStyle.Render(strings.Join(parts, "  •  "))
}

func (m Model) visibleWorkspaces() []WorkspaceItem {
	sorted := m.sortedWorkspaces()
	if m.list.filterQuery == "" {
		return sorted
	}
	q := strings.ToLower(m.list.filterQuery)
	var out []WorkspaceItem
	for _, ws := range sorted {
		if strings.Contains(strings.ToLower(ws.Name), q) {
			out = append(out, ws)
		}
	}
	return out
}

func (m Model) sortedWorkspaces() []WorkspaceItem {
	ws := make([]WorkspaceItem, len(m.workspaces))
	copy(ws, m.workspaces)
	switch m.list.sortMode {
	case sortByAge:
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].CreatedAt.After(ws[j].CreatedAt)
		})
	case sortByActivity:
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].LastActivity.After(ws[j].LastActivity)
		})
	case sortByPR:
		prOrder := func(s string) int {
			switch s {
			case "open":
				return 0
			case "merged":
				return 1
			default:
				return 2
			}
		}
		sort.Slice(ws, func(i, j int) bool {
			return prOrder(ws[i].PRStatus) < prOrder(ws[j].PRStatus)
		})
	default: // sortByName
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].Name < ws[j].Name
		})
	}
	return ws
}

func renderCIBadge(ci string) string {
	switch ci {
	case "success":
		return " " + ciSuccessStyle.Render("✓ CI")
	case "failure":
		return " " + ciFailureStyle.Render("✗ CI")
	case "pending":
		return " " + ciPendingStyle.Render("⟳ CI")
	}
	return ""
}
