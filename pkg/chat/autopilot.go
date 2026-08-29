package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/diag"
	"github.com/axelgar/opentree/pkg/notify"
	"github.com/axelgar/opentree/pkg/ui"
)

// The autopilot phase closes the loop the dashboard leaves open: when a turn
// ends, the project's check command decides whether the work is done, a
// failure goes back to the agent as the next prompt, and a pass publishes the
// branch as a PR. The human is interrupted for the two things only a human can
// answer — permissions, and the one-time approval of the check command — and
// notified when the PR exists.
//
// Like Setup, the decisions that need the repository — is the check approved,
// how is the toggle persisted, what publishing means — are made by the caller
// and arrive here as answers. The chat still does not know what a workspace or
// a trust file is, and pkg/chat still does not import pkg/github.

// maxAutopilotIterations is how many consecutive autopilot-fed turns may run
// before the loop halts and waits for a human. It exists because a check the
// agent cannot satisfy would otherwise loop forever, spending money on the
// same failure; five attempts is enough to fix a real mistake and few enough
// to notice a hopeless one. Any human prompt resets the count.
const maxAutopilotIterations = 5

// checkTailLines and checkTailBytes bound what a failed check feeds back to
// the agent. The tail rather than the head, because that is where test
// runners put the summary; bounded, because a build log can be megabytes and
// the agent's context is the scarcest thing in the loop.
const (
	checkTailLines = 400
	checkTailBytes = 64 * 1024
)

// Autopilot is the spec the caller hands the chat: whether the loop is on,
// what the check command is, and the answers to every question that needs the
// repository.
type Autopilot struct {
	// Enabled is the persisted toggle, as it stood when the chat started.
	// The socket flips it live; state.json is the caller's to write, through
	// Persist.
	Enabled bool

	// Check is the trust-gated [workspace] check command. Empty publishes
	// without checking — a project with no gate configured still gets the
	// push-and-PR half of the loop.
	Check string

	// Trusted is whether this machine has approved the block Check is part
	// of. False puts the approval question on screen before the first check
	// runs; headless callers refuse earlier, where nothing can ask.
	Trusted bool

	// Approve records approval for next time, once the user has given it here.
	Approve func() error

	// MaxIterations caps consecutive autopilot-fed turns; zero means the
	// package default. It is a field so tests do not need five failures to see
	// the halt.
	MaxIterations int

	// Publish drives the branch to a published PR — push what is missing,
	// create or update, never duplicate — and reports what it did.
	Publish func() (PublishOutcome, error)

	// Persist writes the toggle to state.json, so the next chat window starts
	// with the answer.
	Persist func(on bool) error

	// PRURL is the workspace's PR as state.json last knew it, so a chat
	// reopened after a publish keeps watching the PR it made. The loop's own
	// publishes update the live copy.
	PRURL string

	// Poll asks GitHub what is new on the PR — a CI failure not yet forwarded,
	// review comments not yet forwarded — and how to mark each as forwarded.
	// Nil polls nothing.
	Poll func() (*PollUpdate, error)
}

// PollUpdate is one poll's answer: at most one CI prompt and one reviews
// prompt, each with the watermark write that marks it forwarded. The Acks run
// only at the moment the prompt is actually sent — a chat that dies holding
// one must re-fetch, not lose it.
type PollUpdate struct {
	CIPrompt      string
	ReviewsPrompt string
	AckCI         func() error
	AckReviews    func() error
}

// PublishOutcome mirrors workspace.PublishOutcome without importing it: what
// publishing did, so the notice and the notification can say so.
type PublishOutcome struct {
	PRURL   string
	Created bool
	Pushed  bool
	Skipped string
}

// available is whether this chat was wired for autopilot at all — a caller
// that cannot persist the toggle cannot honour it.
func (a Autopilot) available() bool { return a.Persist != nil }

// autopilotPhaseName is the stage as the socket spells it.
func autopilotPhaseName(s autoStage) string {
	switch s {
	case autoAsking:
		return "asking"
	case autoChecking:
		return "checking"
	case autoPublishing:
		return "publishing"
	case autoHalted:
		return "halted"
	case autoIdle:
		return "idle"
	}
	return ""
}

// autoStage is where the loop has got to.
type autoStage int

