package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/axelgar/opentree/pkg/config"
)

const (
	headerFooterHeight = 8
	minDiffHeight      = 5
)

func (m Model) View() string {
	// --- Full-screen overlays (unchanged) ---

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

			status := "not found"
			statusSt := agentNotFoundStyle
			if agent.IsInstalled() {
				status = "installed"
				statusSt = agentInstalledStyle
			}

			cmdStr := agent.Command
			if len(agent.Args) > 0 {
				cmdStr += " " + strings.Join(agent.Args, " ")
			}

			line := fmt.Sprintf("%s%-18s %-14s %s  %s",
				cursor, name, cmdStr, statusSt.Render(status), agent.Description)
			sb.WriteString(style.Render(line))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("↑/↓ navigate • Enter select • Esc cancel"))
		return appStyle.Render(sb.String())
	}

	if m.diffViewing {
		lines := m.diffLines
		if lines == nil {
			lines = strings.Split(m.diffContent, "\n")
		}
		availHeight := m.height - headerFooterHeight
		if availHeight < minDiffHeight {
			availHeight = minDiffHeight
		}
		maxScroll := len(lines) - availHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
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
			confirmLabelStyle.Render("The workspace and all local changes will be removed."),
			footer,
		)
		return appStyle.Render(deleteDialogStyle.Render(content))
	}

	if m.creating && m.issueMode {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
			titleStyle.Render("Create Workspace from GitHub Issue"),
			m.input.View(),
			helpStyle.Render("Enter issue number • Esc to cancel"),
		))
	}

	if m.creating && m.remoteBranchMode {
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Create Workspace from Remote Branch"))
		sb.WriteString("\n\n")
		sb.WriteString(m.input.View())
		sb.WriteString("\n")
		if len(m.filteredBranches) > 0 {
			sb.WriteString("\n")
			for i, b := range m.filteredBranches {
				if i == m.branchSuggestionCursor {
					sb.WriteString(selectedItemStyle.Render("▶ " + b))
				} else {
					sb.WriteString(itemStyle.Render("  " + b))
				}
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

	if m.prGenerating {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s",
			titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase)),
			helpStyle.Render("Generating title and description from commits…"),
		))
	}

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

	if m.showConfig {
		cfgPath := config.FindConfigFile()
		agentCmd := m.cfg.Agent.CommandLine()
		autoPush := "false"
		if m.cfg.GitHub.AutoPush != nil && *m.cfg.GitHub.AutoPush {
			autoPush = "true"
		}
		row := func(label, value string) string {
			return fmt.Sprintf("  %s %s\n", configLabelStyle.Render(fmt.Sprintf("%-24s", label)), configValueStyle.Render(value))
		}
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Configuration") + "\n\n")
		sb.WriteString(row("agent.command:", agentCmd))
		sb.WriteString(row("worktree.base_dir:", m.cfg.Worktree.BaseDir))
		sb.WriteString(row("worktree.default_base:", m.cfg.Worktree.DefaultBase))
		sb.WriteString(row("github.auto_push:", autoPush))
		sb.WriteString(row("config file:", cfgPath))
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("A  switch agent  •  esc/c  close"))
		return appStyle.Render(sb.String())
	}

	// --- Split-pane main view ---
	leftWidth := m.leftPaneWidth()
	rightCols, rightRows := m.rightPaneSize()
	// Use rightPaneSize as the single source of truth for dimensions.
	// Add back the border+padding overhead for the lipgloss border style.
	rightWidth := rightCols
	paneHeight := rightRows
	if paneHeight < 10 {
		paneHeight = 10
	}

	leftContent := m.renderLeftPane(leftWidth, paneHeight)
	rightContent := m.renderRightPane(rightWidth, paneHeight)

	// Style panes with focus-aware borders
	leftBorder := unfocusedPaneBorder
	rightBorder := unfocusedPaneBorder
	if m.terminalFocused {
		rightBorder = focusedPaneBorder
	} else {
		leftBorder = focusedPaneBorder
	}

	leftPane := leftBorder.
		Width(leftWidth).
		Height(paneHeight).
		Render(leftContent)

	rightPane := rightBorder.
		Width(rightWidth).
		Height(paneHeight).
		Render(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}

