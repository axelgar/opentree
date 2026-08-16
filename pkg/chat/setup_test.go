package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// setupModel is a chat with a bootstrap phase in front of it, in the state Run
// leaves it: no agent yet, and the phase about to open.
func setupModel(t *testing.T, spec Setup) Model {
	t.Helper()
	m := newTestModel()
	m.ctx = context.Background()
	m.sessionID = "" // no session yet: the agent has not been started
	m.setup.spec = spec
	m.send = func(tea.Msg) {} // nothing reads the channel in a test
	return m
}

// approvedSetup is a spec this machine has already said yes to.
func approvedSetup(commands ...string) Setup {
	return Setup{Commands: commands, Trusted: true}
}

// The gate is the point: opentree.toml is tracked, so these commands arrived
// with a clone from whoever last had commit rights.
func TestSetup_UntrustedCommandsAreAskedAboutFirst(t *testing.T) {
	m := setupModel(t, Setup{Commands: []string{"pnpm install"}, Run: "pnpm dev"})

	m, _ = applyUpdate(m, setupBeginMsg{})

	if m.setup.stage != setupAsking {
		t.Fatalf("stage = %v, want the approval question", m.setup.stage)
	}
	if m.overlay() != overlaySetup {
		t.Errorf("overlay = %v, want the setup panel to own the screen", m.overlay())
	}
	// Everything the approval covers is on screen, the run command included:
	// approving something unread is not approving, and the gate covers both.
	view := m.View()
	for _, want := range []string{"pnpm install", "pnpm dev"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel does not show %q, which approving allows:\n%s", want, view)
		}
	}
}

func TestSetup_ApprovingRecordsItBeforeRunning(t *testing.T) {
	approved := 0
	m := setupModel(t, Setup{
		Commands: []string{"true"},
		Approve:  func() error { approved++; return nil },
	})
	m, _ = applyUpdate(m, setupBeginMsg{})

	m, cmd := applyUpdate(m, keyMsg("a"))

	if approved != 1 {
		t.Errorf("Approve called %d times, want once", approved)
	}
	if m.setup.stage != setupRunning {
		t.Errorf("stage = %v, want the commands running", m.setup.stage)
	}
	if cmd == nil {
		t.Error("approving started nothing")
	}
}

// A refusal is "not now": nothing is recorded, so the question comes back, and
// the agent starts against a worktree that may not be ready.
func TestSetup_DecliningStartsTheAgentAnyway(t *testing.T) {
	m := setupModel(t, Setup{
		Commands: []string{"pnpm install"},
		Approve:  func() error { t.Error("declining recorded an approval"); return nil },
		Record:   func() error { t.Error("declining recorded a setup that never ran"); return nil },
	})
	m, _ = applyUpdate(m, setupBeginMsg{})

	m, cmd := applyUpdate(m, keyMsg("d"))

	if m.setup.stage != setupLaunching {
		t.Errorf("stage = %v, want the agent starting", m.setup.stage)
	}
	if cmd == nil {
		t.Error("declining did not start the agent")
	}
	if m.overlay() == overlaySetup {
		t.Error("the setup panel still owns the screen after being declined")
	}
}

// Already approved, so nothing is asked: the commands run as the window opens.
func TestSetup_TrustedCommandsRunStraightAway(t *testing.T) {
	m := setupModel(t, approvedSetup("true"))

	m, cmd := applyUpdate(m, setupBeginMsg{})

	if m.setup.stage != setupRunning {
		t.Fatalf("stage = %v, want the commands running", m.setup.stage)
	}
	if cmd == nil {
		t.Fatal("nothing was started")
	}
}

func TestSetup_OutputStreamsIntoTheLog(t *testing.T) {
	m := setupModel(t, approvedSetup("pnpm install"))
	m, _ = applyUpdate(m, setupBeginMsg{})

	m, _ = applyUpdate(m, setupOutputMsg{line: "$ pnpm install"})
	m, _ = applyUpdate(m, setupOutputMsg{line: "Packages: +412"})

	// One entry, not one per line: an install prints thousands, and the log is
	// re-rendered on every one of them.
	setupEntries := 0
	for _, e := range m.entries {
		if e.kind == entrySetup {
			setupEntries++
		}
	}
	if setupEntries != 1 {
		t.Errorf("setup entries = %d, want the output kept as one block", setupEntries)
	}
	if view := m.View(); !strings.Contains(view, "Packages: +412") {
		t.Errorf("output is not on screen:\n%s", view)
	}
}

func TestSetup_LongOutputIsCapped(t *testing.T) {
	m := setupModel(t, approvedSetup("pnpm install"))
	m, _ = applyUpdate(m, setupBeginMsg{})

	for i := range setupLogLines * 2 {
		m, _ = applyUpdate(m, setupOutputMsg{line: "line " + strings.Repeat("x", i%5)})
	}
	m, _ = applyUpdate(m, setupOutputMsg{line: "the last thing it said"})

	last := m.entries[len(m.entries)-1]
	if got := len(strings.Split(last.text, "\n")); got > setupLogLines {
		t.Errorf("kept %d lines, want at most %d", got, setupLogLines)
	}
	// The end is the part worth having: it is where a failure explains itself.
	if !strings.HasSuffix(last.text, "the last thing it said") {
		t.Error("the transcript dropped its most recent line")
	}
}

// Success records the marker, so the next chat in this worktree — and there are
// many — goes straight to the agent.
func TestSetup_SuccessRecordsAndLaunches(t *testing.T) {
	recorded := 0
	m := setupModel(t, Setup{
		Commands: []string{"pnpm install"},
		Trusted:  true,
		Record:   func() error { recorded++; return nil },
	})
	m, _ = applyUpdate(m, setupBeginMsg{})

	m, cmd := applyUpdate(m, setupDoneMsg{})

	if recorded != 1 {
		t.Errorf("Record called %d times, want once", recorded)
	}
	if m.setup.stage != setupLaunching {
		t.Errorf("stage = %v, want the agent starting", m.setup.stage)
	}
	if cmd == nil {
		t.Error("a finished setup did not start the agent")
	}
}

