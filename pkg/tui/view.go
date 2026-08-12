package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
)

// agentBrand names the agent running in a workspace, in its own colour.
// Colourless for an agent outside the registry, because inventing a colour for
// one would make the colours mean nothing.
func agentBrand(name string) string {
	if name == "" {
		return ""
	}
	mark, colour, display := config.Brand(name)
	label := mark + " " + display
	if colour == "" {
		return label
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render(label)
}

const (
	headerFooterHeight = 8
	minDiffHeight      = 5
	defaultPanelWidth  = 60
	minPanelWidth      = 20
)

func (m Model) View() string {
	// Error log overlay
	if m.showErrLog {
		var sb strings.Builder
		sb.WriteString(errLogTitleStyle.Render("Error Log") + "\n\n")
		if len(m.errLog) == 0 {
			sb.WriteString(errLogLineStyle.Render("No errors recorded."))
		} else {
			for _, entry := range m.errLog {
				sb.WriteString(errLogLineStyle.Render(entry))
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n" + helpStyle.Render("Any key to close"))
		return appStyle.Render(sb.String())
	}

	// Adapter download confirmation. Enter on an agent whose adapter is missing
	// means "use this agent", but 300MB is asked about rather than sprung.
	if m.agentInstallConfirm != nil {
		agent := *m.agentInstallConfirm
		size := ""
		if agent.ACP.InstallSize != "" {
			size = " (" + agent.ACP.InstallSize + ", needs node)"
		}
		footer := fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("install and use"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel"),
		)
		content := fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
			titleStyle.Render("Install adapter for "+agent.Name+"?"),
			confirmLabelStyle.Render(agent.Name+" speaks the Agent Client Protocol through "+
				agent.ACPCommand()+size+"."),
			confirmLabelStyle.Render("It installs to "+config.ToolsDir()+"."),
			footer,
		)
		return appStyle.Render(content)
	}

	// Agent selection overlay
	if m.agentSelecting {
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Select Agent"))
		sb.WriteString("\n\n")
		for i, agent := range config.PredefinedAgents {
			cursor := "  "
			style := itemStyle
			if i == m.agentCursor {
				cursor = "▶ "
				style = selectedItemStyle
			}

			name := agent.Name
			if agent.IsActive(m.cfg) {
				name += " (active)"
			}

			status, ready := m.readiness(agent)
			statusSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
			switch {
			case ready:
				statusSt = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
			case status == agentAdapterMissing:
				statusSt = lipgloss.NewStyle().Foreground(lipgloss.Color("#E9C46A"))
			}

			// Pad before styling: the escape codes count toward %-16s otherwise
			// and the column stops lining up.
			line := fmt.Sprintf("%s%-18s %-14s %s %s",
				cursor, name, agent.Command, statusSt.Render(fmt.Sprintf("%-15s", status)), agent.Description)
			sb.WriteString(style.Render(line))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		// The overlay covers the list, so a refusal has nowhere else to appear —
		// without this, enter on an unusable agent looks like a dead key.
		if m.err != nil {
			sb.WriteString(dangerStyle.Render(m.err.Error()))
			sb.WriteString("\n\n")
		}
		sb.WriteString(helpStyle.Render("↑/↓ navigate • Enter select • i install adapter • Esc cancel"))
		return appStyle.Render(sb.String())
	}

	// Agent permission dialog. The options are the ones the agent offered, so
	// the list never presents a choice the agent will refuse.
	if m.answering && m.answerPerm != nil {
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Agent permission: " + m.answerWs))
		sb.WriteString("\n\n")
		sb.WriteString(confirmLabelStyle.Render(m.answerPerm.Title))
		sb.WriteString("\n\n")
		for i, o := range m.answerPerm.Options {
			cursor, style := "  ", itemStyle
			if i == m.answerCursor {
				cursor, style = "▶ ", selectedItemStyle
			}
			sb.WriteString(style.Render(fmt.Sprintf("%s[%d] %s", cursor, i+1, o.Name)))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("↑/↓ navigate • 1-9 or Enter to answer • Esc cancel"))
		return appStyle.Render(sb.String())
	}

	// Message-the-agent dialog
	if m.prompting {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
			titleStyle.Render("Message agent: "+m.promptWs),
			m.input.View(),
			helpStyle.Render("Enter to send • Esc to cancel"),
		))
	}

	// Diff view overlay
	if m.diffViewing {
		lines := strings.Split(m.diffContent, "\n")
		availHeight := m.height - headerFooterHeight
		if availHeight < minDiffHeight {
			availHeight = minDiffHeight
		}
		maxScroll := len(lines) - availHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		// Clamp is authoritative in Update; this is a read-only safety for rendering.
		offset := m.diffScrollOffset
		if offset > maxScroll {
			offset = maxScroll
		}
		end := offset + availHeight
		if end > len(lines) {
			end = len(lines)
		}
		visible := lines[offset:end]

		var sb strings.Builder
		for _, line := range visible {
			sb.WriteString(renderDiffLine(line))
			sb.WriteString("\n")
		}

		scrollInfo := fmt.Sprintf("line %d/%d", offset+1, len(lines))
		footer := fmt.Sprintf("%s  •  %s  •  %s",
			helpStyle.Render("↑/k ↓/j scroll"),
			helpStyle.Render("esc to close"),
			helpStyle.Render(scrollInfo),
		)
		content := fmt.Sprintf("%s\n\n%s\n%s",
			titleStyle.Render("Diff: "+m.diffWsName),
			sb.String(),
			footer,
		)
		return appStyle.Render(content)
	}

	// Delete confirmation dialog
	if m.deleting {
		var titleMsg string
		if m.deleteTarget != "" {
			titleMsg = fmt.Sprintf("Delete workspace %q?", m.deleteTarget)
		} else {
			names := make([]string, 0, len(m.selected))
			for name := range m.selected {
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

	// Issue creation dialog
	if m.creating && m.issueMode {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
			titleStyle.Render("Create Workspace from GitHub Issue"),
			m.input.View(),
			helpStyle.Render("Enter issue number • Esc to cancel"),
		))
	}

	// Remote branch creation dialog with suggestion list
	if m.creating && m.remoteBranchMode {
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Create Workspace from Remote Branch"))
		sb.WriteString("\n\n")
		sb.WriteString(m.input.View())
		sb.WriteString("\n")
		if len(m.filteredBranches) > 0 {
			sb.WriteString("\n")
			// A repo with hundreds of remote branches would otherwise render one
			// line each and push the input box being typed into off the top of
			// the screen, since an over-tall frame loses its first lines.
			start, end := branchWindow(len(m.filteredBranches), m.branchSuggestionCursor, m.height-branchDialogChrome)
			for i := start; i < end; i++ {
				if i == m.branchSuggestionCursor {
					sb.WriteString(selectedItemStyle.Render("▶ " + m.filteredBranches[i]))
				} else {
					sb.WriteString(itemStyle.Render("  " + m.filteredBranches[i]))
				}
				sb.WriteString("\n")
			}
			if hidden := len(m.filteredBranches) - end + start; hidden > 0 {
				sb.WriteString(scrollHintStyle.Render(fmt.Sprintf("  … %d more, keep typing to narrow", hidden)))
				sb.WriteString("\n")
			}
		} else if len(m.remoteBranches) == 0 {
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

	// Two-step create dialog
	if m.creating {
		var stepLabel string
		if m.createStep == 0 {
			stepLabel = "Step 1/2 — Branch name"
		} else {
			stepLabel = fmt.Sprintf("Step 2/2 — Base branch  (branching from: %s)", m.newBranchName)
		}
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
			titleStyle.Render("Create New Workspace"),
			stepLabelStyle.Render(stepLabel),
			m.input.View(),
			helpStyle.Render("Enter to continue • Esc to cancel"),
		))
	}

	// PR content generation in progress
	if m.prGenerating {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s",
			titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase)),
			helpStyle.Render("Generating title and description from commits…"),
		))
	}

	// PR creation dialog
	if m.prCreating {
		var stepLabel string
		if m.prStep == 0 {
			stepLabel = "Step 1/2 — PR title"
		} else {
			stepLabel = fmt.Sprintf("Step 2/2 — PR body  (title: %s)", m.prTitle)
		}
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
			titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase)),
			stepLabelStyle.Render(stepLabel),
			m.input.View(),
			helpStyle.Render("Enter to continue • Esc to cancel"),
		))
	}

	var s strings.Builder

	// Logo
	s.WriteString(renderLogo())
	s.WriteString("\n\n")

	// Header with sort/filter info
	header := "Workspaces"
	s.WriteString(titleStyle.Render(header))
	s.WriteString("\n\n")

	// Filter prompt
	if m.filtering {
		prompt := filterPromptStyle.Render("/") + " " + m.filterQuery + "█"
		s.WriteString(prompt + "\n\n")
	} else if m.filterQuery != "" {
		s.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.filterQuery)) + "\n\n")
	}

	// Error message (transient)
	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	// Success notice (transient)
	if m.notice != "" {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(m.notice))
		s.WriteString("\n\n")
	}

	visible := m.visibleWorkspaces()

	// Everything below the list is built first so its height is known: the list
	// gets whatever is left, rather than overflowing and having the renderer
	// drop lines off the top of the screen.
	panels := m.selectionPanels(visible)
	start, end := m.listWindow(visible, m.height-lipgloss.Height(s.String())-lipgloss.Height(panels)-listChromeLines)

	// Workspace list
	if len(visible) == 0 {
		if m.filterQuery != "" {
			s.WriteString(itemStyle.Render("No workspaces match the filter."))
		} else {
			s.WriteString(itemStyle.Render("No workspaces found. Press 'n' to create one."))
		}
		s.WriteString("\n")
	} else {
		if start > 0 {
			s.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
			s.WriteString("\n")
		}
		for i := start; i < end; i++ {
			ws := visible[i]
			// Inline deleting state
			isDeleting := m.workspaceDeletingName == ws.Name || m.workspaceDeletingNames[ws.Name]
			if isDeleting {
				spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
				row := spinner + " " + ws.Name + "  " + pendingLabelStyle.Render("deleting…")
				s.WriteString(pendingItemStyle.Render(row))
				s.WriteString("\n")
				continue
			}

			style := itemStyle
			if i == m.cursor {
				style = selectedItemStyle
			}

			// Activity dot
			status := "○"
			statusColor := stoppedStyle
			if ws.Active {
				status = "●"
				statusColor = activeStyle
			} else if ws.WindowID != "" {
				status = "◎"
				statusColor = idleStyle
			}

			// Multi-select mark
			selectMark := "  "
			if m.selected[ws.Name] {
				selectMark = selectedMarkStyle.Render("✓ ")
			}

			title := selectMark + fmt.Sprintf("%s %s", statusColor.Render(status), ws.Name)

			// Badges
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

			// Agent badge. The chat process holds the protocol connection, so it
			// reports exactly what the agent is doing rather than inferring it.
			if badge := renderChatBadge(ws.ChatStatus); badge != "" {
				title += "  " + badge
			}

			// Description line
			branchDisplay := ws.Branch
			if ws.BaseBranch != "" {
				branchDisplay += " ← " + ws.BaseBranch
			}
			// The agent leads the line: which agent a worktree runs is the one
			// thing the row never said, and it decides how you talk to it.
			descParts := []string{branchDisplay, ws.DiffStat, "created " + formatAge(ws.CreatedAt)}
			if brand := agentBrand(ws.Agent); brand != "" {
				descParts = append([]string{brand}, descParts...)
			}

			if ws.UncommittedCount > 0 {
				descParts = append(descParts, uncommittedStyle.Render(fmt.Sprintf("~%d uncommitted", ws.UncommittedCount)))
			}

			if meta := chatMeta(ws.ChatStatus); meta != "" {
				descParts = append(descParts, meta)
			}

			if !ws.LastActivity.IsZero() {
				descParts = append(descParts, "active "+formatAge(ws.LastActivity))
			}

			desc := "  " + strings.Join(descParts, " • ")

			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
			s.WriteString("\n")

			// Merged cleanup hint
			if ws.PRStatus == "merged" && i == m.cursor {
				s.WriteString(mergedHintStyle.Render("  → Press x to clean up this merged workspace"))
				s.WriteString("\n")
			}
		}
		if end < len(visible) {
			s.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↓ %d more", len(visible)-end)))
			s.WriteString("\n")
		}
		s.WriteString(panels)
	}

	// Creating ghost entry (non-selectable, rendered outside the list)
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

	// Status bar
	s.WriteString("\n")
	s.WriteString(m.statusBar())
	s.WriteString("\n")

	// Help
	s.WriteString(m.help.View(m.keys))

	return appStyle.Render(s.String())
}

