package chat

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/tmux"
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
	// Ordered like the chat's own overlays, so the badge in the list names the
	// panel the window is actually showing.
	st := Status{Workspace: m.opts.Workspace, State: StateIdle}
	switch {
	case m.perm() != nil:
		st.State = StateAwaiting
		st.Permission = &Permission{
			Title:   toolLabel(m.perm().req.ToolCall, m.opts.Cwd),
			Options: m.perm().req.Options,
		}
	case m.dead || m.authNeed:
		st.State = StateStopped
	case m.turn:
		st.State = StateWorking
	case m.sessionID == "":
		st.State = StateStarting
	}

	if m.queued != "" {
		st.Queued = m.queued
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

	// Having captured the mouse, the wheel has to do something: the viewport
	// scrolls itself, three lines at a time, whatever panel the footer shows.
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case acpUpdateMsg:
		m = m.applyUpdate(acp.SessionUpdate(msg))
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case permissionMsg:
		// Hold the escalation; the agent stays blocked on its reply channel
		// until a key answers it. A second one that arrives while the first is
		// on screen queues behind it rather than taking its place: both are
		// blocked on channels of their own, and the one overwritten would never
		// be answered — its turn would never end.
		m.perms = append(m.perms, msg)
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case sessionReadyMsg:
		m.sessionID = msg.id
		m.configOptions = msg.options
		if msg.note != "" {
			m = m.appendNotice(msg.note)
		}
		m = m.relayout()
		return m.flushQueued()

	case promptDoneMsg:
		m.turn = false
		if msg.err != nil {
			m.err = msg.err
			m.queued = "" // a queued prompt must not fire into a broken session
			return m, nil
		}
		// A turn that began before this model existed — a resumed session, or a
		// test — has no start, and time.Since(zero) would report decades.
		var elapsed time.Duration
		if !m.turnStart.IsZero() {
			elapsed = time.Since(m.turnStart)
		}
		m = m.appendNotice(turnSummary(msg.resp, elapsed))
		m = m.relayout()
		return m.flushQueued()

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
		// Nothing can act on an answer now, so release whatever was blocked on
		// one rather than leaving a dialog up that offers to allow a tool call
		// which will never run.
		m = m.cancelPerms()
		if m.err == nil {
			m.err = fmt.Errorf("%s exited", m.opts.Command)
		}
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case clientReadyMsg:
		m.client = msg.client
		m.generation = msg.generation
		m = m.withAgentInfo(msg.info)
		m.dead, m.authNeed, m.err, m.restarting = false, false, nil, false
		// The replay rebuilds the log from scratch, so drop what is on screen
		// rather than rendering the conversation twice.
		m.entries, m.toolIdx = nil, make(map[string]int)
		m = m.relayout()
		return m, m.startSession()

	case filesLoadedMsg:
		m.files = msg.files
		return m, nil

	case pastedImageMsg:
		if msg.err != "" {
			m = m.appendNotice(msg.err)
			return m.relayout(), nil
		}
		// Into the message rather than beside it: the label is where the cursor
		// was, so an image can be pasted mid-sentence, and what is on screen is
		// what will be sent.
		m.pending = append(m.pending, msg.block)
		m.input.InsertString(imageLabel(msg.block) + " ")
		m = m.refreshCompletion()
		return m.relayout(), nil

	case configChangedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("could not set %s: %w", msg.configID, msg.err)
			return m.relayout(), nil
		}
		if len(msg.options) > 0 {
			m.configOptions = msg.options
		}
		// No log entry: the new value is on screen permanently, and a notice
		// per flip would bury the conversation under its own settings. It would
		// not survive a resume either, since the log replays from the agent.
		return m.relayout(), nil

	case socketCommandMsg:
		// Also arrives down the handler channel, so the reader has to be handed
		// back alongside whatever the command itself triggers.
		next, cmd, res := m.applyRemoteCommand(msg.cmd)
		if msg.reply != nil {
			msg.reply <- res
		}
		return next, tea.Batch(cmd, waitForMsg(m.msgs))

	case authDoneMsg:
		// ExecProcess's RestoreTerminal re-enters the alt screen but drops mouse
		// mode, so coming back from the login flow leaves the wheel scrolling the
		// host terminal again until it is turned back on (see commit ebc72b9,
		// which found this on the dashboard's attach path).
		if msg.err != nil {
			m.err = msg.err
			return m, tea.EnableMouseCellMotion
		}
		// Credentials are read at startup, so a fresh login needs a fresh agent.
		m.restarting = true
		return m, tea.Batch(tea.EnableMouseCellMotion, m.restartCmd())

	case errMsg:
		m.err = msg.err
		m.authNeed = msg.auth
		m.turn = false
		// A restart that failed is over; r has to work again.
		m.restarting = false
		if msg.fatal {
			m.dead = true
		}
		m = m.relayout()
		return m, nil
	}

	// The textarea's own paste lands here, since its message type is private to
	// it. Nothing else in this branch changes the text, so a change is a paste —
	// and a path pasted with ctrl+v should become an attachment exactly like one
	// that was dragged in.
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m = m.attachDropped()
	}
	return m, cmd
}

