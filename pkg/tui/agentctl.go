package tui

import (
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
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
		if st.Queued != "" {
			label += " · 1 queued"
		}
		return agentWorkingStyle.Render(truncateBadge(label))
	case chat.StateStopped:
		return dangerStyle.Render("agent stopped")
	case chat.StateStarting:
		if st.Queued != "" {
			return agentIdleStyle.Render("starting… · 1 queued")
		}
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

// chatUnavailable explains why the dashboard cannot reach this workspace's
// agent, or "" when it can. A key that quietly does nothing is
// indistinguishable from a broken one, so every one of these keys says why.
//
// A silent socket usually just means the chat window was closed, which is an
// ordinary thing to do and one enter away from fixed. The exception is a
// workspace created by an older opentree against an agent that has since left
// the registry: reopening it cannot work, and saying so is the only way the
// user learns the workspace needs a different agent.
func (ws WorkspaceItem) chatUnavailable() string {
	if ws.ChatStatus != nil {
		if ws.ChatStatus.State == chat.StateStopped {
			return fmt.Sprintf("%s's agent has stopped — attach to restart it", ws.Name)
		}
		return ""
	}
	if config.FindAgent(ws.Agent) == nil {
		return fmt.Sprintf("%s runs %s, which opentree no longer supports — delete it or switch its agent",
			ws.Name, ws.Agent)
	}
	return fmt.Sprintf("%s's chat is not running — press enter to reopen it", ws.Name)
}

// sendAgentCommand delivers one instruction to a workspace's chat process and
// waits for it to say whether it acted. The chat refuses anything it cannot
// honour — a prompt while the agent is mid-turn, a permission already answered
// in the window — and reporting "sent" for one of those would be a lie the user
// only discovers by attaching and finding nothing there.
func (m Model) sendAgentCommand(wsName, action string, cmd chat.Command) tea.Cmd {
	repoRoot := m.repoRoot
	return func() tea.Msg {
		if err := chat.Send(chat.SocketPath(repoRoot, wsName), cmd); err != nil {
			return errMsg{fmt.Errorf("%s: %w", wsName, err)}
		}
		return agentCommandSentMsg{wsName: wsName, action: action}
	}
}

// selectAgent makes an agent the configured one and persists it. Returns an
// error message for the caller to surface, or "" on success.
func (m *Model) selectAgent(agent config.PredefinedAgent) string {
	m.cfg.Agent.Command = agent.Command
	// Persist only the agent key (not the merged config), and surface failures
	// instead of silently losing the selection.
	if err := config.SetKeys(config.FindConfigFile(), map[string]any{
		"agent.command": m.cfg.Agent.Command,
	}); err != nil {
		return fmt.Sprintf("failed to save agent selection: %v", err)
	}
	return ""
}

// installAdapterCmd fetches an agent's ACP adapter, handing the terminal to the
// package manager so its progress and any failure are the user's to read.
func (m Model) installAdapterCmd(agent config.PredefinedAgent) tea.Cmd {
	install := agent.ACPInstallCommand()
	c := exec.Command(install[0], install[1:]...) // #nosec G204 -- from the agent registry, not user input
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return adapterInstalledMsg{adapter: agent.ACPCommand(), err: err}
	})
}

// What the picker can say about an agent, and what pressing enter on it does.
const (
	agentReady          = "installed"
	agentNotFound       = "not found"
	agentAdapterMissing = "adapter missing"
)

// agentReadiness is the status shown beside an agent in the picker. An agent
// reached through an adapter can be installed while the adapter is not, and the
// picker is where you would want to learn that.
func agentReadiness(agent config.PredefinedAgent) (string, bool) {
	if !agent.IsInstalled() {
		return agentNotFound, false
	}
	if len(agent.ACPInstallCommand()) > 0 && !agent.ACPInstalled() {
		return agentAdapterMissing, false
	}
	return agentReady, true
}

// readiness answers through the model so tests are not at the mercy of which
// agents happen to be installed on the machine running them.
func (m Model) readiness(agent config.PredefinedAgent) (string, bool) {
	if m.agentReadiness != nil {
		return m.agentReadiness(agent)
	}
	return agentReadiness(agent)
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
