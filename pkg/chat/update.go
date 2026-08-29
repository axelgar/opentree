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
	"github.com/axelgar/opentree/pkg/notify"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/ui"
)

// Update handles one message and republishes the session's status. Publishing
// here rather than at each call site is the whole point: every state change
// funnels through this method, so the socket cannot drift out of date.
//
// It is also where the edge is: the newly computed state is compared against
// the last one published, which is the one observation both Status.Since and
// anything watching this session for a moment worth carrying need.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}

	st := nm.status()
	if st.State != m.state {
		nm.state, nm.since = st.State, time.Now()
	}
	st.Since = nm.since

	if nm.publish != nil {
		nm.publish(st)
	}
	if nm.opts.Notify != nil {
		nm.opts.Notify(signalOf(st))
	}
	return nm, cmd
}

// signalOf is this session as a notifier sees it.
//
// The mapping is written out rather than passing the state string through:
// notify keeps a vocabulary of its own on purpose — a notifier that imported
// this package could not be tested without an agent to run — and this is the
// one place the two meet.
//
// A setup phase that failed reads as a stopped agent, because that is what it
// is to anyone waiting on the window: nothing further will happen there until
// somebody deals with it.
func signalOf(st Status) notify.Signal {
	switch {
	case st.State == StateAwaiting:
		title := ""
		if st.Permission != nil {
			title = st.Permission.Title
		}
		return notify.Signal{State: notify.StateBlocked, Detail: title}
	case st.State == StateSettingUp && st.Error != "":
		return notify.Signal{State: notify.StateStopped, Detail: st.Error}
	// The approval question is a human question, and blocked is the signal
	// humans have turned on; a running check or publish reads as working, so
	// done fires once when the whole loop settles rather than in the gap
	// between the turn ending and the check starting.
	case st.State == StateChecking && st.Autopilot != nil && st.Autopilot.Phase == "asking":
		return notify.Signal{State: notify.StateBlocked, Detail: "approve the check command"}
	case st.State == StateChecking:
		return notify.Signal{State: notify.StateWorking}
	case st.State == StateStopped:
		return notify.Signal{State: notify.StateStopped}
	case st.State == StateWorking:
		return notify.Signal{State: notify.StateWorking}
	case st.State == StateIdle:
		return notify.Signal{State: notify.StateIdle}
	}
	return notify.Signal{State: notify.StateOther}
}