// renderLeftPane renders the workspace list dashboard for the left pane.
func (m Model) renderLeftPane(width, height int) string {
	var s strings.Builder

	// Logo
	s.WriteString(renderLogo())
	s.WriteString("\n\n")

	// Filter prompt
	if m.filtering {
		prompt := filterPromptStyle.Render("/") + " " + m.filterQuery + "█"
		s.WriteString(prompt + "\n\n")
	} else if m.filterQuery != "" {
		s.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %q  (/)", m.filterQuery)) + "\n\n")
	}

	// Error message (transient)
	if m.err != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	visible := m.visibleWorkspaces()

	if len(visible) == 0 {
		if m.filterQuery != "" {
			s.WriteString(itemStyle.Render("No workspaces match."))
		} else {
			s.WriteString(itemStyle.Render("No workspaces. Press 'n' to create."))
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

			// Badges (compact for left pane)
			switch {
			case ws.PRStatus == "merged":
				title += " " + mergedBadgeStyle.Render("merged")
			case ws.PRStatus == "closed":
				title += " " + closedBadgeStyle.Render("closed")
			case ws.RemoteDeleted:
				title += " " + remoteDeletedBadgeStyle.Render("gone")
			case ws.PRStatus == "open" && ws.MergeConflicts:
				title += " " + conflictsBadgeStyle.Render("PR conflicts")
			case ws.PRStatus == "open":
				title += " " + prOpenBadgeStyle.Render("PR")
				if ci, ok := m.ciStatus[ws.Name]; ok {
					title += renderCIBadge(ci)
				}
			case ws.BranchPushed:
				title += " " + pushedBadgeStyle.Render("pushed")
			}

			if ws.IssueNumber > 0 {
				title += " " + issueBadgeStyle.Render(fmt.Sprintf("#%d", ws.IssueNumber))
			}

			if ws.AgentStatus != nil {
				switch ws.AgentStatus.Status {
				case "success":
					title += " " + agentSuccessStyle.Render("done")
				case "failure":
					title += " " + agentFailureStyle.Render("failed")
				case "error":
					title += " " + agentErrorStyle.Render("error")
				case "in_progress":
					title += " " + agentInProgressStyle.Render("working...")
				}
			}

			// Compact description
			desc := "  " + ws.DiffStat
			if !ws.LastActivity.IsZero() {
				desc += " • " + formatAge(ws.LastActivity)
			}

			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
			s.WriteString("\n")

			if ws.PRStatus == "merged" && i == m.cursor {
				s.WriteString(mergedHintStyle.Render("  → x to delete"))
				s.WriteString("\n")
			}
		}

		// Per-file changes for selected workspace
		if m.cursor < len(visible) {
			ws := visible[m.cursor]
			if len(ws.FileChanges) > 0 {
				panelWidth := width - 4
				if panelWidth < 20 {
					panelWidth = 20
				}
				content := m.renderFileChanges(ws.FileChanges, panelWidth)
				s.WriteString(fileChangesBoxStyle.Width(panelWidth).Render(content))
				s.WriteString("\n")
			}
		}
	}

	// Creating ghost entry
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

	// Help — use the bubbles/help model so '?' toggles full key-binding view.
	if m.terminalFocused {
		s.WriteString(helpStyle.Render("Ctrl+]: dashboard"))
	} else {
		s.WriteString(m.help.View(m.keys))
	}

	return s.String()
}