// leave hands the terminal back to opentree's workspace list without stopping
// the chat: the conversation, the agent and the status the list reads all
// outlive looking away, and the window stays there to come back to. Quitting is
// the fallback for a chat nobody attached to — run by hand, or in a window from
// before opentree recorded the way back — where there is nowhere to return to.
func leave() tea.Msg {
	if tmux.ReturnToList() {
		return nil
	}
	return tea.Quit()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Whichever panel the footer drew is the one the keys drive. A stopped
	// agent takes over the keyboard because r and l would otherwise be
	// swallowed by the textarea, which is useless with nothing to send to.
	switch m.overlay() {
	case overlayPermission:
		return m.handlePermissionKey(msg)
	case overlayStopped:
		return m.handleStoppedKey(msg)
	case overlaySettings:
		return m.handleSettingsKey(msg)
	case overlayHelp:
		// The key list is read-only, so anything at all dismisses it rather
		// than making people hunt for the one key that closes it.
		if key.Matches(msg, m.keys.Back) {
			return m, leave
		}
		m.showHelp = false
		return m.relayout(), nil
	}

	// The palette owns navigation and acceptance while it is open, so arrows
	// and tab do not fall through to scrolling or the textarea.
	if m.completion.active() {
		if handled, model, cmd := m.handleCompletionKey(msg); handled {
			return model, cmd
		}
	}

	switch {
	case key.Matches(msg, m.keys.Back):
		return m, leave

	case key.Matches(msg, m.keys.Settings):
		return m.openSettings()

	case key.Matches(msg, m.keys.CycleMode):
		return m.cycleMode()

	case key.Matches(msg, m.keys.Thoughts):
		m.hideThoughts = !m.hideThoughts
		m = m.relayout()
		return m, nil

	// Taken from the textarea, which binds ctrl+v to pasting text. The command
	// hands it straight back when the clipboard holds no image, so the key does
	// what it always did and gains a second meaning rather than losing its first.
	case key.Matches(msg, m.keys.Paste):
		return m, pasteCmd()

	// Guarded on an empty message: "?" is a character before it is a key, and
	// a chat that cannot type a question mark is a bad trade for a shortcut.
	case key.Matches(msg, m.keys.Help) && m.input.Value() == "":
		m.showHelp = true
		return m.relayout(), nil

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
		if text == "" {
			return m, nil
		}
		// opentree's own commands never reach the agent.
		if configID, ok := m.clientCommandFor(text); ok {
			m.input.Reset()
			m.completion = completionState{}
			return m.openSettingsAt(configID)
		}
		if m.turn || m.sessionID == "" {
			return m, nil
		}
		m.input.Reset()
		m.completion = completionState{}
		return m.startTurn(text)
	}

	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m = m.healLabels(before)
	// Only on a paste. Dragging a file onto a terminal arrives as one bracketed
	// paste, while a path typed by hand would turn into a label the instant it
	// became a real filename — mid-word, with the text it replaced gone.
	if msg.Paste {
		m = m.attachDropped()
	}
	m = m.refreshCompletion()
	return m, cmd
}

