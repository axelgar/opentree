package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/ui"
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
	// Error log overlay. Full screen rather than a card — it is a twenty-entry
	// debugging aid, not a question — but with the same bars as the diff view.
	if m.showErrLog {
		var sb strings.Builder
		sb.WriteString(m.bar(errLogTitleStyle.Render("Error Log"),
			dialogHintStyle.Render(fmt.Sprintf("%d recorded", len(m.errLog)))))
		sb.WriteString("\n" + m.divider() + "\n")
		if len(m.errLog) == 0 {
			sb.WriteString(errLogLineStyle.Render("No errors recorded."))
			sb.WriteString("\n")
		} else {
			for _, entry := range m.errLog {
				sb.WriteString(errLogLineStyle.Render(entry))
				sb.WriteString("\n")
			}
		}
		sb.WriteString(m.divider() + "\n")
		// This screen exists to get text out of the program, so the key that
		// does it is named, and its outcome lands here — the toast slot belongs
		// to the list, which is not on screen to show it.
		hint := "any key to close"
		if len(m.errLog) > 0 {
			hint = "c copy all  •  any key to close"
		}
		sb.WriteString(m.bar(dialogHintStyle.Render(hint), m.toastLine()))
		return appStyle.Render(sb.String())
	}

	// The Skills tab draws its own screen, including the tab bar. It sits above
	// the workspace dialogs because none of them can be open behind it.
	if m.tab == tabSkills {
		return m.skillsView()
	}
	if m.tab == tabServers {
		return m.serversView()
	}

	// Adapter download confirmation. Enter on an agent whose adapter is missing
	// means "use this agent", but 300MB is asked about rather than sprung.
	if m.agentInstallConfirm != nil {
		agent := *m.agentInstallConfirm
		size := ""
		if agent.ACP.InstallSize != "" {
			size = " (" + agent.ACP.InstallSize + ", needs node)"
		}
		body := fmt.Sprintf("%s\n%s",
			confirmLabelStyle.Render(agent.Name+" speaks the Agent Client Protocol through "+
				agent.ACPCommand()+size+"."),
			confirmLabelStyle.Render("It installs to "+config.ToolsDir()+"."),
		)
		footer := fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("install and use"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel"),
		)
		return m.dialogCard("Install adapter for "+agent.Name+"?", body, footer, dialogAccent)
	}

	// Agent selection overlay
	if m.agentSelecting {
		var sb strings.Builder
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
				statusSt = successStyle
			case status == agentAdapterMissing:
				statusSt = warnStyle
			}

			// Pad before styling: the escape codes count toward %-16s otherwise
			// and the column stops lining up. The description takes whatever the
			// card has left, so a narrow terminal shortens it rather than
			// wrapping one agent onto two rows and breaking the column.
			head := fmt.Sprintf("%s%-18s %-14s %-15s ", cursor, name, agent.Command, "")
			// The card's interior, less the row style's own border and padding.
			room := m.dialogMaxWidth() - 2*dialogPadding - 3 - lipgloss.Width(head)
			line := fmt.Sprintf("%s%-18s %-14s %s %s",
				cursor, name, agent.Command, statusSt.Render(fmt.Sprintf("%-15s", status)),
				ui.Truncate(agent.Description, max(room, 8)))
			sb.WriteString(style.Render(line))
			if i < len(config.PredefinedAgents)-1 {
				sb.WriteString("\n")
			}
		}
		// The card covers the list, so a refusal has nowhere else to appear —
		// without this, enter on an unusable agent looks like a dead key.
		if m.err != nil {
			sb.WriteString("\n\n" + dangerStyle.Render(m.err.Error()))
		}
		return m.dialogCard("Select Agent", sb.String(),
			dialogHintStyle.Render("↑/↓ navigate • Enter select • i install adapter • Esc cancel"),
			dialogAccent)
	}

	// Agent permission dialog. The options are the ones the agent offered, so
	// the list never presents a choice the agent will refuse.
	if m.answering && m.answerPerm != nil {
		var sb strings.Builder
		sb.WriteString(confirmLabelStyle.Render(m.answerPerm.Title))
		sb.WriteString("\n")
		for i, o := range m.answerPerm.Options {
			cursor, style := "  ", itemStyle
			if i == m.answerCursor {
				cursor, style = "▶ ", selectedItemStyle
			}
			sb.WriteString("\n" + style.Render(fmt.Sprintf("%s[%d] %s", cursor, i+1, o.Name)))
		}
		return m.dialogCard("Agent permission: "+m.answerWs, sb.String(),
			dialogHintStyle.Render("↑/↓ navigate • 1-9 or Enter to answer • Esc cancel"),
			dialogAccent)
	}

	// Message-the-agent dialog
	if m.prompting {
		body := m.input.View()
		if i := m.workspaceIndex(m.promptWs); i >= 0 {
			if h := m.workspaces[i].promptHint(); h != "" {
				body += "\n" + rowHintStyle.Render(h)
			}
		}
		return m.dialogCard("Message agent: "+m.promptWs, body,
			dialogHintStyle.Render("Enter to send • Esc to cancel"), dialogAccent)
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

		// A reader, not a card — but with the same two bars, so the keys sit on
		// one line with the position instead of wrapping into a second.
		header := m.bar(titleStyle.Render("Diff: "+m.diffWsName), m.diffSummary())
		footer := m.bar(
			dialogHintStyle.Render("↑/k ↓/j scroll  •  esc close"),
			dialogHintStyle.Render(fmt.Sprintf("line %d/%d", offset+1, len(lines))),
		)
		return appStyle.Render(strings.Join([]string{
			header, m.divider(), sb.String() + m.divider(), footer,
		}, "\n"))
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
		// The one destructive dialog keeps the red border it already had.
		//
		// The server is named because it is the part that is not obviously
		// implied: a dev server outlives the window it was started from, and
		// one still running against a deleted worktree holds its port and
		// prints stack traces at nobody.
		return m.dialogCard(titleMsg,
			confirmLabelStyle.Render("The worktree, its tmux windows — including any dev server — and all local changes will be removed."),
			footer, dialogDanger)
	}

	// Issue creation dialog
	if m.creating && m.issueMode {
		return m.dialogCard("Create Workspace from GitHub Issue", m.input.View(),
			dialogHintStyle.Render("Enter issue number • Esc to cancel"), dialogAccent)
	}

	// Remote branch creation dialog with suggestion list
	if m.creating && m.remoteBranchMode {
		var sb strings.Builder
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
			sb.WriteString("\n" + dialogHintStyle.Render("  loading branches…") + "\n")
		} else {
			sb.WriteString("\n" + dialogHintStyle.Render("  no branches match") + "\n")
		}
		return m.dialogCard("Create Workspace from Remote Branch",
			strings.TrimRight(sb.String(), "\n"),
			dialogHintStyle.Render("↑/↓ navigate • Tab select • Enter confirm • Esc cancel"),
			dialogAccent)
	}

	// Two-step create dialog
	if m.creating {
		var stepLabel string
		if m.createStep == 0 {
			stepLabel = "Step 1/2 — Branch name"
		} else {
			stepLabel = fmt.Sprintf("Step 2/2 — Base branch  (branching from: %s)", m.newBranchName)
		}
		return m.dialogCard("Create New Workspace",
			stepLabelStyle.Render(stepLabel)+"\n"+m.input.View(),
			dialogHintStyle.Render("Enter to continue • Esc to cancel"), dialogAccent)
	}

	// PR content generation in progress
	if m.prGenerating {
		// No spinner: the tick only runs for create and delete, and a frozen
		// spinner reads as a hang rather than as work in progress.
		return m.dialogCard(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase),
			pendingLabelStyle.Render("Generating title and description from commits…"),
			"", dialogAccent)
	}

	// PR creation dialog
	if m.prCreating {
		var stepLabel string
		if m.prStep == 0 {
			stepLabel = "Step 1/2 — PR title"
		} else {
			stepLabel = fmt.Sprintf("Step 2/2 — PR body  (title: %s)", m.prTitle)
		}
		return m.dialogCard(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase),
			stepLabelStyle.Render(stepLabel)+"\n"+m.input.View(),
			dialogHintStyle.Render("Enter to continue • Esc to cancel"), dialogAccent)
	}

	var s strings.Builder

	// Logo
	s.WriteString(renderLogo())
	s.WriteString("\n\n")

	s.WriteString(m.tabBar())
	s.WriteString("\n\n")

	// The missing-tmux warning, above everything the list has to say: without
	// tmux none of it can be acted on. It sits in the flow rather than the toast
	// slot because it is a standing condition, not an event — a toast would
	// clear itself after three seconds and leave the screen looking fine.
	if banner := m.tmuxBanner(); banner != "" {
		s.WriteString(banner)
		s.WriteString("\n\n")
	}

	// Filter prompt
	if m.filtering {
		prompt := filterPromptStyle.Render("/") + " " + m.filterQuery + "█"
		s.WriteString(prompt + "\n\n")
	} else if m.filterQuery != "" {
		s.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.filterQuery)) + "\n\n")
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
				spinner := ui.SpinnerFrames[m.spinnerFrame%len(ui.SpinnerFrames)]
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

			// The agent working here cannot see the repository's own skills, and
			// nothing else about the row would say so.
			if len(ws.MissingSkills) > 0 {
				title += "  " + uncommittedStyle.Render("⚠ no repo skills")
			}

			// Description line
			branchDisplay := ws.Branch
			if ws.BaseBranch != "" {
				branchDisplay += " ← " + ws.BaseBranch
			}
			// The agent leads the line: which agent a worktree runs is the one
			// thing the row never said, and it decides how you talk to it.
			descParts := []string{branchDisplay, ws.renderDiffStat(), "created " + formatAge(ws.CreatedAt)}
			if brand := agentBrand(ws.Agent); brand != "" {
				descParts = append([]string{brand}, descParts...)
			}

			if ws.UncommittedCount > 0 {
				descParts = append(descParts, uncommittedStyle.Render(fmt.Sprintf("~%d uncommitted", ws.UncommittedCount)))
			}

			if meta := chatMeta(ws.ChatStatus); meta != "" {
				descParts = append(descParts, meta)
			}

			// A running server, and the port it was given — which is the thing
			// you actually need from it, since five worktrees of one project all
			// wanted the same one.
			if ws.ServerRunning {
				descParts = append(descParts, agentWorkingStyle.Render(fmt.Sprintf("server :%d", ws.Port)))
			}

			if !ws.LastActivity.IsZero() {
				descParts = append(descParts, "active "+formatAge(ws.LastActivity))
			}

			// A long branch name used to push the timestamps off the right
			// edge. ansi.Truncate because the parts carry their own colours:
			// slicing runes here would cut through an escape sequence.
			desc := "  " + ansi.Truncate(strings.Join(descParts, " • "), m.panelWidth()-2, "…")

			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
			s.WriteString("\n")

			// What the selected row can do right now, in its own quiet line —
			// the row carries state, this carries the next action.
			if i == m.cursor {
				if hint := ws.actionHint(); hint != "" {
					s.WriteString(rowHintStyle.Render("  → " + hint))
					s.WriteString("\n")
				}
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
		spinner := ui.SpinnerFrames[m.spinnerFrame%len(ui.SpinnerFrames)]
		s.WriteString(pendingItemStyle.Render(fmt.Sprintf(
			"  %s %s  %s",
			spinner,
			m.workspaceCreatingName,
			pendingLabelStyle.Render("creating…"),
		)))
		s.WriteString("\n")
	}

	// Toast slot: one line, always rendered, pinned above the status bar.
	// Always reserving the line is what stops a banner's arrival or clearing
	// from moving the list.
	s.WriteString(m.toastLine())
	s.WriteString("\n")

	// Status bar, fenced off from the list by a rule. The divider takes the
	// blank line's old slot, so the chrome budget below does not change.
	s.WriteString(m.divider())
	s.WriteString("\n")
	s.WriteString(m.statusBar())
	s.WriteString("\n")

	// Help
	s.WriteString(m.help.View(m.keys))

	return appStyle.Render(s.String())
}

