package chat

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// Update handles one message and republishes the session's status. Publishing
// here rather than at each call site is the whole point: every state change
// funnels through this method, so the socket cannot drift out of date.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(Model); ok && nm.publish != nil {
		nm.publish(nm.status())
	}
	return next, cmd
}

// status is the view of this session the workspace list sees.
func (m Model) status() Status {
	st := Status{Workspace: m.opts.Workspace, State: StateIdle}
	switch {
	case m.dead || m.authNeed:
		st.State = StateStopped
	case m.perm != nil:
		st.State = StateAwaiting
		st.Permission = &Permission{
			Title:   toolLabel(m.perm.req.ToolCall, m.opts.Cwd),
			Options: m.perm.req.Options,
		}
	case m.turn:
		st.State = StateWorking
	case m.sessionID == "":
		st.State = StateStarting
	}

	st.Tool = m.currentTool()
	if m.usage != nil {
		if m.usage.Cost != nil {
			st.Cost = m.usage.Cost.Amount
		}
		if m.usage.Size > 0 {
			st.ContextPct = m.usage.Used * 100 / m.usage.Size
		}
	}
	return st
}

// currentTool is the call the agent is running, or the last one it ran — which
// is what a glance at the list wants to know.
func (m Model) currentTool() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind == entryTool {
			return toolLabel(m.entries[i].tool, m.opts.Cwd)
		}
	}
	return ""
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(msg.Width - 4)
		m.ready = true
		m = m.relayout()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case acpUpdateMsg:
		m = m.applyUpdate(acp.SessionUpdate(msg))
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case permissionMsg:
		// Hold the escalation; the agent stays blocked on its reply channel
		// until a key answers it.
		m.perm = &msg
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case sessionReadyMsg:
		m.sessionID = msg.id
		m.modelName = currentValue(msg.options, "model")
		if msg.note != "" {
			m = m.appendNotice(msg.note)
		}
		m = m.relayout()
		return m, nil

	case promptDoneMsg:
		m.turn = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m = m.appendNotice(turnSummary(msg.resp))
		m = m.relayout()
		return m, nil

	case spinnerTickMsg:
		if !m.turn {
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		return m, spinnerTick()

	case agentGoneMsg:
		// This arrives down the handler channel, so the reader has to be
		// re-issued like any other delivery — otherwise the stream stops here
		// and a restarted agent's history replay has nowhere to land.
		//
		// A restarted agent also supersedes the old one; the old process's
		// death notice arrives afterwards and must not condemn its replacement.
		if msg.generation != m.generation {
			return m, waitForMsg(m.msgs)
		}
		m.dead = true
		m.turn = false
		if m.err == nil {
			m.err = fmt.Errorf("%s exited", m.opts.Command)
		}
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case clientReadyMsg:
		m.client = msg.client
		m.generation = msg.generation
		m = m.withAgentInfo(msg.info)
		m.dead, m.authNeed, m.err = false, false, nil
		// The replay rebuilds the log from scratch, so drop what is on screen
		// rather than rendering the conversation twice.
		m.entries, m.toolIdx = nil, make(map[string]int)
		m = m.relayout()
		return m, m.startSession()

	case filesLoadedMsg:
		m.files = msg.files
		return m, nil

	case socketCommandMsg:
		// Also arrives down the handler channel, so the reader has to be handed
		// back alongside whatever the command itself triggers.
		next, cmd := m.applyRemoteCommand(Command(msg))
		return next, tea.Batch(cmd, waitForMsg(m.msgs))

	case authDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Credentials are read at startup, so a fresh login needs a fresh agent.
		return m, m.restartCmd()

	case errMsg:
		m.err = msg.err
		m.authNeed = msg.auth
		m.turn = false
		m = m.relayout()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.perm != nil {
		return m.handlePermissionKey(msg)
	}
	// A stopped agent takes over the keyboard: r and l would otherwise be
	// swallowed by the textarea, which is useless with nothing to send to.
	if m.stopped() {
		return m.handleStoppedKey(msg)
	}

	// The palette owns navigation and acceptance while it is open, so arrows
	// and tab do not fall through to scrolling or the textarea.
	if m.completion.active() {
		if handled, model, cmd := m.handleCompletionKey(msg); handled {
			return model, cmd
		}
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Thoughts):
		m.hideThoughts = !m.hideThoughts
		m = m.relayout()
		return m, nil

	case key.Matches(msg, m.keys.Cancel):
		if m.turn {
			_ = m.client.Cancel(m.sessionID)
		}
		return m, nil

	case key.Matches(msg, m.keys.PageUp):
		m.viewport.PageUp()
		return m, nil

	case key.Matches(msg, m.keys.PageDown):
		m.viewport.PageDown()
		return m, nil

	case key.Matches(msg, m.keys.ScrollUp):
		m.viewport.HalfPageUp()
		return m, nil

	case key.Matches(msg, m.keys.ScrollDn):
		m.viewport.HalfPageDown()
		return m, nil

	case key.Matches(msg, m.keys.Send):
		text := strings.TrimSpace(m.input.Value())
		if text == "" || m.turn || m.sessionID == "" {
			return m, nil
		}
		cmd := m.promptCmd(text)
		m.input.Reset()
		m.completion = completionState{}
		m.entries = append(m.entries, entry{kind: entryUser, text: text})
		m.turn = true
		m.err = nil
		m = m.relayout()
		return m, tea.Batch(cmd, spinnerTick())
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m = m.refreshCompletion()
	return m, cmd
}