const (
	autoOff        autoStage = iota // switched off, or never wired
	autoIdle                        // on, waiting for a turn to end
	autoAsking                      // waiting for this machine to approve the check command
	autoChecking                    // the check command is running
	autoPublishing                  // pushing and creating or updating the PR
	autoHalted                      // the check kept failing; waiting for a human
)

// autoPhase is the chat's own state for the loop.
type autoPhase struct {
	stage autoStage
	spec  Autopilot

	// iterations counts consecutive autopilot-fed turns, reset by any human
	// prompt. At the cap the loop halts rather than spending forever on a
	// failure it cannot fix.
	iterations int

	// cancel stops the running check's whole process group. Held here because
	// esc arrives long after the command was started.
	cancel context.CancelFunc

	// prURL is the PR the loop last published, for the status the dashboard
	// and dispatch read.
	prURL string

	// published is whether a publish has succeeded in this chat's lifetime —
	// dispatch's success signal.
	published bool

	// pendingCI and pendingReviews are what the poll found, waiting for the
	// agent to be free. One coalescing slot each, latest wins: a newer CI
	// failure supersedes an older one, and the review set is re-fetched whole.
	// Deliberately not m.queued — a human's prompt must never be refused
	// because the loop got there first.
	pendingCI         string
	pendingCIAck      func() error
	pendingReviews    string
	pendingReviewsAck func() error

	err error
}

// enabled is whether the loop acts on a finished turn at all.
func (a autoPhase) enabled() bool { return a.stage != autoOff }

// active reports whether the phase owns the screen.
func (a autoPhase) active() bool {
	return a.stage == autoAsking || a.stage == autoChecking || a.stage == autoPublishing
}

// running is whether something of the loop's is executing, for the spinner.
func (a autoPhase) running() bool {
	return a.stage == autoChecking || a.stage == autoPublishing
}

