package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
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

			status := "not found"
			statusSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
			if agent.IsInstalled() {
				status = "installed"
				statusSt = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
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

	// Diff view overlay
	if m.diffViewing {
		lines := strings.Split(m.diffContent, "\n")
		availHeight := m.height - 8
		if availHeight < 5 {
			availHeight = 5
		}
		// clamp scroll
		maxScroll := len(lines) - availHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.diffScrollOffset > maxScroll {
			m.diffScrollOffset = maxScroll
		}
		end := m.diffScrollOffset + availHeight
		if end > len(lines) {
			end = len(lines)
		}
		visible := lines[m.diffScrollOffset:end]

		var sb strings.Builder
		for _, line := range visible {
			sb.WriteString(renderDiffLine(line))
			sb.WriteString("\n")
		}

		scrollInfo := fmt.Sprintf("line %d/%d", m.diffScrollOffset+1, len(lines))
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

	visible := m.visibleWorkspaces()

	// Workspace list
	if len(visible) == 0 {
		if m.filterQuery != "" {
			s.WriteString(itemStyle.Render("No workspaces match the filter."))
		} else {
			s.WriteString(itemStyle.Render("No workspaces found. Press 'n' to create one."))
		}
		s.WriteString("\n")
	} else {
		for i, ws := range visible {
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
					switch ci {
					case "success":
						title += " " + ciSuccessStyle.Render("✓ CI")
					case "failure":
						title += " " + ciFailureStyle.Render("✗ CI")
					case "pending":
						title += " " + ciPendingStyle.Render("⟳ CI")
					}
				}
			case ws.PRStatus == "open":
				title += "  " + prOpenBadgeStyle.Render("PR open")
				if ci, ok := m.ciStatus[ws.Name]; ok {
					switch ci {
					case "success":
						title += " " + ciSuccessStyle.Render("✓ CI")
					case "failure":
						title += " " + ciFailureStyle.Render("✗ CI")
					case "pending":
						title += " " + ciPendingStyle.Render("⟳ CI")
					}
				}
			case ws.BranchPushed:
				title += "  " + pushedBadgeStyle.Render("pushed")
			default:
				title += "  " + notPushedBadgeStyle.Render("not pushed")
			}

			// Agent completion badge
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

			// Description line
			descParts := []string{ws.Branch, ws.DiffStat, "created " + formatAge(ws.CreatedAt)}

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

			// Merged cleanup hint
			if ws.PRStatus == "merged" && i == m.cursor {
				s.WriteString(mergedHintStyle.Render("  → Press x to clean up this merged workspace"))
				s.WriteString("\n")
			}
		}

		// Per-file changes panel for selected workspace
		if m.cursor < len(visible) {
			ws := visible[m.cursor]
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

		// Agent output preview for selected workspace
		if m.agentPreview != "" && m.cursor < len(visible) {
			wsName := visible[m.cursor].Name
			previewWidth := m.width - 8
			if previewWidth < 20 {
				previewWidth = 60
			}
			content := previewTitleStyle.Render("Agent Output: "+wsName) + "\n" +
				previewLineStyle.Render(m.agentPreview)
			s.WriteString(previewBoxStyle.Width(previewWidth).Render(content))
			s.WriteString("\n")
		}
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
		"sort: " + sortModeNames[m.sortMode],
	}
	if doneCount > 0 {
		parts = append(parts, fmt.Sprintf("%d done", doneCount))
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
func (m Model) sortedWorkspaces() []WorkspaceItem {
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