// renderRightPane renders the terminal view for the right pane.
func (m Model) renderRightPane(width, height int) string {
	visible := m.visibleWorkspaces()
	if len(visible) == 0 || m.cursor >= len(visible) {
		return termPaneHeaderStyle.Render("No workspace selected")
	}

	ws := visible[m.cursor]

	// Header
	agentExited := !m.terminalRunning
	headerTitle := "Terminal: " + ws.Name
	if m.terminalFocused {
		if agentExited {
			headerTitle += " (exited — Enter to restart • Ctrl+] to exit)"
		} else {
			headerTitle += " (focused)"
		}
	}
	if m.termScrollOffset > 0 {
		headerTitle += fmt.Sprintf(" [scroll ↑%d]", m.termScrollOffset)
	}
	var header string
	if m.terminalFocused || m.termScrollOffset > 0 {
		header = termPaneHeaderFocusedStyle.Render(headerTitle)
	} else {
		header = termPaneHeaderStyle.Render(headerTitle)
	}

	// Get terminal screen content
	var termContent string
	if m.termPM != nil {
		// Scrollback mode: use history buffer.
		if m.termScrollOffset > 0 {
			_, paneH := m.rightPaneSize()
			linesNeeded := paneH - 3
			if linesNeeded < 1 {
				linesNeeded = 1
			}
			sbLines, err := m.termPM.ScrollbackLines(ws.Name, m.termScrollOffset, linesNeeded)
			if err == nil && len(sbLines) > 0 {
				termContent = strings.Join(sbLines, "\n")
			} else {
				termContent = helpStyle.Render("No scrollback available")
			}
		} else if agentExited {
			// Process has exited: show a clear restart prompt regardless of VT state.
			if m.terminalFocused {
				termContent = helpStyle.Render("Agent process exited.\n\n  Enter    restart agent\n  Ctrl+]   return to dashboard")
			} else {
				termContent = helpStyle.Render("Press Enter to start terminal")
			}
		} else {
			// Live view: render current VT screen.
			screen, err := m.termPM.RenderScreen(ws.Name)
			switch {
			case err != nil && m.terminalFocused:
				termContent = helpStyle.Render("Starting…")
			case err != nil:
				termContent = helpStyle.Render("Press Enter to start terminal")
			case strings.TrimSpace(screen) == "" && m.terminalRunning:
				termContent = helpStyle.Render("Starting…")
			case strings.TrimSpace(screen) == "":
				termContent = helpStyle.Render("Press Enter to start terminal")
			default:
				termContent = screen
			}
		}
	} else {
		termContent = helpStyle.Render("Terminal not available")
	}

	// Truncate terminal content to fit pane
	lines := strings.Split(termContent, "\n")
	maxLines := height - 3 // header + border padding
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	// Truncate line widths
	maxWidth := width - 2
	if maxWidth < 10 {
		maxWidth = 10
	}
	for i, line := range lines {
		if lipgloss.Width(line) > maxWidth {
			lines[i] = truncateString(line, maxWidth)
		}
	}

	return header + "\n" + strings.Join(lines, "\n")
}

// statusBar renders the bottom stats line.
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
	sortNames := [4]string{"name", "age", "activity", "pr"}
	parts := []string{
		fmt.Sprintf("%d ws", total),
		fmt.Sprintf("%d active", active),
		"↕" + sortNames[m.sortMode],
	}
	if openPRs > 0 {
		parts = append(parts, fmt.Sprintf("%d PRs", openPRs))
	}
	if doneCount > 0 {
		parts = append(parts, fmt.Sprintf("%d done", doneCount))
	}
	if len(m.selected) > 0 {
		parts = append(parts, fmt.Sprintf("%d sel", len(m.selected)))
	}
	if len(m.errLog) > 0 {
		parts = append(parts, fmt.Sprintf("%d err", len(m.errLog)))
	}
	return statusBarStyle.Render(strings.Join(parts, " • "))
}

// visibleWorkspaces returns the sorted and filtered workspace list.
// Results are cached and only recomputed when viewCacheDirty is set.
func (m Model) visibleWorkspaces() []WorkspaceItem {
	if !m.viewCacheDirty && m.cachedVisible != nil {
		return m.cachedVisible
	}
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
// Results are cached and only recomputed when viewCacheDirty is set.
func (m Model) sortedWorkspaces() []WorkspaceItem {
	if !m.viewCacheDirty && m.cachedSorted != nil {
		return m.cachedSorted
	}
	ws := make([]WorkspaceItem, len(m.workspaces))
	copy(ws, m.workspaces)
	switch m.sortMode {
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

// truncateString truncates a string to maxWidth visible characters,
// correctly handling ANSI escape sequences.
func truncateString(s string, maxWidth int) string {
	return ansi.Truncate(s, maxWidth, "")
}