// tmuxBanner is the standing warning for a machine with no tmux on it, or ""
// on the ordinary machine that has one. Two lines: what is wrong, and the one
// command that fixes it — a user who has just installed opentree has no reason
// to know it needs tmux at all, so naming the dependency is half the message.
//
// Each line is truncated to the panel rather than wrapped: a wrapped second
// line would push a workspace row off the bottom of a short terminal, and the
// install command is the part that survives a narrow one.
func (m Model) tmuxBanner() string {
	if !m.tmuxMissing {
		return ""
	}
	width := m.panelWidth()
	return warnStyle.Render(ui.Truncate("⚠ tmux is not installed — no workspace can be created", width)) +
		"\n" +
		diffStyle.Render(ui.Truncate("  opentree runs each agent in a tmux window · brew install tmux", width))
}

// toastLine renders the transient error or notice into its fixed slot, or ""
// when there is nothing to say. Errors win the slot: a failure is the thing
// the user can least afford to miss.
func (m Model) toastLine() string {
	width := m.panelWidth() - 2 // the ✓/✕ glyph and its space
	if m.err != nil {
		return toastErrStyle.Render("✕ " + ui.Truncate(m.err.Error(), width))
	}
	if m.notice != "" {
		return successStyle.Render("✓ " + ui.Truncate(m.notice, width))
	}
	return ""
}