// An agent let loose on a half-installed worktree spends its first turn on a
// problem that has nothing to do with the task.
func TestSetup_FailureStopsBeforeTheAgent(t *testing.T) {
	m := setupModel(t, Setup{
		Commands: []string{"pnpm install"},
		Trusted:  true,
		Record:   func() error { t.Error("a failed setup was recorded as done"); return nil },
	})
	m, _ = applyUpdate(m, setupBeginMsg{})

	m, cmd := applyUpdate(m, setupDoneMsg{err: errors.New("pnpm install: exit status 1")})

	if m.setup.stage != setupFailed {
		t.Fatalf("stage = %v, want the failed panel", m.setup.stage)
	}
	if cmd != nil {
		t.Error("a failed setup started the agent anyway")
	}
	view := m.View()
	for _, want := range []string{"exit status 1", "[r]", "[s]"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel is missing %q:\n%s", want, view)
		}
	}
}

func TestSetup_RetryAndStartAnyway(t *testing.T) {
	fail := func(t *testing.T) Model {
		t.Helper()
		m := setupModel(t, approvedSetup("pnpm install"))
		m, _ = applyUpdate(m, setupBeginMsg{})
		m, _ = applyUpdate(m, setupDoneMsg{err: errors.New("exit status 1")})
		return m
	}

	m, cmd := applyUpdate(fail(t), keyMsg("r"))
	if m.setup.stage != setupRunning || cmd == nil {
		t.Errorf("[r] left stage %v with cmd %v, want the commands running again", m.setup.stage, cmd != nil)
	}

	m, cmd = applyUpdate(fail(t), keyMsg("s"))
	if m.setup.stage != setupLaunching || cmd == nil {
		t.Errorf("[s] left stage %v with cmd %v, want the agent starting", m.setup.stage, cmd != nil)
	}
}

// Any timeout is wrong for somebody, so cancelling is how a hung setup ends.
func TestSetup_EscCancelsTheRunningCommand(t *testing.T) {
	m := setupModel(t, approvedSetup("cargo build"))
	m, _ = applyUpdate(m, setupBeginMsg{})

	cancelled := false
	m.setup.cancel = func() { cancelled = true }
	m, _ = applyUpdate(m, keyMsg("esc"))

	if !cancelled {
		t.Fatal("esc did not cancel the command")
	}
	// Still running until the process actually stops: the panel changes when
	// the command does, not when the key is pressed.
	if m.setup.stage != setupRunning {
		t.Errorf("stage = %v, want it still running until the command ends", m.setup.stage)
	}

	m, _ = applyUpdate(m, setupDoneMsg{err: context.Canceled})
	if m.setup.stage != setupFailed {
		t.Fatalf("stage = %v after cancelling, want the failed panel", m.setup.stage)
	}
	if view := m.View(); !strings.Contains(view, "cancelled") {
		t.Errorf("a cancelled setup should say so rather than report a signal:\n%s", view)
	}
}

// The workspace list has to be able to say why a window is not answering, and
// a failure has to reach opentree's error log — the window may be one nobody
// ever attaches to.
func TestSetup_StatusSaysSettingUp(t *testing.T) {
	m := setupModel(t, approvedSetup("pnpm install"))
	m, _ = applyUpdate(m, setupBeginMsg{})

	if st := m.status(); st.State != StateSettingUp {
		t.Errorf("state = %q, want %q", st.State, StateSettingUp)
	}
	if st := m.status(); st.Error != "" {
		t.Errorf("error = %q while it is still running", st.Error)
	}

	m, _ = applyUpdate(m, setupDoneMsg{err: errors.New("pnpm install: exit status 1")})
	st := m.status()
	if st.State != StateSettingUp {
		t.Errorf("state = %q after a failure, want %q", st.State, StateSettingUp)
	}
	if !strings.Contains(st.Error, "exit status 1") || !strings.Contains(st.Error, m.opts.Workspace) {
		t.Errorf("error = %q, want the workspace and the failure", st.Error)
	}
}

// The output the user just watched is not thrown away by the agent arriving.
// Only a restart, which replays a whole conversation, needs an empty log.
func TestSetup_LaunchingKeepsTheTranscript(t *testing.T) {
	m := setupModel(t, approvedSetup("pnpm install"))
	m, _ = applyUpdate(m, setupBeginMsg{})
	m, _ = applyUpdate(m, setupOutputMsg{line: "Packages: +412"})

	m, _ = applyUpdate(m, clientReadyMsg{keepLog: true})
	if len(m.entries) == 0 {
		t.Fatal("the setup transcript was cleared when the agent arrived")
	}

	m, _ = applyUpdate(m, clientReadyMsg{})
	if len(m.entries) != 0 {
		t.Error("a restart kept a log its replay is about to rebuild")
	}
}

// Nothing configured, or a worktree that already ran these exact commands: the
// chat is what it always was.
func TestSetup_NoPhaseLeavesTheChatAlone(t *testing.T) {
	m := setupModel(t, Setup{})

	if m.setup.spec.wanted() {
		t.Fatal("an empty spec asked for a setup phase")
	}
	m, cmd := applyUpdate(m, setupBeginMsg{})
	if cmd != nil {
		t.Error("a chat with nothing to set up started something")
	}
	if m.overlay() == overlaySetup {
		t.Error("the setup panel is on screen with nothing to do")
	}
}
