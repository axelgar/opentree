package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.err == nil {
			m.err = fmt.Errorf("%s exited", m.opts.Command)
		}
		m.turn = false
		return m, nil

	case errMsg:
		m.err = msg.err
		m.turn = false
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

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

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
		m.input.Reset()
		m.entries = append(m.entries, entry{kind: entryUser, text: text})
		m.turn = true
		m.err = nil
		m = m.relayout()
		return m, tea.Batch(m.promptCmd(text), spinnerTick())
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