// max is the iteration cap in force.
func (a autoPhase) max() int {
	if a.spec.MaxIterations > 0 {
		return a.spec.MaxIterations
	}
	return maxAutopilotIterations
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// checkOutputMsg is one line the check command printed, into the log the way
// setup output goes.
type checkOutputMsg struct{ line string }

// checkDoneMsg is the check's verdict: the exit code, the bounded tail of what
// it printed (for the prompt a failure becomes), and the error for a check
// that never finished.
type checkDoneMsg struct {
	exitCode int
	tail     []string
	err      error
}

// publishDoneMsg ends the publishing stage.
type publishDoneMsg struct {
	out PublishOutcome
	err error
}

// autoTickMsg is the poll's clock; autoPollResultMsg is what one poll found.
type autoTickMsg struct{}

type autoPollResultMsg struct {
	upd *PollUpdate
	err error
}

// ---------------------------------------------------------------------------
// Driving the loop
// ---------------------------------------------------------------------------

// afterTurn decides what a finished turn leads to.
//
// A human's queued prompt always wins — their message is why the agent exists.
// After that, autopilot acts only on a turn the agent ended of its own accord:
// a cancelled or refused turn is not "the agent thinks it is done", a pending
// permission means the turn is not really over, and a dead agent has nothing
// to act for.
func (m Model) afterTurn(resp *acp.PromptResponse) (tea.Model, tea.Cmd) {
	if len(m.queue) > 0 {
		return m.flushQueued()
	}
	if !m.auto.enabled() || m.dead || m.perm() != nil {
		return m, nil
	}
	if resp == nil || resp.StopReason != acp.StopEndTurn {
		return m, nil
	}

	if m.auto.stage == autoHalted {
		return m, nil
	}

	if m.auto.spec.Check != "" && !m.auto.spec.Trusted {
		m.auto.stage = autoAsking
		return m.relayout(), nil
	}
	if m.auto.spec.Check != "" {
		return m.startCheck()
	}
	return m.startPublish()
}

// startCheck runs the check command, streaming its output into the log.
func (m Model) startCheck() (Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.auto.stage = autoChecking
	m.auto.err = nil
	m.auto.cancel = cancel

	send, cwd, command := m.send, m.opts.Cwd, m.auto.spec.Check
	run := func() tea.Msg {
		// The tail is collected here rather than on the model because emit is
		// called from the command's own stream goroutine; the mutex covers the
		// window where a drain timeout lets that goroutine outlive the wait.
		var mu sync.Mutex
		var tail []string
		code, err := bootstrap.RunCheck(ctx, cwd, command, func(line string) {
			mu.Lock()
			tail = append(tail, line)
			if len(tail) > checkTailLines {
				tail = tail[len(tail)-checkTailLines:]
			}
			mu.Unlock()
			send(checkOutputMsg{line: line})
		})
		cancel()
		mu.Lock()
		kept := append([]string(nil), tail...)
		mu.Unlock()
		return checkDoneMsg{exitCode: code, tail: kept, err: err}
	}

	m, tick := m.spin()
	return m.relayout(), tea.Batch(run, tick)
}

// finishCheck acts on the verdict: a pass publishes, a failure goes back to
// the agent as the next prompt, and one failure too many halts the loop.
func (m Model) finishCheck(msg checkDoneMsg) (tea.Model, tea.Cmd) {
	m.auto.cancel = nil

	if msg.err != nil {
		// Cancelled, or the command could not start. Either way nothing here
		// is the agent's to fix, so the loop goes quiet rather than prompting.
		m.auto.stage = autoIdle
		m = m.appendNotice("autopilot: check did not finish: " + checkErrText(msg.err))
		return m.relayout(), nil
	}
	if msg.exitCode == 0 {
		m = m.appendNotice("autopilot: check passed")
		return m.startPublish()
	}

	m.auto.iterations++
	if m.auto.iterations >= m.auto.max() {
		m.auto.stage = autoHalted
		m = m.appendNotice(fmt.Sprintf(
			"autopilot: halted — the check is still failing after %d attempts; your next message resets it", m.auto.iterations))
		return m.relayout(), nil
	}

	m.auto.stage = autoIdle
	prompt := formatCheckPrompt(m.auto.spec.Check, msg.exitCode, msg.tail)
	return m.startTurn(prompt, autopilotFed)
}

// startPublish pushes and creates or updates the PR, off the render loop.
func (m Model) startPublish() (Model, tea.Cmd) {
	m.auto.stage = autoPublishing
	publish := m.auto.spec.Publish
	run := func() tea.Msg {
		if publish == nil {
			return publishDoneMsg{err: fmt.Errorf("this chat has no publisher wired")}
		}
		out, err := publish()
		return publishDoneMsg{out: out, err: err}
	}
	m, tick := m.spin()
	return m.relayout(), tea.Batch(run, tick)
}

// finishPublish records what publishing did and tells the human the one thing
// worth a banner: the PR exists.
func (m Model) finishPublish(msg publishDoneMsg) (tea.Model, tea.Cmd) {
	m.auto.stage = autoIdle
	if msg.err != nil {
		// Kept for the status the dashboard's error log copies: this window
		// may be one nobody ever attaches to. The next green check retries.
		m.auto.err = msg.err
		m = m.appendNotice("autopilot: publishing failed: " + msg.err.Error())
		return m.relayout(), nil
	}
	m.auto.err = nil

	switch {
	case msg.out.Skipped != "":
		m = m.appendNotice("autopilot: nothing published: " + msg.out.Skipped)
	case msg.out.Created:
		m.auto.prURL, m.auto.published = msg.out.PRURL, true
		m = m.appendNotice("autopilot: PR created " + msg.out.PRURL)
	case msg.out.Pushed:
		m.auto.prURL, m.auto.published = msg.out.PRURL, true
		m = m.appendNotice("autopilot: pushed, PR updated " + msg.out.PRURL)
	default:
		m.auto.prURL, m.auto.published = msg.out.PRURL, true
		m = m.appendNotice("autopilot: PR already up to date " + msg.out.PRURL)
	}

	// Only a publish that moved something is worth a banner; "still up to
	// date" every few turns is the notifier people mute.
	if m.opts.Announce != nil && (msg.out.Created || msg.out.Pushed) {
		m.opts.Announce(notify.Event{
			Kind:      notify.PRReady,
			Workspace: m.opts.Workspace,
			Detail:    msg.out.PRURL,
		})
	}

	// The loop has settled and there is a PR now: start watching it, and hand
	// the agent whatever the watch already found.
	m = m.relayout()
	m, poll := m.startAutoPoll()
	next, drain := m.drainAutopilot()
	return next, tea.Batch(poll, drain)
}

// autopilotPollInterval is how often the loop asks GitHub what is new on the
// PR. Generous on purpose: each poll is two gh calls and a GraphQL query, per
// workspace, and CI takes minutes to say anything new anyway.
const autopilotPollInterval = 2 * time.Minute

// autoPollTick is the next beat of the poll's clock.
func autoPollTick() tea.Cmd {
	return tea.Tick(autopilotPollInterval, func(time.Time) tea.Msg { return autoTickMsg{} })
}

// startAutoPoll begins the tick chain if there is anything to watch and no
// chain is already running — the same self-sustaining-chain guard the spinner
// uses, for the same reason: two chains is polling at twice the rate forever.
func (m Model) startAutoPoll() (Model, tea.Cmd) {
	if m.autoPolling || !m.auto.enabled() || m.auto.spec.Poll == nil || m.auto.prURL == "" {
		return m, nil
	}
	m.autoPolling = true
	return m, autoPollTick()
}

// handleAutoTick is one beat: re-arm, and fetch unless the answer could not be
// acted on anyway.
func (m Model) handleAutoTick() (tea.Model, tea.Cmd) {
	if !m.auto.enabled() || m.auto.spec.Poll == nil || m.auto.prURL == "" {
		// The chain ends here, so whatever re-enables the loop has to be free
		// to start a fresh one.
		m.autoPolling = false
		return m, nil
	}
	m.autoPolling = true
	if m.turn || m.auto.active() {
		// Mid-turn the drain would wait anyway; fetching now would only buy a
		// staler answer. The chain keeps beating.
		return m, autoPollTick()
	}
	poll := m.auto.spec.Poll
	fetch := func() tea.Msg {
		upd, err := poll()
		return autoPollResultMsg{upd: upd, err: err}
	}
	return m, tea.Batch(fetch, autoPollTick())
}

// handleAutoPollResult folds what a poll found into the pending slots and
// drains if nothing is in the way.
func (m Model) handleAutoPollResult(msg autoPollResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Rate limits and offline moments retry themselves next tick; the
		// error log is for failures somebody must act on, and this is not one.
		diag.Log("chat", "autopilot poll failed", "workspace", m.opts.Workspace, "err", msg.err)
	}
	if msg.upd == nil {
		return m, nil
	}
	if msg.upd.CIPrompt != "" {
		m.auto.pendingCI, m.auto.pendingCIAck = msg.upd.CIPrompt, msg.upd.AckCI
	}
	if msg.upd.ReviewsPrompt != "" {
		m.auto.pendingReviews, m.auto.pendingReviewsAck = msg.upd.ReviewsPrompt, msg.upd.AckReviews
	}
	return m.drainAutopilot()
}

