package tui

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/ui"
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
	st, ok := chat.Query(chat.SocketPath(repoRoot, wsName), wsName)
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
		return agentWaitingStyle.Render(ui.Truncate(label, badgeWidth))
	case chat.StateWorking:
		label := "working…"
		if st.Tool != "" {
			label += " · " + st.Tool
		}
		if st.Queued != "" {
			label += " · 1 queued"
		}
		return agentWorkingStyle.Render(ui.Truncate(label, badgeWidth))
	case chat.StateSettingUp:
		// Before the agent exists: the window is running the project's setup
		// commands, which is why nothing has answered a prompt yet.
		if st.Error != "" {
			return dangerStyle.Render("setup failed")
		}
		return agentWorkingStyle.Render("setting up…")
	case chat.StateChecking:
		label := "checking…"
		if st.Autopilot != nil && st.Autopilot.Phase == "publishing" {
			label = "publishing…"
		}
		if st.Autopilot != nil && st.Autopilot.Phase == "asking" {
			label = "APPROVE CHECK"
			return agentWaitingStyle.Render(label)
		}
		return agentWorkingStyle.Render(label)
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

// badgeWidth keeps a badge from pushing the rest of the row off screen; a bash
// command or a long path is otherwise unbounded.
const badgeWidth = 48