// attachDropped turns an image path that just landed in the message into an
// attachment, so a file dragged onto the terminal reads the same as one pasted
// off the clipboard rather than as eighty characters of /Users/…
//
// ponytail: the trailing word, which is the same limit the completion palette
// takes. A drop lands at the cursor and the cursor is at the end. One dropped
// into the middle of a sentence still travels as an image; it just keeps
// looking like a path until it is sent.
func (m Model) attachDropped() Model {
	if !m.canSendImages {
		return m
	}
	runes := []rune(m.input.Value())
	start, end := lastWord(runes)
	if start < 0 {
		return m
	}
	block, _, ok := attach(string(runes[start:end]), m.opts.Cwd, m.trackedFiles(), true)
	if !ok || block.Type != acp.BlockImage {
		// A path to something that is not an image reads perfectly well as a
		// path, and it already travels as a link.
		return m
	}
	m.pending = append(m.pending, block)
	m.input.SetValue(string(runes[:start]) + imageLabel(block) + string(runes[end:]))
	m.input.CursorEnd()
	return m
}

// lastWord is the span of the final word, honouring the backslash escapes a
// dragged path arrives wearing.
func lastWord(runes []rune) (start, end int) {
	start, end = -1, -1
	for i := 0; i < len(runes); {
		if isBoundary(runes[i]) {
			i++
			continue
		}
		j := wordEnd(runes, i)
		start, end = i, j
		i = j
	}
	return start, end
}

// healLabels removes an attachment's label whole once a keystroke has broken
// it. A label reads as one thing and has to behave like one: twenty-five
// backspaces to take back one paste is not an undo, and the twenty-four states
// in between are gibberish.
//
// Nothing needs to be dropped from m.pending — a block whose label is not in
// the message never makes it into the prompt.
//
// ponytail: single-line messages, which is where a paste lands. Repairing
// across a newline means mapping a rune offset onto the textarea's wrapped
// rows, and SetCursor only addresses one row; there a label erases by hand.
func (m Model) healLabels(before string) Model {
	after := m.input.Value()
	if after == before || strings.ContainsRune(after, '\n') {
		return m
	}
	for _, c := range m.pending {
		label := imageLabel(c)
		at := strings.Index(before, label)
		if at < 0 || strings.Contains(after, label) {
			continue
		}
		// The edit landed inside this label. Take back the whole region it
		// occupied, grown or shrunk by whatever the keystroke did to it.
		runes := []rune(after)
		start := utf8.RuneCountInString(before[:at])
		end := start + utf8.RuneCountInString(label) + (len(runes) - utf8.RuneCountInString(before))
		start, end = min(start, len(runes)), min(max(end, 0), len(runes))
		if start > end {
			continue
		}
		m.input.SetValue(string(runes[:start]) + string(runes[end:]))
		m.input.SetCursor(start)
		return m
	}
	return m
}