// status is the view of this session the workspace list sees.
func (m Model) status() Status {
	// Ordered like the chat's own overlays, so the badge in the list names the
	// panel the window is actually showing.
	//
	// Stamped with what this window is, as well as what it is doing: a chat
	// outlives every upgrade, so the reader may be a newer binary than the
	// writer and has no other way to find out.
	st := Status{
		Workspace: m.opts.Workspace,
		State:     StateIdle,
		Protocol:  ProtocolVersion,
		Version:   m.opts.Version,
	}
	switch {
	case m.perm() != nil:
		st.State = StateAwaiting
		st.Permission = &Permission{
			Title:      toolLabel(m.perm().req.ToolCall, m.opts.Cwd),
			Options:    m.perm().req.Options,
			ToolCallID: m.perm().req.ToolCall.ToolCallID,
		}
	case m.setup.active():
		st.State = StateSettingUp
		// A setup failure belongs in opentree's error log, which is the
		// dashboard's. It is the only way it reaches someone who is not looking
		// at this window — and the window may be one nobody ever attached to.
		if m.setup.stage == setupFailed {
			st.Error = m.opts.Workspace + ": " + setupErrorText(m.setup.err)
		}
	case m.dead || m.authNeed:
		st.State = StateStopped
	case m.auto.active():
		st.State = StateChecking
	case m.turn:
		st.State = StateWorking
	case m.sessionID == "":
		st.State = StateStarting
	}

	if m.auto.enabled() {
		st.Autopilot = &AutopilotStatus{
			Enabled:   m.auto.enabled(),
			Phase:     autopilotPhaseName(m.auto.stage),
			Iteration: m.auto.iterations,
			PRURL:     m.auto.prURL,
		}
		if m.auto.published {
			st.Autopilot.Outcome = "published"
		}
		// The loop's failures travel the same road setup's do: into the error
		// log of a dashboard whose window this may never be.
		if st.Error == "" {
			switch {
			case m.auto.stage == autoHalted:
				st.Error = fmt.Sprintf("%s: autopilot halted — the check is still failing after %d attempts",
					m.opts.Workspace, m.auto.iterations)
			case m.auto.err != nil:
				st.Error = m.opts.Workspace + ": autopilot: " + m.auto.err.Error()
			}
		}
	}

	// The badge reports the head of the queue — the message that fires next —
	// which keeps the socket's shape from before the queue could hold more
	// than one.
	if len(m.queue) > 0 {
		st.Queued = m.queue[0].text
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
	// namedKey first, so a key the terminal spelled out in full arrives at
	// the switch under a name the bindings can be written against.
	switch msg := namedKey(msg).(type) {

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
		return m.relayout(), cmd

	case acpUpdateMsg:
		m = m.applyUpdate(acp.SessionUpdate(msg))
		// The agent's commands arrive after the session opens, often while the
		// palette is already on screen showing only opentree's own — an open
		// palette has to take them.
		m = m.refreshCompletion()
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
		m.configOptions, m.classicModes = withClassicModes(msg.options, msg.modes)
		m.titled = msg.resumed
		if msg.note != "" {
			m = m.appendNotice(msg.note)
		}
		m = m.refreshCompletion()
		m = m.relayout()
		return m.flushQueued()

	case promptDoneMsg:
		m.turn = false
		if msg.err != nil {
			m.err = msg.err
			// A queued prompt must not fire into a broken session. Each is
			// dropped by name — the text is one ↑ away — rather than silently.
			for _, q := range m.queue {
				m = m.appendNotice("not sent: " + q.text)
			}
			m.queue = nil
			return m.relayout(), nil
		}
		// A turn that began before this model existed — a resumed session, or a
		// test — has no start, and time.Since(zero) would report decades.
		var elapsed time.Duration
		if !m.turnStart.IsZero() {
			elapsed = time.Since(m.turnStart)
		}
		m = m.appendNotice(turnSummary(msg.resp, elapsed))
		m = m.relayout()
		return m.afterTurn(msg.resp)

	case checkOutputMsg:
		m = m.appendSetupLine(msg.line)
		return m.relayout(), waitForMsg(m.msgs)

	case checkDoneMsg:
		return m.finishCheck(msg)

	case publishDoneMsg:
		return m.finishPublish(msg)

	case autoTickMsg:
		return m.handleAutoTick()

	case autoPollResultMsg:
		return m.handleAutoPollResult(msg)

	case setupBeginMsg:
		return m.beginSetup()

	case setupStepMsg:
		m.setup.at = msg.at
		return m, waitForMsg(m.msgs)

	case setupOutputMsg:
		m = m.appendSetupLine(msg.line)
		return m.relayout(), waitForMsg(m.msgs)

	case setupDoneMsg:
		// The summary goes in the log, never to the agent. Whether it should see
		// a failed install is the user's call, and pasting it is one action they
		// can take themselves.
		m = m.appendNotice(setupSummary(m.setup.spec.Commands, msg.err))
		return m.finishSetup(msg.err)

	case spinnerTickMsg:
		// The setup phase spins for the same reason a turn does: something is
		// running that prints nothing for minutes at a time. So does a check.
		if !m.turn && m.setup.stage != setupRunning && !m.auto.running() {
			// The chain ends here, so whatever needs one next has to be free to
			// start a fresh one. Left set, this would be a spinner that ran for
			// the first turn of a session and stood still for every one after.
			m.spinning = false
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(ui.SpinnerFrames)
		// The frame lives inside rendered entries — the thinking line and every
		// running tool's glyph — and the viewport caches whatever the last
		// relayout gave it. Without a relayout here the spinner only moves when
		// the agent happens to say something, which is exactly when nobody
		// needs reassurance that it is alive.
		return m.relayout(), spinnerTick()

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
			m.err = fmt.Errorf("%s exited", m.opts.Agent.Command)
		}
		// Whatever the agent printed on its way out, into the log where it can be
		// read. The stopped panel keeps one line — the rest would swamp a footer —
		// and an agent that died explaining why deserves better than to have that
		// explanation truncated at the terminal's width.
		m = m.appendNotice(m.agentOutput())
		m = m.relayout()
		return m, waitForMsg(m.msgs)

	case clientReadyMsg:
		m.client = msg.client
		m.generation = msg.generation
		m = m.withAgentInfo(msg.info)
		m.dead, m.authNeed, m.err, m.restarting = false, false, nil, false
		m.launching = false
		m.setup.stage = setupNone
		// The replay rebuilds the log from scratch, so drop what is on screen
		// rather than rendering the conversation twice.
		if !msg.keepLog {
			m.entries, m.toolIdx = nil, make(map[string]int)
		}
		m = m.relayout()
		return m, m.startSession()

	case filesLoadedMsg:
		return m.withFiles(msg.files), nil

	case sessionsListedMsg:
		// The picker may already be gone — the list is a round trip and esc is
		// instant — in which case its answer is stale by definition.
		if !m.sessions.open {
			return m, nil
		}
		m.sessions.loading = false
		if msg.err != nil {
			// Not an error banner: the recorded conversations are still listed
			// underneath, and one of them is probably the one being looked for.
			m.sessions.err = "could not list " + m.opts.Agent.Name + "'s own"
			return m.relayout(), nil
		}
		m.sessions.rows = mergeSessions(msg.sessions, m.opts.KnownSessions)
		m.sessions.cursor = min(m.sessions.cursor, max(len(m.sessions.rows)-1, 0))
		return m.relayout(), nil

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

	case authenticatedMsg:
		m.loggingIn = false
		if msg.err != nil {
			// The agent's own words: "Gemini API key is missing or not
			// configured" is the whole answer, and opentree has nothing to add.
			m.err = msg.err
			return m.relayout(), nil
		}
		// This agent logged itself in, so unlike the terminal flow there is no
		// restart to do — the process holding the new credentials is the one
		// already running.
		m.dead, m.authNeed, m.err = false, false, nil

		// A conversation that already exists keeps going under the new
		// credentials; reopening it would replay its whole history into a log
		// that already has it. Only a chat that never got a session — which is
		// what a login blocks — has one to start, and that is also the answer to
		// whether the login took: an agent that only claimed to log in fails here.
		if m.sessionID != "" {
			return m.appendNotice("logged in to " + m.opts.Agent.Name).relayout(), nil
		}
		return m.relayout(), m.startSession()

	case errMsg:
		m.err = msg.err
		m.authNeed = msg.auth
		m.turn = false
		// A restart that failed is over; r has to work again.
		m.restarting = false
		// A first launch that failed is over too. Left set, its panel would sit
		// above the stopped one — which is the panel that says what went wrong
		// and offers the restart.
		m.launching = false
		// An agent that would not start after the setup phase is a stopped
		// agent, not a phase still in progress.
		m.setup.stage = setupNone
		if msg.fatal {
			m.dead = true
		}
		// The whole failure into the log, where there is room for it. The panels
		// that report an error have one line each and keep the first, which is
		// the cause; an adapter that failed to start explains itself in the lines
		// after it — acp folds its stderr into the error precisely so that
		// explanation travels — and truncating those at the terminal's width is
		// throwing away the only account of what went wrong. There is no client
		// on this path, so agentOutput, which reads the live process's stderr,
		// has nothing to say here.
		m = m.appendNotice(errorDetail(msg.err))
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
	if def, ok := overlayDefs[m.overlay()]; ok {
		return def.keys(m, msg)
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

	case key.Matches(msg, m.keys.Expand):
		return m.toggleExpand(), nil

	case key.Matches(msg, m.keys.Retry):
		return m.retryTurn()

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
			return m, nil
		}
		// With no turn to interrupt, esc clears a message not yet sent. It is
		// recorded first, so ↑ undoes the clear; pasted images go with their
		// labels, exactly as if the labels had been deleted by hand.
		if strings.TrimSpace(m.input.Value()) != "" {
			m = m.sent(m.input.Value())
			m.pending = nil
		}
		return m, nil

	// Scrolling relayouts because the footer grows a line when the reader
	// leaves the bottom. Without it the viewport keeps its old height and
	// overlaps the input box.
	case key.Matches(msg, m.keys.PageUp):
		m.viewport.PageUp()
		return m.relayout(), nil

	case key.Matches(msg, m.keys.PageDown):
		m.viewport.PageDown()
		return m.relayout(), nil

	case key.Matches(msg, m.keys.ScrollUp):
		m.viewport.HalfPageUp()
		return m.relayout(), nil

	case key.Matches(msg, m.keys.ScrollDn):
		m.viewport.HalfPageDown()
		return m.relayout(), nil

	// The message box keeps the arrows while there is somewhere to go inside
	// it, and only lends them out from its edges: up recalls from the first
	// row, down moves on from the last. A one-line message — which is most of
	// them — is on both edges at once, so the arrows recall on every press,
	// while a recalled paragraph can still be walked through and edited.
	case key.Matches(msg, m.keys.HistoryPrev) && m.atFirstRow():
		return m.recall(-1)

	case key.Matches(msg, m.keys.HistoryNext) && m.history.walking() && m.atLastRow():
		return m.recall(+1)

	case key.Matches(msg, m.keys.Send):
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		// opentree's own commands never reach the agent.
		if run, ok := m.clientCommandFor(text); ok {
			return run(m.sent(text))
		}
		// A message typed while the agent is working queues instead of silently
		// not sending — visibly, above the box, and one flushes per finished
		// turn. The images leave with it now: captured at enter, so deleting a
		// label later cannot reach into a message already committed to.
		if m.turn || m.sessionID == "" {
			m = m.sent(text)
			m.queue = append(m.queue, queuedPrompt{text: text, images: m.pending, source: typedHere})
			m.pending = nil
			return m.relayout(), nil
		}
		return m.sent(text).startTurn(text, typedHere)

	// Backspace on an empty box takes the newest queued message back to be
	// edited — or just deleted, which is the same gesture one keypress longer.
	// After the palette and overlays: their backspace is their own.
	case msg.Type == tea.KeyBackspace && strings.TrimSpace(m.input.Value()) == "" && len(m.queue) > 0:
		q := m.queue[len(m.queue)-1]
		m.queue = m.queue[:len(m.queue)-1]
		m.input.SetValue(q.text)
		m.input.CursorEnd()
		m.pending = append(m.pending, q.images...)
		return m.relayout(), nil
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

// sent empties the message box now that its contents have gone, and files them
// for the arrows to find again.
func (m Model) sent(text string) Model {
	m.history = m.history.record(text)
	m.input.Reset()
	m.completion = completionState{}
	return m
}

// recall puts a message sent earlier back in the box.
//
// The palette closes rather than reopening on whatever the remembered message
// ends with: recalling is not typing, and a message that ended in "@main.go"
// should come back as it was sent, not with six files offered under it.
//
// ponytail: the text, which is all a message box can hold. A remembered message
// that carried a pasted image comes back with the label the image left behind,
// and the label is what would be sent — the bytes went with the message that
// carried them. Attaching it again is one ctrl+v.
func (m Model) recall(delta int) (tea.Model, tea.Cmd) {
	next, text, ok := m.history.walk(delta, m.input.Value())
	if !ok {
		return m, nil
	}
	m.history = next
	// SetValue leaves the cursor at the end of what it inserted, which is where
	// someone who pressed up to change the last word wants it.
	m.input.SetValue(text)
	m.completion = completionState{}
	return m.relayout(), nil
}

// atFirstRow and atLastRow report where the cursor sits in the message box,
// counting the rows the box wrapped a long line onto rather than only the lines
// that were typed — a paragraph folded onto three rows is three presses tall
// whether or not anybody pressed ctrl+j.
func (m Model) atFirstRow() bool {
	return m.input.Line() == 0 && m.input.LineInfo().RowOffset == 0
}

func (m Model) atLastRow() bool {
	li := m.input.LineInfo()
	return m.input.Line() == m.input.LineCount()-1 && li.RowOffset == li.Height-1
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
	block, _, ok := m.composer().attach(string(runes[start:end]))
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
	// Same token, new matches: the list can grow or shrink under an open
	// palette when the agent's commands land, so the cursor is clamped rather
	// than trusted.
	next.cursor = min(m.completion.cursor, max(0, len(next.items)-1))
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
		// Answer the question that was asked. The sender saw a permission at
		// most one refresh tick ago; if it has since been answered here and
		// another has taken its place, applying the answer anyway allows a
		// tool nobody looked at.
		//
		// Only when the sender said which one. An empty id is a dashboard
		// older than this chat, and refusing all of those would break remote
		// answering entirely for anyone mid-upgrade.
		if cmd.ToolCallID != "" && cmd.ToolCallID != m.perm().req.ToolCall.ToolCallID {
			return m, nil, Result{Reason: "that permission has already been answered"}
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
			m.queue = append(m.queue, queuedPrompt{text: text, source: sentRemotely})
			m = m.appendNotice("queued: " + text)
			return m.relayout(), nil, Result{OK: true}
		}
		next, cmd := m.startTurn(text, sentRemotely)
		return next, cmd, Result{OK: true}

	case CommandAutopilot:
		switch cmd.Text {
		case "on", "off":
			next, reason := m.setAutopilot(cmd.Text == "on")
			if reason != "" {
				return m, nil, Result{Reason: reason}
			}
			// A PR may already exist to watch; startAutoPoll is a no-op when
			// there is nothing to do or a chain is already beating.
			next, poll := next.startAutoPoll()
			return next.relayout(), poll, Result{OK: true}
		}
		return m, nil, Result{Reason: `autopilot takes "on" or "off"`}
	}
	return m, nil, Result{Reason: m.unknownCommandReason(cmd)}
}

// unknownCommandReason explains a command this chat has no case for.
//
// Which explanation is right depends on who sent it. From a peer of the same
// age it is a command that does not exist, and naming it is all there is to
// say. From a newer one it is this window being out of date — a chat is only
// relaunched once it has stopped serving, so an upgraded opentree talks to
// windows built by the binary it replaced — and "unknown command" sends
// somebody hunting for a bug when the answer is to close the window.
func (m Model) unknownCommandReason(cmd Command) string {
	if cmd.Protocol > ProtocolVersion {
		version := m.opts.Version
		if version == "" {
			version = "an older release"
		}
		return fmt.Sprintf("this chat is running opentree %s and has no %q — close the window and reopen it",
			version, cmd.Type)
	}
	return "unknown command " + cmd.Type
}

// turnSource is where a prompt came from, which decides what it is allowed to
// take with it.
type turnSource int

const (
	// typedHere is a message from this window's own box. The images waiting
	// beside it were pasted into that message and leave with it.
	typedHere turnSource = iota

	// sentRemotely is a prompt from the workspace list — `opentree message`, or
	// the review sender — whether it runs the moment it arrives or waits in the
	// queue first. It carries no attachments at all.
	sentRemotely

	// autopilotFed is the loop's own feedback: a failed check's output, or a
	// forwarded CI failure or review. No attachments, and — unlike the two
	// above — it does not reset the loop's iteration count, which is the whole
	// mechanism by which the loop notices it is going in circles.
	autopilotFed
)

// startTurn sends a message to the agent and records it in the log.
//
// Composing here rather than inside promptCmd is what keeps the two honest with
// each other: the log shows what was sent, not what was typed, so an attachment
// that quietly became a link says so on the line you can see.
//
// from is whose message this is. Only a message typed here takes the pasted
// images: a prompt that arrived over the control socket is somebody else's,
// and attaching a half-composed screenshot to it sends an image nobody meant to
// send while orphaning the label the user is still looking at in the box. The
// blocks are left exactly where they were rather than handed back afterwards —
// putting them back would resurrect attachments that had been deleted in the
// meantime, and the labels in the message are the only record of which those
// are.
func (m Model) startTurn(text string, from turnSource) (Model, tea.Cmd) {
	blocks, notices := m.composeTurn(text, from)
	if from == typedHere {
		m.pending = nil
	}

	// A human's message is a new task: the loop's failure count starts over,
	// and a halt — which exists to wait for exactly this — lifts.
	if from != autopilotFed {
		m.auto.iterations = 0
		if m.auto.stage == autoHalted {
			m.auto.stage = autoIdle
		}
	}

	cmd := m.promptCmd(blocks)
	m.lastSent = blocks
	m.entries = append(m.entries, entry{kind: entryUser, text: echo(blocks), rev: m.nextRev()})
	for _, n := range notices {
		m = m.appendNotice(n)
	}

	// The first thing said to a conversation is what names it in /resume.
	var name tea.Cmd
	if !m.titled {
		m.titled = true
		name = m.nameSessionCmd(text)
	}

	m.turn = true
	m.turnStart = time.Now()
	m.err = nil
	m, tick := m.spin()
	return m.relayout(), tea.Batch(cmd, name, tick)
}

// composeTurn is the message about to be sent, as blocks. It exists so the
// state that feeds the composer — the pasted attachments, what this agent
// accepts, which files git knows about — is reachable from a test without
// executing the command that would need a live agent behind it.
//
// The attachments are one of those pieces of state, so the question of whose
// message this is has to be asked here too: see startTurn.
func (m Model) composeTurn(text string, from turnSource) ([]acp.ContentBlock, []string) {
	if from != typedHere {
		return m.composer().prompt(text, nil)
	}
	return m.composer().prompt(text, m.pending)
}

// composer is this session's view of what composing needs to know.
func (m Model) composer() composer {
	return composer{cwd: m.opts.Cwd, known: m.known, images: m.canSendImages}
}

// flushQueued runs the oldest prompt that arrived while the agent was busy or
// starting — one per finished turn, so each answer still gets read before the
// next question goes.
//
// A queued message stays whose it was, however long it waited: one typed here
// leaves with the images captured when enter was pressed, and one from the
// control socket takes no attachments at all — an image pasted in the minutes
// since it was accepted has nothing to do with it. The box's own pending
// images are set aside and put back so a queued message's send cannot take a
// screenshot pasted for the next one.
func (m Model) flushQueued() (tea.Model, tea.Cmd) {
	if len(m.queue) == 0 || m.sessionID == "" || m.turn {
		return m, nil
	}
	q := m.queue[0]
	m.queue = m.queue[1:]
	if q.source != typedHere {
		return m.startTurn(q.text, q.source)
	}
	held := m.pending
	m.pending = q.images
	next, cmd := m.startTurn(q.text, typedHere)
	next.pending = held
	return next, cmd
}

// retryTurn sends the last message again, exactly as it went the first time —
// the blocks rather than the text, so an image is retried along with the words
// around it. Only after a failure: with no error there is nothing to retry,
// and mid-turn there is nothing to retry yet.
func (m Model) retryTurn() (tea.Model, tea.Cmd) {
	if m.err == nil || m.turn || m.sessionID == "" || len(m.lastSent) == 0 {
		return m, nil
	}
	cmd := m.promptCmd(m.lastSent)
	m.entries = append(m.entries, entry{kind: entryUser, text: echo(m.lastSent), rev: m.nextRev()})
	m.turn = true
	m.turnStart = time.Now()
	m.err = nil
	m, tick := m.spin()
	return m.relayout(), tea.Batch(cmd, tick)
}

// stopped reports whether the agent is unusable until something is done about
// it: either it exited, or it refused to work without credentials.
func (m Model) stopped() bool { return m.dead || m.authNeed }

// handleHelpKey dismisses the key list. It is read-only, so anything at all
// closes it rather than making people hunt for the one key that does.
func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		return m, leave
	}
	m.showHelp = false
	return m.relayout(), nil
}