// drainAutopilot sends one pending item to the agent, CI before reviews —
// broken code outranks style feedback — and only when nothing outranks the
// loop: a running turn, a pending permission, a human's queued prompt, a halt.
// One item per turn, so each answer lands as its own conversation entry and
// the turn it starts re-runs the check like any other.
func (m Model) drainAutopilot() (tea.Model, tea.Cmd) {
	if !m.auto.enabled() || m.auto.stage != autoIdle || m.turn || m.dead ||
		m.perm() != nil || len(m.queue) > 0 || m.sessionID == "" {
		return m, nil
	}
	text, ack := m.auto.pendingCI, m.auto.pendingCIAck
	if text != "" {
		m.auto.pendingCI, m.auto.pendingCIAck = "", nil
	} else {
		text, ack = m.auto.pendingReviews, m.auto.pendingReviewsAck
		if text == "" {
			return m, nil
		}
		m.auto.pendingReviews, m.auto.pendingReviewsAck = "", nil
	}
	// Marked forwarded at the moment of sending, not of fetching: an ack that
	// fails only costs a duplicate later, where the reverse order would lose
	// feedback a dying chat never delivered.
	if ack != nil {
		if err := ack(); err != nil {
			m = m.appendNotice("could not record the forwarded feedback: " + err.Error())
		}
	}
	return m.startTurn(text, autopilotFed)
}

// setAutopilot flips the loop on or off, persisting the answer. It is the one
// mutation both the socket command and the slash command share.
func (m Model) setAutopilot(on bool) (Model, string) {
	if !m.auto.spec.available() {
		return m, "autopilot is not available in this chat"
	}
	if on {
		if !m.auto.enabled() {
			m.auto.stage = autoIdle
		}
	} else {
		if m.auto.stage == autoChecking && m.auto.cancel != nil {
			m.auto.cancel()
		}
		m.auto.stage = autoOff
	}
	if err := m.auto.spec.Persist(on); err != nil {
		m = m.appendNotice("could not record the autopilot toggle: " + err.Error())
	}
	if on {
		m = m.appendNotice("autopilot on — after each turn the check runs, failures go back to the agent, green publishes a PR")
	} else {
		m = m.appendNotice("autopilot off")
	}
	return m, ""
}

