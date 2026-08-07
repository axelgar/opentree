package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
)

// This file is the workspace list's half of the chat control socket: reading
// what an ACP agent is doing, and answering it without attaching.

// readChatStatus asks a workspace's chat process what it is doing. Workspaces
// running a non-ACP agent, or an ACP agent whose window is closed, simply have
// no socket — that is the normal case, not an error.
func readChatStatus(repoRoot, wsName string) *chat.Status {
	if repoRoot == "" {
		return nil
	}
	st, ok := chat.Query(chat.SocketPath(repoRoot, wsName))
	if !ok {
		return nil
	}
	return &st
}

// renderChatBadge is the list badge for a workspace running an ACP chat.
// Returns "" when there is no chat, so the caller falls back to the
// status-file badge.
func renderChatBadge(st *chat.Status) string {
	if st == nil {
		return ""
	}
	switch st.State {
	case chat.StateAwaiting:
		label := "PERMISSION"
		if st.Permission != nil && st.Permission.Title != "" {
			label += " · " + st.Permission.Title
		}
		return agentWaitingStyle.Render(truncateBadge(label))
	case chat.StateWorking:
		label := "working…"
		if st.Tool != "" {
			label += " · " + st.Tool
		}
		return agentWorkingStyle.Render(truncateBadge(label))
	case chat.StateStopped:
		return dangerStyle.Render("agent stopped")
	case chat.StateStarting:
		return agentIdleStyle.Render("starting…")
	default:
		return agentIdleStyle.Render("idle")
	}
}

// truncateBadge keeps a badge from pushing the rest of the row off screen; a
// bash command or a long path is otherwise unbounded.
func truncateBadge(s string) string {
	const max = 48
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

// chatMeta is the cost and context line shown under a workspace running a chat.
func chatMeta(st *chat.Status) string {
	if st == nil {
		return ""
	}
	var parts []string
	if st.ContextPct > 0 {
		parts = append(parts, fmt.Sprintf("%d%% ctx", st.ContextPct))
	}
	if st.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", st.Cost))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// pendingPermission is the escalation the selected workspace is blocked on, or
// nil when it is not waiting on one.
func (ws WorkspaceItem) pendingPermission() *chat.Permission {
	if ws.ChatStatus == nil || ws.ChatStatus.State != chat.StateAwaiting {
		return nil
	}
	return ws.ChatStatus.Permission
}

func (ws WorkspaceItem) chatWorking() bool {
	return ws.ChatStatus != nil && ws.ChatStatus.State == chat.StateWorking
}

// sendAgentCommand delivers one instruction to a workspace's chat process.
func (m Model) sendAgentCommand(wsName, action string, cmd chat.Command) tea.Cmd {
	repoRoot := m.repoRoot
	return func() tea.Msg {
		if err := chat.Send(chat.SocketPath(repoRoot, wsName), cmd); err != nil {
			return errMsg{fmt.Errorf("failed to reach %s's agent: %w", wsName, err)}
		}
		return agentCommandSentMsg{wsName: wsName, action: action}
	}
}

// openAnswerDialog arms the permission dialog for the selected workspace.
func (m Model) openAnswerDialog(ws WorkspaceItem) Model {
	perm := ws.pendingPermission()
	if perm == nil || len(perm.Options) == 0 {
		return m
	}
	m.answering = true
	m.answerWs = ws.Name
	m.answerPerm = perm
	m.answerCursor = 0
	return m
}

func (m Model) closeAnswerDialog() Model {
	m.answering = false
	m.answerWs = ""
	m.answerPerm = nil
	m.answerCursor = 0
	return m
}

// handleAnswerKey drives the permission dialog. Options are whatever the agent
// offered, so the dialog never invents a choice the agent will not accept.
func (m Model) handleAnswerKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	options := m.answerPerm.Options

	switch msg.String() {
	case "esc", "q":
		return m.closeAnswerDialog(), nil

	case "up", "k":
		m.answerCursor = (m.answerCursor - 1 + len(options)) % len(options)
		return m, nil

	case "down", "j":
		m.answerCursor = (m.answerCursor + 1) % len(options)
		return m, nil

	case "enter":
		wsName, optionID := m.answerWs, options[m.answerCursor].OptionID
		return m.closeAnswerDialog(), m.sendAgentCommand(wsName, "answered", chat.Command{
			Type: chat.CommandPermission, OptionID: optionID,
		})
	}

	for i, o := range options {
		if msg.String() == fmt.Sprint(i+1) {
			wsName := m.answerWs
			return m.closeAnswerDialog(), m.sendAgentCommand(wsName, "answered", chat.Command{
				Type: chat.CommandPermission, OptionID: o.OptionID,
			})
		}
	}
	return m, nil
}