// refreshCompletion recomputes the palette from whatever is now typed. Keeping
// the cursor at zero on every keystroke is deliberate: the best match should be
// selected as the token narrows, not whatever was highlighted three letters ago.
func (m Model) refreshCompletion() Model {
	next := completionFor(m.input.Value(), m.paletteCommands(), m.files)
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
func (m Model) applyRemoteCommand(cmd Command) (tea.Model, tea.Cmd, Result) {
	switch cmd.Type {
	case CommandPermission:
		if m.perm() == nil {
			return m, nil, Result{Reason: "nothing is waiting on permission"}
		}
		return m.answerPerm(cmd.OptionID), nil, Result{OK: true}

	case CommandInterrupt:
		if !m.turn {
			return m, nil, Result{Reason: "the agent is not working"}
		}
		_ = m.client.Cancel(m.sessionID)
		return m, nil, Result{OK: true}

	case CommandPrompt:
		text := strings.TrimSpace(cmd.Text)
		if text == "" {
			return m, nil, Result{Reason: "empty prompt"}
		}
		// A prompt that cannot run yet is queued rather than refused: the
		// alternative is telling someone to retry a message they already typed.
		// It is shown in the log and in the list's badge, so a prompt firing
		// later is something you watched arrive, not a surprise.
		if m.sessionID == "" || m.turn {
			if m.queued != "" {
				return m, nil, Result{Reason: "a prompt is already queued"}
			}
			m.queued = text
			m = m.appendNotice("queued: " + text)
			return m.relayout(), nil, Result{OK: true}
		}
		next, cmd := m.startTurn(text)
		return next, cmd, Result{OK: true}
	}
	return m, nil, Result{Reason: "unknown command " + cmd.Type}
}

// startTurn sends a message to the agent and records it in the log.
//
// Composing here rather than inside promptCmd is what keeps the two honest with
// each other: the log shows what was sent, not what was typed, so an attachment
// that quietly became a link says so on the line you can see.
func (m Model) startTurn(text string) (Model, tea.Cmd) {
	blocks, notices := m.composeTurn(text)
	m.pending = nil

	cmd := m.promptCmd(blocks)
	m.entries = append(m.entries, entry{kind: entryUser, text: echo(blocks)})
	for _, n := range notices {
		m = m.appendNotice(n)
	}
	m.turn = true
	m.turnStart = time.Now()
	m.err = nil
	return m.relayout(), tea.Batch(cmd, spinnerTick())
}

// composeTurn is the message about to be sent, as blocks. It exists so the
// state that feeds composePrompt — the pasted attachments, what this agent
// accepts, which files git knows about — is reachable from a test without
// executing the command that would need a live agent behind it.
func (m Model) composeTurn(text string) ([]acp.ContentBlock, []string) {
	return composePrompt(text, m.opts.Cwd, m.trackedFiles(), m.canSendImages, m.pending)
}

// flushQueued runs a prompt that arrived while the agent was busy or starting.
func (m Model) flushQueued() (tea.Model, tea.Cmd) {
	if m.queued == "" || m.sessionID == "" || m.turn {
		return m, nil
	}
	text := m.queued
	m.queued = ""
	return m.startTurn(text)
}

// stopped reports whether the agent is unusable until something is done about
// it: either it exited, or it refused to work without credentials.
func (m Model) stopped() bool { return m.dead || m.authNeed }

func (m Model) handleStoppedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		return m, leave

	case key.Matches(msg, m.keys.Login) && m.canLogIn():
		return m, m.authCmd()

	case key.Matches(msg, m.keys.Restart):
		if m.restarting {
			return m, nil
		}
		m.restarting = true
		return m.relayout(), m.restartCmd()
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
	switch msg.String() {
	case "esc", "ctrl+c":
		return m.answerPerm(""), nil
	}

	if id, ok := optionForKey(msg.String(), m.perm().req.Options); ok {
		return m.answerPerm(id), nil
	}
	return m, nil
}

// answerPerm replies to the escalation on screen and brings up whatever queued
// behind it. Exactly one value reaches each reply channel, which is the whole
// contract: the agent's request is blocked on it.
func (m Model) answerPerm(optionID string) Model {
	m.perms[0].reply <- optionID
	m.perms = m.perms[1:]
	return m.relayout()
}

// cancelPerms declines everything still waiting, for when the agent that asked
// is gone. Each reply channel is buffered, so this never blocks on a reader
// that has already given up.
func (m Model) cancelPerms() Model {
	for _, p := range m.perms {
		p.reply <- ""
	}
	m.perms = nil
	return m
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
			m = m.replayUserChunk(u.Message.Content)
		}

	case acp.UpdateAgentMessage:
		if u.Message != nil {
			m = m.appendChunk(entryAgent, blockText(u.Message.Content))
		}

	case acp.UpdateAgentThought:
		if u.Message != nil {
			m = m.appendChunk(entryThought, blockText(u.Message.Content))
		}

	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		if u.ToolCall != nil {
			m = m.upsertToolCall(*u.ToolCall)
		}

	case acp.UpdateCommands:
		m.commands = u.Commands

	case acp.UpdateUsage:
		m.usage = u.Usage

	case acp.UpdatePlan:
		m = m.upsertPlan(u.Plan)

	case acp.UpdateMode, acp.UpdateConfigOptions:
		// The agent changed its own settings — answering its plan-mode dialog,
		// or narrowing the effort levels after a model switch. Without these the
		// flags beside the input keep reporting what was last asked for, which
		// after "yes, and auto-accept edits" still reads "plan" while edits no
		// longer stop for permission.
		if len(u.ConfigOptions) > 0 {
			m.configOptions = u.ConfigOptions
		}
		if u.CurrentModeID != "" {
			m.configOptions = withModeValue(m.configOptions, u.CurrentModeID)
		}
	}
	return m
}

// upsertPlan keeps the agent's plan as one entry, patched where it stands. The
// whole list arrives on every change — six times for a three-item plan — and
// appending each would bury the conversation under its own table of contents.
func (m Model) upsertPlan(entries []acp.PlanEntry) Model {
	if len(entries) == 0 {
		return m
	}
	for i := range m.entries {
		if m.entries[i].kind == entryPlan {
			m.entries[i].plan = entries
			return m
		}
	}
	m.entries = append(m.entries, entry{kind: entryPlan, plan: entries})
	return m
}