func (m Model) handleStoppedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		return m, leave

	case key.Matches(msg, m.keys.Login) && m.canLogIn():
		return m.startLogin()

	case key.Matches(msg, m.keys.Restart):
		if m.restarting {
			return m, nil
		}
		m.restarting = true
		return m.relayout(), m.restartCmd()
	}
	return m, nil
}

// authCmd hands the terminal to a login flow that wants one, which opentree
// owns and the agent does not.
func (m Model) authCmd(r authRemedy) tea.Cmd {
	c := exec.Command(r.command, r.args...) // #nosec G204 -- from the agent registry or the agent's own _meta
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

// permKeys pairs each reflex key with the permission kind it answers. One
// table serves both directions — resolving a keystroke here, and labelling an
// option's row in the dialog (kindKey) — so the hint and the handler cannot
// disagree about which key means what.
var permKeys = []struct{ key, kind string }{
	{"a", acp.PermissionAllowOnce},
	{"A", acp.PermissionAllowAlways},
	{"d", acp.PermissionRejectOnce},
}

// optionForKey maps a keystroke to a permission option: a digit picks by
// position, and a/A/d pick by kind when the agent offers that kind.
func optionForKey(k string, options []acp.PermissionOption) (string, bool) {
	if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(options) {
		return options[n-1].OptionID, true
	}
	if k == "r" {
		k = "d" // rejecting answers to both letters; the dialog shows [d]
	}
	for _, pk := range permKeys {
		if pk.key != k {
			continue
		}
		for _, o := range options {
			if o.Kind == pk.kind {
				return o.OptionID, true
			}
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
			m.entries[i].rev = m.nextRev()
			return m
		}
	}
	m.entries = append(m.entries, entry{kind: entryPlan, plan: entries, rev: m.nextRev()})
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
		m.entries[n-1].rev = m.nextRev()
		return m
	}
	m.entries = append(m.entries, entry{kind: kind, text: text, rev: m.nextRev()})
	return m
}