// refreshCompletion recomputes the palette from whatever is now typed. Keeping
// the cursor at zero on every keystroke is deliberate: the best match should be
// selected as the token narrows, not whatever was highlighted three letters ago.
func (m Model) refreshCompletion() Model {
	next := completionFor(m.input.Value(), m.commands, m.files)
	if next.token != m.completion.token || next.kind != m.completion.kind {
		m.completion = next
		return m.relayout()
	}
	next.cursor = m.completion.cursor
	m.completion = next
	return m
}

// handleCompletionKey returns handled=false for anything the palette does not
// claim, so ordinary typing still reaches the textarea.
func (m Model) handleCompletionKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+p":
		m.completion.cursor = (m.completion.cursor - 1 + len(m.completion.items)) % len(m.completion.items)
		return true, m, nil

	case "down", "ctrl+n":
		m.completion.cursor = (m.completion.cursor + 1) % len(m.completion.items)
		return true, m, nil

	case "tab", "enter":
		m.input.SetValue(applyCompletion(m.input.Value(), m.completion.items[m.completion.cursor]))
		m.input.CursorEnd()
		m.completion = completionState{}
		return true, m.relayout(), nil

	case "esc":
		m.completion = completionState{}
		return true, m.relayout(), nil
	}
	return false, m, nil
}

// applyRemoteCommand runs an instruction from the workspace list. Each one is
// only honoured in the state it makes sense in — the list's view of this
// session is up to a refresh tick stale, so it can ask to allow a permission
// that was already answered here.
func (m Model) applyRemoteCommand(cmd Command) (tea.Model, tea.Cmd) {
	switch cmd.Type {
	case CommandPermission:
		if m.perm == nil {
			return m, nil
		}
		m.perm.reply <- cmd.OptionID
		m.perm = nil
		return m.relayout(), nil

	case CommandInterrupt:
		if m.turn {
			_ = m.client.Cancel(m.sessionID)
		}
		return m, nil

	case CommandPrompt:
		text := strings.TrimSpace(cmd.Text)
		if text == "" || m.turn || m.sessionID == "" {
			return m, nil
		}
		prompt := m.promptCmd(text)
		m.entries = append(m.entries, entry{kind: entryUser, text: text})
		m.turn = true
		m.err = nil
		return m.relayout(), tea.Batch(prompt, spinnerTick())
	}
	return m, nil
}

// stopped reports whether the agent is unusable until something is done about
// it: either it exited, or it refused to work without credentials.
func (m Model) stopped() bool { return m.dead || m.authNeed }

func (m Model) handleStoppedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Login) && len(m.opts.AuthCommand) > 0:
		return m, m.authCmd()

	case key.Matches(msg, m.keys.Restart):
		return m, m.restartCmd()
	}
	return m, nil
}

// authCmd hands the terminal to the agent's own login flow. opentree cannot
// perform the login itself — ACP only reports that one is needed — but it does
// own a terminal, which is exactly what the agent asks for.
func (m Model) authCmd() tea.Cmd {
	c := exec.Command(m.opts.Command, m.opts.AuthCommand...) // #nosec G204 -- from the agent registry, not user input
	c.Dir = m.opts.Cwd
	return tea.ExecProcess(c, func(err error) tea.Msg { return authDoneMsg{err: err} })
}

