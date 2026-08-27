package chat

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/notify"
)

// autopilotModel is a test model with the loop wired and on: a check command,
// already trusted, and recording stubs for the closures the caller would hand
// in.
func autopilotModel(rec *autopilotRecorder) Model {
	m := newTestModel()
	m.ctx = context.Background()
	m.send = func(tea.Msg) {}
	m.auto.spec = Autopilot{
		Enabled: true,
		Check:   "make check",
		Trusted: true,
		Approve: func() error { rec.approved = true; return nil },
		Publish: func() (PublishOutcome, error) {
			rec.published++
			return rec.publishOutcome, rec.publishErr
		},
		Persist: func(on bool) error { rec.persisted = append(rec.persisted, on); return nil },
	}
	m.auto.stage = autoIdle
	return m
}

type autopilotRecorder struct {
	approved       bool
	published      int
	publishOutcome PublishOutcome
	publishErr     error
	persisted      []bool
	announced      []notify.Event
}

func endTurn(m Model) (Model, bool) {
	next, _ := applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	return next, next.auto.stage == autoChecking
}

func TestAutopilot_TurnEndStartsTheCheck(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.turn = true

	m, checking := endTurn(m)
	if !checking {
		t.Fatalf("stage = %v, want the check running after an end_turn", m.auto.stage)
	}
	if m.status().State != StateChecking {
		t.Errorf("status = %q, want %q on the socket while the check runs", m.status().State, StateChecking)
	}
}

func TestAutopilot_OnlyAnEndTurnCounts(t *testing.T) {
	for _, reason := range []string{acp.StopCancelled, acp.StopRefusal, "max_tokens"} {
		m := autopilotModel(&autopilotRecorder{})
		m.turn = true
		m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: reason}})
		if m.auto.stage != autoIdle {
			t.Errorf("%s: stage = %v, want idle — a %s turn is not the agent thinking it is done",
				reason, m.auto.stage, reason)
		}
	}
}