// statusBar renders the bottom stats line.
func (m Model) statusBar() string {
	total := len(m.workspaces)
	active := 0
	openPRs := 0
	waiting := 0
	for _, ws := range m.workspaces {
		if ws.Active {
			active++
		}
		if ws.PRStatus == "open" {
			openPRs++
		}
		// "waiting" is a permission the agent is blocked on, which the chat
		// reports outright. There is no "stalled" count any more: that existed
		// to guess at a hook-written file gone quiet, and the protocol never
		// leaves that in doubt.
		if ws.pendingPermission() != nil {
			waiting++
		}
	}
	parts := []string{
		fmt.Sprintf("%d workspaces", total),
		fmt.Sprintf("%d active", active),
		fmt.Sprintf("%d open PRs", openPRs),
		"sort: " + sortModeNames[m.sortMode],
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waiting))
	}
	if len(m.selected) > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", len(m.selected)))
	}
	if len(m.errLog) > 0 {
		parts = append(parts, fmt.Sprintf("%d errors (E)", len(m.errLog)))
	}
	return statusBarStyle.Render(strings.Join(parts, "  •  "))
}

// visibleWorkspaces returns the sorted and filtered workspace list.
// panelWidth returns the width for full-width panels. Before the first
// WindowSizeMsg (width 0) it falls back to the default; on genuinely narrow
// terminals it clamps to the minimum instead of overflowing every line.
func (m Model) panelWidth() int {
	if m.width <= 0 {
		return defaultPanelWidth
	}
	return max(minPanelWidth, m.width-8)
}