// chatMeta is the cost and context line shown under a workspace running a chat.
func chatMeta(st *chat.Status) string {
	if st == nil {
		return ""
	}
	var parts []string
	// How long it has been waiting leads, because it is the only part of this
	// line that is a reason to get up. The badge says a permission is pending;
	// this says it has been pending since before lunch.
	if w := waitedFor(st); w != "" {
		parts = append(parts, agentWaitingStyle.Render(w))
	}
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

// waitedFor is "blocked 12m" for a workspace whose agent is stopped on a
// permission, or "" for one that is not. Only this state gets an elapsed time:
// a working agent's badge already names the tool it is on, and an idle one has
// nobody to report to.
//
// A chat from before Status carried the moment leaves Since zero, and says
// nothing rather than claiming to have been waiting since 1970.
func waitedFor(st *chat.Status) string {
	if st == nil || st.State != chat.StateAwaiting || st.Since.IsZero() {
		return ""
	}
	return "blocked " + compactSince(st.Since)
}

// compactSince is an elapsed time short enough to sit in a row that already has
// five other things on it: 40s, 12m, 3h.
func compactSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// nextBlocked is the row the b key moves to: the workspace that has been
// waiting longest, or the next one along when the cursor is already on it.
//
// It is what makes a notification worth sending. A banner you cannot act on
// from where it appears is an interruption; this is the one keystroke that
// turns it back into an answer, and the cycle is for the case where the reason
// you were interrupted is that three of them are waiting.
func nextBlocked(visible []WorkspaceItem, cursor int) (int, bool) {
	waiting := make([]int, 0, len(visible))
	for i, ws := range visible {
		if ws.pendingPermission() != nil {
			waiting = append(waiting, i)
		}
	}
	if len(waiting) == 0 {
		return 0, false
	}
	sort.SliceStable(waiting, func(a, b int) bool {
		return blockedLonger(visible[waiting[a]], visible[waiting[b]])
	})

	for n, i := range waiting {
		if i == cursor {
			return waiting[(n+1)%len(waiting)], true
		}
	}
	return waiting[0], true
}

// blockedLonger reports whether a has been waiting longer than b. A chat too
// old to stamp the moment sorts last: not knowing how long it has waited is not
// a claim to have been waiting since 1970.
func blockedLonger(a, b WorkspaceItem) bool {
	at, bt := a.ChatStatus.Since, b.ChatStatus.Since
	if at.IsZero() || bt.IsZero() {
		return !at.IsZero()
	}
	return at.Before(bt)
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

// promptHint warns the message dialog about what a send will do before it is
// sent. A busy agent queues the prompt — the README promises it, but the
// dialog is where the promise belongs.
//
// The m key already refuses to open this dialog on an unreachable agent, but a
// refresh can kill one mid-typing; that case answers in chatUnavailable's own
// words rather than a second set of them that drifts from the first.
func (ws WorkspaceItem) promptHint() string {
	if why := ws.chatUnavailable(); why != "" {
		return why
	}
	switch {
	case ws.ChatStatus.Queued != "":
		return "another prompt is already queued — sending will be refused"
	case ws.ChatStatus.State == chat.StateWorking || ws.ChatStatus.State == chat.StateStarting:
		return "agent is working — your message will be queued"
	}
	return ""
}

// sendAgentCommand delivers one instruction to a workspace's chat process and
// waits for it to say whether it acted. The chat refuses anything it cannot
// honour — a prompt while the agent is mid-turn, a permission already answered
// in the window — and reporting "sent" for one of those would be a lie the user
// only discovers by attaching and finding nothing there.
// toggleAutopilotCmd flips a workspace's autopilot: state first, so the answer
// survives the chat window, then the live chat, so it takes effect now. A
// workspace with no chat running still toggles — the flag is what the next
// window reads — and only the send is best-effort.
func (m Model) toggleAutopilotCmd(ws WorkspaceItem) tea.Cmd {
	on := !ws.Autopilot
	repoRoot, store := m.repoRoot, m.stateStore
	return func() tea.Msg {
		if err := store.Update(ws.Name, func(w *state.Workspace) error {
			w.Autopilot = on
			return nil
		}); err != nil {
			return errMsg{fmt.Errorf("%s: %w", ws.Name, err)}
		}
		action := "autopilot off"
		text := "off"
		if on {
			action, text = "autopilot on", "on"
		}
		// The running chat is told directly rather than left to notice: the
		// flag in state.json is read at window start, and the window may have
		// hours left in it. A chat that is not running, or predates the
		// command, still honours the flag on its next start.
		if err := chat.Send(chat.SocketPath(repoRoot, ws.Name), ws.Name, chat.Command{
			Type: chat.CommandAutopilot, Text: text,
		}); err != nil {
			return agentCommandSentMsg{wsName: ws.Name, action: action + " (from the next chat window)"}
		}
		return agentCommandSentMsg{wsName: ws.Name, action: action}
	}
}

func (m Model) sendAgentCommand(wsName, action string, cmd chat.Command) tea.Cmd {
	repoRoot := m.repoRoot
	return func() tea.Msg {
		if err := chat.Send(chat.SocketPath(repoRoot, wsName), wsName, cmd); err != nil {
			return errMsg{fmt.Errorf("%s: %w", wsName, err)}
		}
		return agentCommandSentMsg{wsName: wsName, action: action}
	}
}

// selectAgent makes an agent the configured one for this repository and
// persists it. Returns an error message for the caller to surface, or "" on
// success.
func (m *Model) selectAgent(agent config.PredefinedAgent) string {
	return m.selectAgentIn(agent, config.FindConfigFile())
}

// selectAgentIn is selectAgent with the config file named: the repository's
// (the everyday case) or the global one (`agents use --global`), which the
// Agents tab offers under g.
func (m *Model) selectAgentIn(agent config.PredefinedAgent, path string) string {
	m.cfg.Agent.Command = agent.Command
	// Persist only the agent key (not the merged config), and surface failures
	// instead of silently losing the selection.
	if err := config.SetKeys(path, map[string]any{
		"agent.command": m.cfg.Agent.Command,
	}); err != nil {
		return fmt.Sprintf("failed to save agent selection: %v", err)
	}
	return ""
}

// handleAdapterConfirm answers the adapter download card, however it was
// asked for. Both keys that can start one arrive here, and they differ only
// in what happens afterwards: a pending selection means enter asked for it
// and the switch is waiting on the install, no pending selection means i did
// and the adapter is being fetched for later. The workspace list and the
// Agents tab both draw the card, so both answer it through this one place.
func (m Model) handleAdapterConfirm(msg tea.KeyMsg) (Model, tea.Cmd) {
	agent := *m.agentInstallConfirm
	switch msg.String() {
	case "y", "Y", "enter":
		m.agentInstallConfirm = nil
		m.agentSelecting = false
		return m, m.installAdapterCmd(agent)
	case "n", "esc", "q":
		m.agentInstallConfirm = nil
		m.agentPendingSelect = nil
		m.agentPendingPath = ""
	}
	return m, nil
}

// adapterConfirmView is the adapter download card, for both keys that can
// start one. Enter on an agent whose adapter is missing means "use this
// agent" and i means "get it ready for later", but 300MB off a package
// registry is asked about rather than sprung either way.
func (m Model) adapterConfirmView() string {
	agent := *m.agentInstallConfirm
	size := ""
	if agent.ACP.InstallSize != "" {
		size = " (" + agent.ACP.InstallSize + ", needs node)"
	}
	// The command is spelled out in full because the verb alone is not
	// something anyone can honestly agree to. The pinned version, the
	// prefix that keeps this out of the user's global npm root and the
	// refusal to run install hooks are the entire difference between this
	// and handing npm the machine, and none of the three is visible from
	// the word "install".
	body := fmt.Sprintf("%s\n%s\n\n%s",
		confirmLabelStyle.Render(agent.Name+" speaks the Agent Client Protocol through "+
			agent.ACPCommand()+size+"."),
		confirmLabelStyle.Render("It writes nothing outside the prefix named here, and runs:"),
		strings.Join(agent.ACPInstallCommand(), " "),
	)
	// Enter armed this with the agent it means to switch to; i armed it
	// with nothing, and promising a switch that will not happen is worse
	// than a shorter label.
	verb := "install"
	if m.agentPendingSelect != nil {
		verb = "install and use"
	}
	footer := fmt.Sprintf("%s %s  •  %s %s",
		confirmKeyStyle.Render("y"), confirmLabelStyle.Render(verb),
		confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel"),
	)
	return m.dialogCard("Install adapter for "+agent.Name+"?", body, footer, dialogAccent)
}

// agentPickerView is the A overlay: every agent, its readiness, and enter to
// switch.
func (m Model) agentPickerView() string {
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
		statusSt := lipgloss.NewStyle().Foreground(ui.Faint)
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
	// The id of the permission on screen travels with the answer, so the chat
	// can tell it apart from whichever one is waiting by the time it arrives.
	// This dialog's view is up to one refresh tick old.
	toolCallID := m.answerPerm.ToolCallID

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
			Type: chat.CommandPermission, OptionID: optionID, ToolCallID: toolCallID,
		})
	}

	for i, o := range options {
		if msg.String() == fmt.Sprint(i+1) {
			wsName := m.answerWs
			return m.closeAnswerDialog(), m.sendAgentCommand(wsName, "answered", chat.Command{
				Type: chat.CommandPermission, OptionID: o.OptionID, ToolCallID: toolCallID,
			})
		}
	}
	return m, nil
}