// statusBar renders the bottom stats line: numbers bright, labels dim, and
// the counts that signal work-to-do (waiting, errors) in their signal colour.
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
		stat(total, pluralLabel(total, "workspace")),
		stat(active, "active"),
		stat(openPRs, pluralLabel(openPRs, "open PR")),
		// The key is inline: a status that can be changed but says not how
		// is a dead end for anyone who hasn't opened the full help yet.
		statusBarStyle.Render("sort: " + sortModeNames[m.sortMode] + " (s)"),
	}
	if waiting > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("%d", waiting))+
			statusBarStyle.Render(" waiting"))
	}
	if len(m.selected) > 0 {
		parts = append(parts, selectedMarkStyle.Render(fmt.Sprintf("%d", len(m.selected)))+
			statusBarStyle.Render(" selected"))
	}
	if len(m.errLog) > 0 {
		parts = append(parts, toastErrStyle.Render(fmt.Sprintf("%d", len(m.errLog)))+
			statusBarStyle.Render(" errors (E)"))
	}
	return strings.Join(parts, statusBarStyle.Render("  •  "))
}

// stat is one bright count with its dim label, the unit the status bar and
// the skills footer are both made of.
func stat(n int, label string) string {
	return statNumStyle.Render(fmt.Sprintf("%d", n)) + statusBarStyle.Render(" "+label)
}

// divider is the full-width rule fencing the status bar off from the list.
func (m Model) divider() string {
	return dividerStyle.Render(strings.Repeat("─", m.chromeWidth()))
}

// diffSummary is the change count in the diff view's header bar. The reader
// is looking at a wall of hunks; the header says how much of it there is.
func (m Model) diffSummary() string {
	if i := m.workspaceIndex(m.diffWsName); i >= 0 {
		return m.workspaces[i].renderDiffStat()
	}
	return ""
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

// toastLines is the height of the always-reserved feedback slot. Both lists
// render one and both budget for it, so it is one constant rather than a
// hand-edited number in each that has to be kept in step by memory.
const toastLines = 1

// listChromeLines is what the list must leave for the status bar, the help
// line, the blanks between them, the toast slot, and a possible "creating…"
// ghost. The selected row's action hint adds one more line, which the ghost's
// reserved line absorbs when no create is in flight.
const listChromeLines = 5 + toastLines

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

// branchDialogChrome is everything the suggestion list shares its card with:
// the title, the input, the "n more" line, the hints, the blanks between, and
// the card's own border and padding.
const branchDialogChrome = 12

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