func TestAutopilot_AQueuedHumanPromptWins(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.turn = true
	m.queued = "actually, do this instead"

	m, checking := endTurn(m)
	if checking {
		t.Fatal("the check ran ahead of a human's queued prompt")
	}
	if !m.turn {
		t.Error("the queued prompt should have started its turn")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != entryUser || last.text != "actually, do this instead" {
		t.Errorf("last entry = %+v, want the human's prompt", last)
	}
}

func TestAutopilot_AFailedCheckFeedsTheAgent(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.turn = true
	m, _ = endTurn(m)

	m, _ = applyUpdate(m, checkDoneMsg{exitCode: 2, tail: []string{"$ make check", "FAIL: TestThing"}})
	if !m.turn {
		t.Fatal("a failed check should start the fix-it turn")
	}
	if m.auto.iterations != 1 {
		t.Errorf("iterations = %d, want 1", m.auto.iterations)
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != entryUser || !strings.Contains(last.text, "FAIL: TestThing") ||
		!strings.Contains(last.text, "exit code 2") {
		t.Errorf("prompt = %q, want the check's verdict and output in it", last.text)
	}
}

func TestAutopilot_HaltsAtTheIterationCapAndAHumanResets(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.auto.spec.MaxIterations = 2

	for range 2 {
		m.turn = true
		m, _ = endTurn(m)
		m, _ = applyUpdate(m, checkDoneMsg{exitCode: 1, tail: []string{"still failing"}})
	}
	if m.auto.stage != autoHalted {
		t.Fatalf("stage = %v, want halted after the cap", m.auto.stage)
	}
	if err := m.status().Error; !strings.Contains(err, "halted") {
		t.Errorf("status error = %q, want the halt reported where the dashboard's log can copy it", err)
	}

	// A halted loop stays quiet even when a turn ends...
	m.turn = true
	m, checking := endTurn(m)
	if checking {
		t.Fatal("a halted loop ran the check anyway")
	}

	// ...until a human speaks, which is what the halt was waiting for.
	m, _ = m.startTurn("try a different approach", sentRemotely)
	if m.auto.iterations != 0 || m.auto.stage != autoIdle {
		t.Errorf("iterations=%d stage=%v, want the human prompt to reset the loop", m.auto.iterations, m.auto.stage)
	}
}

func TestAutopilot_AGreenCheckPublishes(t *testing.T) {
	rec := &autopilotRecorder{publishOutcome: PublishOutcome{
		PRURL: "https://github.com/acme/repo/pull/12", Created: true, Pushed: true,
	}}
	m := autopilotModel(rec)
	m.opts.Announce = func(ev notify.Event) { rec.announced = append(rec.announced, ev) }
	m.turn = true
	m, _ = endTurn(m)

	m, _ = applyUpdate(m, checkDoneMsg{exitCode: 0})
	if m.auto.stage != autoPublishing {
		t.Fatalf("stage = %v, want publishing after a green check", m.auto.stage)
	}

	m, _ = applyUpdate(m, publishDoneMsg{out: rec.publishOutcome})
	if m.auto.stage != autoIdle || !m.auto.published {
		t.Errorf("stage=%v published=%v, want the loop settled with its PR", m.auto.stage, m.auto.published)
	}
	st := m.status()
	if st.Autopilot == nil || st.Autopilot.Outcome != "published" || st.Autopilot.PRURL != rec.publishOutcome.PRURL {
		t.Errorf("status.Autopilot = %+v, want the published outcome dispatch reads", st.Autopilot)
	}
	if len(rec.announced) != 1 || rec.announced[0].Kind != notify.PRReady {
		t.Fatalf("announced = %+v, want one pr_ready", rec.announced)
	}
}

func TestAutopilot_ANoOpPublishAnnouncesNothing(t *testing.T) {
	rec := &autopilotRecorder{publishOutcome: PublishOutcome{
		PRURL: "https://github.com/acme/repo/pull/12",
	}}
	m := autopilotModel(rec)
	m.opts.Announce = func(ev notify.Event) { rec.announced = append(rec.announced, ev) }

	m, _ = applyUpdate(m, publishDoneMsg{out: rec.publishOutcome})
	if len(rec.announced) != 0 {
		t.Errorf("announced = %+v, want silence for a publish that moved nothing", rec.announced)
	}
	if !m.auto.published {
		t.Error("an up-to-date open PR still counts as published")
	}
}

func TestAutopilot_NoCheckConfiguredPublishesDirectly(t *testing.T) {
	rec := &autopilotRecorder{publishOutcome: PublishOutcome{PRURL: "u", Created: true}}
	m := autopilotModel(rec)
	m.auto.spec.Check = ""
	m.turn = true

	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	if m.auto.stage != autoPublishing {
		t.Errorf("stage = %v, want publishing straight away with no check configured", m.auto.stage)
	}
}

func TestAutopilot_UntrustedCheckAsksFirst(t *testing.T) {
	rec := &autopilotRecorder{}
	m := autopilotModel(rec)
	m.auto.spec.Trusted = false
	m.turn = true

	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	if m.auto.stage != autoAsking {
		t.Fatalf("stage = %v, want the approval question before an unapproved command runs", m.auto.stage)
	}
	if m.overlay() != overlayAutopilot {
		t.Fatalf("overlay = %v, want the autopilot panel", m.overlay())
	}
	if view := m.autopilotView(); !strings.Contains(view, "make check") {
		t.Errorf("panel = %q, want the exact command on screen — that text is what is being approved", view)
	}

	m, _ = applyUpdate(m, keyMsg("a"))
	if !rec.approved {
		t.Error("[a] did not record the approval")
	}
	if m.auto.stage != autoChecking {
		t.Errorf("stage = %v, want the check running once approved", m.auto.stage)
	}
}

func TestAutopilot_DecliningTheCheckDeclinesTheLoop(t *testing.T) {
	rec := &autopilotRecorder{}
	m := autopilotModel(rec)
	m.auto.spec.Trusted = false
	m.turn = true

	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	m, _ = applyUpdate(m, keyMsg("d"))
	if m.auto.stage != autoOff {
		t.Errorf("stage = %v, want off — a loop that cannot check would re-ask after every turn", m.auto.stage)
	}
	if len(rec.persisted) != 1 || rec.persisted[0] {
		t.Errorf("persisted = %v, want the off recorded", rec.persisted)
	}
}

func TestAutopilot_SocketTogglesAndPersists(t *testing.T) {
	rec := &autopilotRecorder{}
	m := autopilotModel(rec)
	m.auto.stage = autoOff

	m, res := remoteResult(m, Command{Type: CommandAutopilot, Text: "on"})
	if !res.OK || m.auto.stage != autoIdle {
		t.Fatalf("on: res=%+v stage=%v", res, m.auto.stage)
	}
	m, res = remoteResult(m, Command{Type: CommandAutopilot, Text: "off"})
	if !res.OK || m.auto.stage != autoOff {
		t.Fatalf("off: res=%+v stage=%v", res, m.auto.stage)
	}
	if len(rec.persisted) != 2 || !rec.persisted[0] || rec.persisted[1] {
		t.Errorf("persisted = %v, want [true false]", rec.persisted)
	}

	_, res = remoteResult(m, Command{Type: CommandAutopilot, Text: "sideways"})
	if res.OK || !strings.Contains(res.Reason, `"on" or "off"`) {
		t.Errorf("res = %+v, want the refusal to teach the vocabulary", res)
	}
}

func TestAutopilot_UnwiredChatRefusesTheToggle(t *testing.T) {
	m := newTestModel() // no Persist: a chat the loop was never wired into
	_, res := remoteResult(m, Command{Type: CommandAutopilot, Text: "on"})
	if res.OK {
		t.Fatal("a chat that cannot persist the toggle accepted it")
	}
}

func TestAutopilot_StatusCarriesTheLoop(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	st := m.status()
	if st.Autopilot == nil || !st.Autopilot.Enabled || st.Autopilot.Phase != "idle" {
		t.Errorf("status.Autopilot = %+v, want the idle loop visible", st.Autopilot)
	}

	off := newTestModel()
	if off.status().Autopilot != nil {
		t.Error("a chat with the loop off should publish no Autopilot field at all")
	}
}

// ---------------------------------------------------------------------------
// Phase B: polling and drains
// ---------------------------------------------------------------------------

func TestAutopilot_PollResultsDrainCIBeforeReviews(t *testing.T) {
	rec := &autopilotRecorder{}
	m := autopilotModel(rec)
	var ackedCI, ackedReviews bool

	m2, _ := applyUpdate(m, autoPollResultMsg{upd: &PollUpdate{
		CIPrompt:      "CI is failing: TestThing",
		ReviewsPrompt: "please rename this",
		AckCI:         func() error { ackedCI = true; return nil },
		AckReviews:    func() error { ackedReviews = true; return nil },
	}})
	m = m2

	// The CI failure went first — broken code outranks style feedback — and
	// only its watermark advanced.
	if !m.turn {
		t.Fatal("nothing was sent")
	}
	last := m.entries[len(m.entries)-1]
	if last.text != "CI is failing: TestThing" {
		t.Fatalf("sent %q, want the CI failure first", last.text)
	}
	if !ackedCI || ackedReviews {
		t.Errorf("acks = ci:%v reviews:%v, want only the sent item marked forwarded", ackedCI, ackedReviews)
	}
	if m.auto.pendingReviews == "" {
		t.Error("the reviews should still be waiting their turn")
	}

	// The reviews drain when the loop next settles — here, after a publish.
	m.turn = false
	m, _ = applyUpdate(m, publishDoneMsg{out: PublishOutcome{PRURL: "u"}})
	last = m.entries[len(m.entries)-1]
	if !m.turn || last.text != "please rename this" {
		t.Errorf("after settling, sent %q, want the reviews", last.text)
	}
	if !ackedReviews {
		t.Error("the reviews ack never ran")
	}
}

func TestAutopilot_PendingWaitsWhileTheAgentWorks(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.turn = true

	m, _ = applyUpdate(m, autoPollResultMsg{upd: &PollUpdate{CIPrompt: "red"}})
	if m.auto.pendingCI != "red" {
		t.Fatal("the finding should wait in its slot")
	}
	if last := m.entries; len(last) != 0 {
		t.Error("nothing should have been sent mid-turn")
	}
}

func TestAutopilot_PendingCoalescesLatestWins(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.turn = true // hold the drain so the slots are observable

	m, _ = applyUpdate(m, autoPollResultMsg{upd: &PollUpdate{CIPrompt: "older failure"}})
	m, _ = applyUpdate(m, autoPollResultMsg{upd: &PollUpdate{CIPrompt: "newer failure"}})
	if m.auto.pendingCI != "newer failure" {
		t.Errorf("pendingCI = %q, want the newer finding — the older one is stale news about the same PR", m.auto.pendingCI)
	}
}

func TestAutopilot_TickChainStopsWhenTheLoopIsOff(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.auto.spec.Poll = func() (*PollUpdate, error) { return nil, nil }
	m.auto.prURL = "https://github.com/acme/repo/pull/1"
	m.autoPolling = true

	m.auto.stage = autoOff
	m2, cmd := applyUpdate(m, autoTickMsg{})
	m = m2
	if m.autoPolling {
		t.Error("the chain should end with the loop off")
	}
	if cmd != nil {
		t.Error("an ended chain must not re-arm")
	}

	// And whatever re-enables the loop is free to start a fresh one.
	m.auto.stage = autoIdle
	m, cmd = m.startAutoPoll()
	if !m.autoPolling || cmd == nil {
		t.Error("startAutoPoll should begin a fresh chain once conditions return")
	}
}

func TestAutopilot_StartAutoPollNeedsAPRAndNoRunningChain(t *testing.T) {
	m := autopilotModel(&autopilotRecorder{})
	m.auto.spec.Poll = func() (*PollUpdate, error) { return nil, nil }

	if m2, cmd := m.startAutoPoll(); cmd != nil || m2.autoPolling {
		t.Error("no PR to watch, nothing to poll")
	}

	m.auto.prURL = "u"
	m2, cmd := m.startAutoPoll()
	if cmd == nil || !m2.autoPolling {
		t.Fatal("a PR and a poller should start the chain")
	}
	if _, cmd := m2.startAutoPoll(); cmd != nil {
		t.Error("a second chain must never start beside a running one")
	}
}

func TestAutopilot_SlashCommandToggles(t *testing.T) {
	rec := &autopilotRecorder{}
	m := autopilotModel(rec)

	next, _ := m.toggleAutopilot()
	m = next.(Model)
	if m.auto.enabled() {
		t.Fatal("/autopilot on an on loop should turn it off")
	}
	next, _ = m.toggleAutopilot()
	m = next.(Model)
	if !m.auto.enabled() {
		t.Fatal("/autopilot again should turn it back on")
	}
	if len(rec.persisted) != 2 || rec.persisted[0] || !rec.persisted[1] {
		t.Errorf("persisted = %v, want [false true]", rec.persisted)
	}
	if !m.canToggleAutopilot() {
		t.Error("a wired chat should offer the command")
	}
	if newTestModel().canToggleAutopilot() {
		t.Error("an unwired chat should not offer the command")
	}
}

func TestAutopilot_CheckRunsForRealAndReportsItsVerdict(t *testing.T) {
	// End-to-end through the real runner: the Batch afterTurn returns carries
	// the command that runs `sh`, and its message is the verdict.
	m := autopilotModel(&autopilotRecorder{})
	m.opts.Cwd = t.TempDir()
	m.auto.spec.Check = "echo not yet; exit 7"
	m.turn = true

	next, cmd := applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	m = next
	if m.auto.stage != autoChecking || cmd == nil {
		t.Fatalf("stage=%v cmd=%v, want the check started", m.auto.stage, cmd)
	}
	done, ok := runUntil[checkDoneMsg](t, cmd)
	if !ok {
		t.Fatal("the batch never produced the check's verdict")
	}
	if done.exitCode != 7 {
		t.Errorf("exit = %d, want the command's own 7", done.exitCode)
	}
	if !strings.Contains(strings.Join(done.tail, "\n"), "not yet") {
		t.Errorf("tail = %v, want the command's output", done.tail)
	}
}

// runUntil executes a tea.Cmd tree until a message of type T appears.
func runUntil[T tea.Msg](t *testing.T, cmd tea.Cmd) (T, bool) {
	t.Helper()
	var zero T
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 32; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case spinnerTickMsg:
			// the spinner chain would tick forever; drop it
		case T:
			return msg, true
		}
	}
	return zero, false
}