// withModeValue records the mode the agent says it is now in. The mode is one
// of the declared config options, so this keeps it where the picker and the
// flags already look rather than adding a second place to hold it.
func withModeValue(options []acp.ConfigOption, mode string) []acp.ConfigOption {
	out := make([]acp.ConfigOption, len(options))
	copy(out, options)
	for i := range out {
		if out[i].Category == categoryMode {
			out[i].CurrentValue = mode
		}
	}
	return out
}

// replayUserChunk folds one chunk of a replayed message back into the log.
//
// A replay is not what was typed. opencode splits one message into several
// chunks under a single messageId, and the ones the agent wrote to itself
// arrive here too: the input it handed a tool, and whole files it inlined,
// both addressed to the assistant. Appending each as its own message handed
// back a conversation nobody had, with the file quoted inside it — on every
// reopen, since a chat loads its session on every attach.
func (m Model) replayUserChunk(c acp.ContentBlock) Model {
	if !c.ForUser() {
		return m
	}
	text := blockText(c)
	// A replay arrives one block per notification, so the joining echo does in
	// one pass happens here across several. Without it a reopened conversation
	// reads "[image · 303 bytes]describe this image" — the same message the
	// live path had already spaced correctly.
	if n := len(m.entries); text != "" && n > 0 && m.entries[n-1].kind == entryUser {
		text = separator(m.entries[n-1].text, text) + text
	}
	return m.appendChunk(entryUser, text)
}

// appendChunk grows the trailing entry when it is the same kind, so a streamed
// message renders as one paragraph rather than one line per token.
//
// Nothing to say means no entry: a block opentree does not render still arrives,
// and an empty entry draws a full-width band, or a coloured bullet on a line of
// its own, with nothing in it.
func (m Model) appendChunk(kind entryKind, text string) Model {
	if text == "" {
		return m
	}
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
// footer currently needs.
//
// The conversation is anchored to the top and grows downward, the way anything
// printed to a terminal does; once it outgrows the viewport the oldest lines
// scroll off the top on their own. Scroll position is only forced to the bottom
// when the reader was already there, so reading back through history is not
// yanked away by an arriving chunk.
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
	m.viewport.SetContent(m.renderLog())
	if atBottom {
		m.viewport.GotoBottom()
	}
	return m
}

// turnSummary is the line under a finished turn. The stop reason only earns a
// place when it is not the ordinary one: "end_turn" printed under every answer
// is protocol vocabulary sitting where "how long did that take" should be.
func turnSummary(resp *acp.PromptResponse, elapsed time.Duration) string {
	if resp == nil {
		return ""
	}
	var parts []string
	if why := stopReasonText(resp.StopReason); why != "" {
		parts = append(parts, why)
	}
	if elapsed > 0 {
		parts = append(parts, elapsed.Truncate(100*time.Millisecond).String())
	}
	if u := resp.Usage; u != nil {
		tokens := fmt.Sprintf("%d in / %d out", u.InputTokens, u.OutputTokens)
		// Cache traffic is most of a real turn and neither of the other two
		// counts it: a live opencode turn reported 1 in and 19 out against
		// 12,056 written to cache, which read as a turn that did nothing.
		if cached := u.CachedReadTokens + u.CachedWriteTokens; cached > 0 {
			tokens += fmt.Sprintf(" / %d cached", cached)
		}
		parts = append(parts, tokens)
	}
	return strings.Join(parts, " · ")
}

// stopReasonText says why a turn ended short, in words. The protocol's own
// vocabulary printed raw is no help: "max_turn_requests" also covers running
// out of budget, which is not what the phrase suggests.
func stopReasonText(reason string) string {
	switch reason {
	case acp.StopEndTurn, "":
		return "" // the ordinary one, and saying it every time says nothing
	case acp.StopCancelled:
		return "interrupted"
	case acp.StopMaxTokens:
		return "stopped: the reply hit its token limit"
	case acp.StopMaxTurnRequests:
		return "stopped: hit the turn or budget limit"
	case acp.StopRefusal:
		return "the agent declined to continue"
	}
	return reason
}