// nextRev stamps a mutation. Every write to an entry takes one, which is the
// whole contract the render cache rests on.
func (m *Model) nextRev() uint64 {
	m.rev++
	return m.rev
}

// toggleExpand opens — or closes again — the most recent tool row that is
// holding lines back. "Most recent" rather than a cursor on purpose: the row
// you want open is the one you are looking at, which is the one that just
// said "… 42 more lines", and anything older the agent can simply be asked
// about. An expanded row counts as the target too, so the same key closes
// what it opened.
func (m Model) toggleExpand() Model {
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.kind != entryTool || (!e.expanded && !holdsBack(e.tool)) {
			continue
		}
		m.entries[i].expanded = !e.expanded
		m.entries[i].rev = m.nextRev()
		return m.relayout()
	}
	return m
}

// holdsBack reports whether a call's row is showing less than it has — the
// same choice renderTool makes: the diff when there is one, the output
// otherwise.
func holdsBack(call acp.ToolCall) bool {
	if changes := callDiff(call); len(changes) > 0 {
		return len(changes) > diffMaxLines
	}
	return len(splitLines(unfence(toolOutput(call)))) > outputMaxLines
}

func (m Model) upsertToolCall(call acp.ToolCall) Model {
	if i, ok := m.toolIdx[call.ToolCallID]; ok {
		m.entries[i].tool.Merge(call)
		m.entries[i].rev = m.nextRev()
		return m
	}
	m.toolIdx[call.ToolCallID] = len(m.entries)
	m.entries = append(m.entries, entry{kind: entryTool, tool: call, rev: m.nextRev()})
	return m
}