func TestCheckErrText(t *testing.T) {
	if got := checkErrText(context.Canceled); got != "cancelled" {
		t.Errorf("checkErrText(canceled) = %q, want the decision, not the signal", got)
	}
	if got := checkErrText(nil); got != "" {
		t.Errorf("checkErrText(nil) = %q", got)
	}
}

func TestAutopilot_TickSkipsFetchMidTurnButKeepsBeating(t *testing.T) {
	polled := 0
	m := autopilotModel(&autopilotRecorder{})
	m.auto.spec.Poll = func() (*PollUpdate, error) { polled++; return nil, nil }
	m.auto.prURL = "u"
	m.autoPolling = true
	m.turn = true

	next, cmd := applyUpdate(m, autoTickMsg{})
	m = next
	if cmd == nil {
		t.Fatal("the chain must keep beating through a turn")
	}
	if polled != 0 {
		t.Error("no fetch mid-turn — the drain would wait anyway")
	}

	// Idle, the beat fetches: the batch carries the poll.
	m.turn = false
	_, cmd = applyUpdate(m, autoTickMsg{})
	if _, ok := runUntil[autoPollResultMsg](t, cmd); !ok {
		t.Fatal("an idle beat should fetch")
	}
	if polled != 1 {
		t.Errorf("polled = %d, want exactly one fetch", polled)
	}
}