// toggleAutopilot is the /autopilot slash command.
func (m Model) toggleAutopilot() (tea.Model, tea.Cmd) {
	next, reason := m.setAutopilot(!m.auto.enabled())
	if reason != "" {
		next = next.appendNotice(reason)
	}
	next, poll := next.startAutoPoll()
	return next.relayout(), poll
}

// canToggleAutopilot gates the slash command on the chat being wired for it.
func (m Model) canToggleAutopilot() bool { return m.auto.spec.available() }

// formatCheckPrompt is the failure as the agent receives it: the command, its
// verdict, and the bounded tail of what it printed.
func formatCheckPrompt(command string, exitCode int, tail []string) string {
	joined := strings.Join(tail, "\n")
	if len(joined) > checkTailBytes {
		joined = joined[len(joined)-checkTailBytes:]
		if i := strings.IndexByte(joined, '\n'); i >= 0 {
			joined = joined[i+1:]
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "The check command `%s` failed with exit code %d. Here is its output:\n\n```\n%s\n```\n\n",
		command, exitCode, joined)
	sb.WriteString("Fix what made it fail. The check runs again when you finish.")
	return sb.String()
}

// checkErrText is the failure in one line, cancelled spelled as a decision
// rather than a signal — the same courtesy setupErrorText extends.
func checkErrText(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), context.Canceled.Error()) {
		return "cancelled"
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Keys and panels
// ---------------------------------------------------------------------------

// handleAutopilotKey drives whichever of the loop's panels is up.
func (m Model) handleAutopilotKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		return m, leave
	}

	switch m.auto.stage {
	case autoAsking:
		switch {
		case key.Matches(msg, m.keys.Approve):
			// Recorded before the command runs, the same as setup: what is
			// approved is this text, not its outcome.
			if approve := m.auto.spec.Approve; approve != nil {
				if err := approve(); err != nil {
					m = m.appendNotice("could not record the approval: " + err.Error())
				}
			}
			m.auto.spec.Trusted = true
			return m.startCheck()

		case key.Matches(msg, m.keys.Decline):
			// Declining the gate declines the loop: an autopilot that cannot
			// run its check would come back with the same question after every
			// turn.
			next, _ := m.setAutopilot(false)
			return next.relayout(), nil
		}

	case autoChecking:
		if key.Matches(msg, m.keys.Cancel) {
			if m.auto.cancel != nil {
				m.auto.cancel()
			}
			return m, nil
		}
	}
	return m, nil
}

// autopilotHeight is the footer the panel needs.
func (m Model) autopilotHeight() int {
	return len(m.autopilotLines(m.width)) + 1
}

// autopilotView draws the loop's footer panel.
func (m Model) autopilotView() string {
	return strings.Join(m.autopilotLines(m.width), "\n")
}

// autopilotLines is the panel's content, one slice for the height and the view
// to agree on.
func (m Model) autopilotLines(width int) []string {
	switch m.auto.stage {
	case autoAsking:
		return []string{
			permLabelStyle.Render("Autopilot wants to run this project's check command after each turn:"),
			noticeStyle.Render(ui.Truncate("  check  "+m.auto.spec.Check, width)),
			helpStyle.Render("from opentree.toml, which arrives with a clone"),
			strings.Join([]string{
				permKeyStyle.Render("[a]") + " " + permLabelStyle.Render("run it from now on"),
				permKeyStyle.Render("[d]") + " " + permLabelStyle.Render("turn autopilot off"),
				permKeyStyle.Render("[ctrl+c]") + " " + permLabelStyle.Render("back to opentree"),
			}, "   "),
		}

	case autoChecking:
		head := fmt.Sprintf("%s autopilot: checking… %s",
			ui.SpinnerFrames[m.spinnerFrame], m.auto.spec.Check)
		return []string{
			toolRunningStyle.Render(ui.Truncate(head, width)),
			permKeyStyle.Render("[esc]") + " " + permLabelStyle.Render("cancel"),
		}

	default: // autoPublishing
		head := ui.SpinnerFrames[m.spinnerFrame] + " autopilot: pushing and opening a PR…"
		return []string{toolRunningStyle.Render(ui.Truncate(head, width))}
	}
}