// setupLogLines caps what the transcript keeps. An install prints thousands of
// lines and the log is re-rendered on every one of them; the end is also the
// part worth having, since that is where a failure says what went wrong.
const setupLogLines = 200

// appendSetupLine grows the setup transcript, which is one entry rather than
// one per line: a thousand entries would be a thousand renders per frame, and
// the output reads as a block anyway.
func (m Model) appendSetupLine(line string) Model {
	n := len(m.entries)
	if n == 0 || m.entries[n-1].kind != entrySetup {
		m.entries = append(m.entries, entry{kind: entrySetup, text: line, rev: m.nextRev()})
		return m
	}
	lines := append(strings.Split(m.entries[n-1].text, "\n"), line)
	if len(lines) > setupLogLines {
		lines = lines[len(lines)-setupLogLines:]
	}
	m.entries[n-1].text = strings.Join(lines, "\n")
	m.entries[n-1].rev = m.nextRev()
	return m
}

func (m Model) appendNotice(text string) Model {
	if text == "" {
		return m
	}
	m.entries = append(m.entries, entry{kind: entryNotice, text: text, rev: m.nextRev()})
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

	// Every path that adds to the log comes through here, so this is the one
	// place that can tell "the reader scrolled up" from "the reader scrolled up
	// and then the agent said something".
	before := m.viewport.TotalLineCount()
	m.viewport.SetContent(m.renderLog())
	switch {
	case atBottom:
		m.viewport.GotoBottom()
	case m.viewport.TotalLineCount() > before:
		m.newBelow = true
	}
	if m.viewport.AtBottom() {
		m.newBelow = false
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

// handleLaunchingKey holds the keyboard while the first agent starts. There is
// nothing to answer yet and nothing to cancel that leaving would not also
// cancel, so only the way out is bound.
func (m Model) handleLaunchingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		return m, leave
	}
	return m, nil
}