// currentWorkspaceName returns the name of the workspace under the cursor,
// or "" when the visible list is empty.
func (m Model) currentWorkspaceName() string {
	visible := m.visibleWorkspaces()
	if len(visible) == 0 || m.cursor >= len(visible) {
		return ""
	}
	return visible[m.cursor].Name
}

func (m Model) visibleWorkspaces() []WorkspaceItem {
	sorted := m.sortedWorkspaces()
	if m.filterQuery == "" {
		return sorted
	}
	q := strings.ToLower(m.filterQuery)
	var out []WorkspaceItem
	for _, ws := range sorted {
		if strings.Contains(strings.ToLower(ws.Name), q) {
			out = append(out, ws)
		}
	}
	return out
}

// sortedWorkspaces returns a copy of m.workspaces sorted by m.sortMode.
// Every mode breaks ties by name: the base list comes from map iteration
// (random per refresh), so without a total order tied rows would reshuffle
// on every refresh underneath the cursor.
func (m Model) sortedWorkspaces() []WorkspaceItem {
	ws := make([]WorkspaceItem, len(m.workspaces))
	copy(ws, m.workspaces)
	switch m.sortMode {
	case sortByAge:
		sort.Slice(ws, func(i, j int) bool {
			if !ws[i].CreatedAt.Equal(ws[j].CreatedAt) {
				return ws[i].CreatedAt.After(ws[j].CreatedAt)
			}
			return ws[i].Name < ws[j].Name
		})
	case sortByActivity:
		sort.Slice(ws, func(i, j int) bool {
			if !ws[i].LastActivity.Equal(ws[j].LastActivity) {
				return ws[i].LastActivity.After(ws[j].LastActivity)
			}
			return ws[i].Name < ws[j].Name
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
			if a, b := prOrder(ws[i].PRStatus), prOrder(ws[j].PRStatus); a != b {
				return a < b
			}
			return ws[i].Name < ws[j].Name
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

// listChromeLines is what the list must leave for the status bar, the help
// line, the blank between them and a possible "creating…" ghost.
const listChromeLines = 5

// listRowLines is the height of one workspace row: its title and its detail
// line. The merged-workspace hint adds one more, which the budget absorbs.
const listRowLines = 2

// listWindow is the slice of workspaces that fits, scrolled to keep the cursor
// inside it. Without this the list renders every workspace and overflows the
// terminal, and bubbletea's renderer resolves an over-tall frame by dropping
// lines from the *top* — silently eating the header, the error banner and the
// first rows, with no key able to bring them back.
func (m Model) listWindow(visible []WorkspaceItem, budget int) (start, end int) {
	rows := budget / listRowLines
	if rows < 1 {
		rows = 1
	}
	if rows >= len(visible) {
		return 0, len(visible)
	}

	// Keep the cursor in view, and prefer to scroll no further than needed so
	// the selection stays where the eye left it.
	start = m.cursor - rows/2
	start = min(max(start, 0), len(visible)-rows)
	return start, start + rows
}

// selectionPanels are the detail panels for the selected workspace. Built
// separately from the list so their height can be subtracted from its budget.
func (m Model) selectionPanels(visible []WorkspaceItem) string {
	if m.cursor >= len(visible) {
		return ""
	}
	var b strings.Builder
	ws := visible[m.cursor]
	width := m.panelWidth()

	if len(ws.FileChanges) > 0 {
		b.WriteString(fileChangesBoxStyle.Width(width).Render(m.renderFileChanges(ws.FileChanges, width)))
		b.WriteString("\n")
	}
	return b.String()
}

// branchDialogChrome is everything the suggestion list shares its screen with:
// the title, the input, the "n more" line, the help, and the blanks between.
const branchDialogChrome = 11

// branchWindow is the slice of suggestions that fits, scrolled to keep the
// highlighted one inside it.
func branchWindow(total, cursor, budget int) (start, end int) {
	rows := max(budget, 1)
	if rows >= total {
		return 0, total
	}
	start = min(max(cursor-rows/2, 0), total-rows)
	return start, start + rows
}