// handlePermissionKey answers the pending escalation. Options are matched
// against what the agent actually offered rather than a fixed set, because
// agents disagree on which kinds they present.
func (m Model) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	perm := m.perm
	answer := func(optionID string) (tea.Model, tea.Cmd) {
		perm.reply <- optionID
		m.perm = nil
		m = m.relayout()
		return m, nil
	}

	switch msg.String() {
	case "esc", "ctrl+c":
		return answer("")
	}

	if id, ok := optionForKey(msg.String(), perm.req.Options); ok {
		return answer(id)
	}
	return m, nil
}

// optionForKey maps a keystroke to a permission option: a digit picks by
// position, and a/A/d pick by kind when the agent offers that kind.
func optionForKey(k string, options []acp.PermissionOption) (string, bool) {
	if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(options) {
		return options[n-1].OptionID, true
	}

	var wantKind string
	switch k {
	case "a":
		wantKind = acp.PermissionAllowOnce
	case "A":
		wantKind = acp.PermissionAllowAlways
	case "d", "r":
		wantKind = acp.PermissionRejectOnce
	default:
		return "", false
	}
	for _, o := range options {
		if o.Kind == wantKind {
			return o.OptionID, true
		}
	}
	return "", false
}

// applyUpdate folds one agent notification into the conversation log.
func (m Model) applyUpdate(u acp.SessionUpdate) Model {
	switch u.Type {
	case acp.UpdateUserMessage:
		// Only seen during history replay; live user turns are appended on send.
		if u.Message != nil {
			m.entries = append(m.entries, entry{kind: entryUser, text: u.Message.Content.Text})
		}

	case acp.UpdateAgentMessage:
		if u.Message != nil {
			m = m.appendChunk(entryAgent, u.Message.Content.Text)
		}

	case acp.UpdateAgentThought:
		if u.Message != nil {
			m = m.appendChunk(entryThought, u.Message.Content.Text)
		}

	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		if u.ToolCall != nil {
			m = m.upsertToolCall(*u.ToolCall)
		}

	case acp.UpdateCommands:
		m.commands = u.Commands

	case acp.UpdateUsage:
		m.usage = u.Usage
	}
	return m
}

// appendChunk grows the trailing entry when it is the same kind, so a streamed
// message renders as one paragraph rather than one line per token.
func (m Model) appendChunk(kind entryKind, text string) Model {
	if n := len(m.entries); n > 0 && m.entries[n-1].kind == kind {
		m.entries[n-1].text += text
		return m
	}
	m.entries = append(m.entries, entry{kind: kind, text: text})
	return m
}

func (m Model) upsertToolCall(call acp.ToolCall) Model {
	if i, ok := m.toolIdx[call.ToolCallID]; ok {
		m.entries[i].tool.Merge(call)
		return m
	}
	m.toolIdx[call.ToolCallID] = len(m.entries)
	m.entries = append(m.entries, entry{kind: entryTool, tool: call})
	return m
}

func (m Model) appendNotice(text string) Model {
	if text == "" {
		return m
	}
	m.entries = append(m.entries, entry{kind: entryNotice, text: text})
	return m
}

// relayout re-renders the log into the viewport, sizing it around whatever the
// footer currently needs. Scroll position is only forced to the bottom when the
// reader was already there, so scrolling back through history is not yanked
// away by an arriving chunk.
func (m Model) relayout() Model {
	if !m.ready {
		return m
	}
	height := m.height - headerHeight - m.footerHeight()
	if height < 1 {
		height = 1
	}

	atBottom := m.viewport.AtBottom()
	if m.viewport.Width == 0 {
		m.viewport = newViewport(m.width, height)
		atBottom = true
	}
	m.viewport.Width, m.viewport.Height = m.width, height
	m.viewport.SetContent(padToBottom(m.renderLog(), height))
	if atBottom {
		m.viewport.GotoBottom()
	}
	return m
}

// padToBottom pushes a short conversation down against the input box. Without
// it a new session renders its first exchange at the top of the screen with a
// growing void beneath, which reads as broken rather than empty.
func padToBottom(content string, height int) string {
	if pad := height - strings.Count(content, "\n"); pad > 0 {
		return strings.Repeat("\n", pad) + content
	}
	return content
}

func turnSummary(resp *acp.PromptResponse) string {
	if resp == nil {
		return ""
	}
	parts := []string{resp.StopReason}
	if resp.Usage != nil {
		parts = append(parts, fmt.Sprintf("%d in / %d out", resp.Usage.InputTokens, resp.Usage.OutputTokens))
	}
	return strings.Join(parts, " · ")
}

func currentValue(options []acp.ConfigOption, id string) string {
	for _, o := range options {
		if o.ID == id {
			return o.CurrentValue
		}
	}
	return ""
}
