package chat

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// socketPath keeps the path short: unix sockets are capped near 104 bytes and
// t.TempDir() is already long on macOS.
func socketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "s")
}

func TestSocketPath_StaysUnderTheUnixLimit(t *testing.T) {
	// The bind fails outright past ~104 bytes, and a worktree under a deep repo
	// is exactly how that happens.
	deep := "/Users/someone/Documents/repositories/some-project/.worktrees/a-branch"
	got := SocketPath(deep, "feature/a-very-long-branch-name-that-keeps-going")
	if len(got) > 90 {
		t.Errorf("path is %d bytes (%q), want comfortably under the ~104 limit", len(got), got)
	}
	if strings.Contains(filepath.Base(got), "/") {
		t.Errorf("base = %q, want slashes replaced", filepath.Base(got))
	}
}

func TestSocketPath_DistinctPerRepo(t *testing.T) {
	a := SocketPath("/repos/one", "main")
	b := SocketPath("/repos/two", "main")
	if a == b {
		t.Errorf("two checkouts share a socket path: %q", a)
	}
}

func TestSocketPath_StablePerRepo(t *testing.T) {
	// The chat binds this path and the list dials it from a different process,
	// so it has to be derived, not remembered.
	// Guards against anyone mixing a pid or timestamp into the path.
	bound := SocketPath("/repos/one", "main")
	dialled := SocketPath("/repos/one", "main")
	if bound != dialled {
		t.Errorf("bound %q but would dial %q; the list could never find the chat", bound, dialled)
	}
}

func TestServe_QueryReturnsPublishedStatus(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	srv.publish(Status{Workspace: "fix-auth", State: StateWorking, Tool: "go test ./...", Cost: 0.42})

	got, ok := Query(path)
	if !ok {
		t.Fatal("Query failed against a live socket")
	}
	if got.Workspace != "fix-auth" || got.State != StateWorking {
		t.Errorf("status = %+v, want fix-auth working", got)
	}
	if got.Tool != "go test ./..." {
		t.Errorf("Tool = %q", got.Tool)
	}
	if got.Cost != 0.42 {
		t.Errorf("Cost = %v, want 0.42", got.Cost)
	}
}

func TestQuery_NoSocketIsNotAnError(t *testing.T) {
	if _, ok := Query(filepath.Join(t.TempDir(), "absent")); ok {
		t.Error("expected a missing socket to report not-running")
	}
}

func TestServe_ReplacesStaleSocket(t *testing.T) {
	// A killed chat process leaves its socket file behind; the next one must
	// still be able to bind.
	path := socketPath(t)
	first, err := serve(path, nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	_ = first.ln.Close() // die without cleaning up

	second, err := serve(path, nil)
	if err != nil {
		t.Fatalf("serve over a stale socket: %v", err)
	}
	defer func() { _ = second.Close() }()

	second.publish(Status{State: StateIdle})
	if _, ok := Query(path); !ok {
		t.Error("expected the replacement socket to answer")
	}
}

func TestSend_DeliversCommand(t *testing.T) {
	path := socketPath(t)

	var mu sync.Mutex
	var got []Command
	srv, err := serve(path, func(c Command) Result {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, c)
		return Result{OK: true}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	if err := Send(path, Command{Type: CommandPermission, OptionID: "once"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("commands = %+v, want 1", got)
	}
	if got[0].Type != CommandPermission || got[0].OptionID != "once" {
		t.Errorf("command = %+v", got[0])
	}
}

func TestSend_NoSocketErrors(t *testing.T) {
	if err := Send(filepath.Join(t.TempDir(), "absent"), Command{Type: CommandInterrupt}); err == nil {
		t.Error("expected an error sending to a socket nobody is listening on")
	}
}

func TestClose_RemovesSocket(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := Query(path); ok {
		t.Error("a closed socket should not answer")
	}
}

// ---------------------------------------------------------------------------
// Status derivation
// ---------------------------------------------------------------------------

func TestStatus_States(t *testing.T) {
	tests := []struct {
		name  string
		setup func(Model) Model
		want  string
	}{
		{"idle", func(m Model) Model { return m }, StateIdle},
		{"working", func(m Model) Model { m.turn = true; return m }, StateWorking},
		{"dead", func(m Model) Model { m.dead = true; return m }, StateStopped},
		{"auth", func(m Model) Model { m.authNeed = true; return m }, StateStopped},
		{"no session yet", func(m Model) Model { m.sessionID = ""; return m }, StateStarting},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.setup(newTestModel()).status().State; got != tt.want {
				t.Errorf("State = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatus_MirrorsPendingPermission(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, permission(allowOnce, rejectOnce))

	st := m.status()
	if st.State != StateAwaiting {
		t.Fatalf("State = %q, want %q", st.State, StateAwaiting)
	}
	if st.Permission == nil {
		t.Fatal("Permission is nil")
	}
	if st.Permission.Title != "rm -rf dist" {
		t.Errorf("Title = %q", st.Permission.Title)
	}
	if len(st.Permission.Options) != 2 {
		t.Errorf("Options = %d, want the agent's two", len(st.Permission.Options))
	}
}

func TestStatus_ReportsToolAndUsage(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, acp.ToolCall{
		ToolCallID: "t1", Title: "go test ./...", Kind: "execute", Status: acp.StatusInProgress,
	}))
	m, _ = applyUpdate(m, acpUpdateMsg(acp.SessionUpdate{
		Type:  acp.UpdateUsage,
		Usage: &acp.ContextUsage{Used: 250, Size: 1000, Cost: &acp.Cost{Amount: 1.25}},
	}))

	st := m.status()
	if st.Tool != "go test ./..." {
		t.Errorf("Tool = %q", st.Tool)
	}
	if st.ContextPct != 25 {
		t.Errorf("ContextPct = %d, want 25", st.ContextPct)
	}
	if st.Cost != 1.25 {
		t.Errorf("Cost = %v, want 1.25", st.Cost)
	}
}

func TestUpdate_PublishesOnEveryChange(t *testing.T) {
	m := newTestModel()
	var mu sync.Mutex
	var published []Status
	m.publish = func(st Status) {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, st)
	}

	m, _ = applyUpdate(m, permission(allowOnce))
	mu.Lock()
	defer mu.Unlock()
	if len(published) == 0 {
		t.Fatal("nothing was published")
	}
	if published[len(published)-1].State != StateAwaiting {
		t.Errorf("last published state = %q, want %q", published[len(published)-1].State, StateAwaiting)
	}
}

// ---------------------------------------------------------------------------
// Remote commands
// ---------------------------------------------------------------------------

// remoteResult runs a command through the model the way the socket does and
// returns what the sender would be told.
func remoteResult(m Model, cmd Command) (Model, Result) {
	reply := make(chan Result, 1)
	next, _ := applyUpdate(m, socketCommandMsg{cmd: cmd, reply: reply})
	return next, <-reply
}

// TestRemoteCommand_RefusalsAreReported is the regression for a prompt sent
// from the workspace list vanishing without trace: the chat dropped anything it
// could not act on, and the socket had already accepted the bytes, so the list
// reported success for a message that never existed.
func TestRemoteCommand_RefusalsAreReported(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(Model) Model
		cmd        Command
		wantReason string
	}{
		{
			name:       "a second prompt while one is already queued",
			setup:      func(m Model) Model { m.turn = true; m.queued = "first"; return m },
			cmd:        Command{Type: CommandPrompt, Text: "second"},
			wantReason: "already queued",
		},
		{
			name:       "empty prompt",
			setup:      func(m Model) Model { return m },
			cmd:        Command{Type: CommandPrompt, Text: "   "},
			wantReason: "empty",
		},
		{
			name:       "permission nobody is waiting on",
			setup:      func(m Model) Model { return m },
			cmd:        Command{Type: CommandPermission, OptionID: "once"},
			wantReason: "waiting on permission",
		},
		{
			name:       "interrupt while idle",
			setup:      func(m Model) Model { return m },
			cmd:        Command{Type: CommandInterrupt},
			wantReason: "not working",
		},
		{
			name:       "unknown command",
			setup:      func(m Model) Model { return m },
			cmd:        Command{Type: "teleport"},
			wantReason: "unknown command",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, res := remoteResult(tt.setup(newTestModel()), tt.cmd)
			if res.OK {
				t.Fatal("command was accepted; the sender would be told it worked")
			}
			if !strings.Contains(res.Reason, tt.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", res.Reason, tt.wantReason)
			}
		})
	}
}

// TestRemoteCommand_PromptQueues covers the other half of the vanishing-prompt
// bug: a prompt that cannot run yet waits instead of being thrown away, and is
// visible while it waits.
func TestRemoteCommand_PromptQueues(t *testing.T) {
	tests := []struct {
		name  string
		setup func(Model) Model
	}{
		{"agent is mid-turn", func(m Model) Model { m.turn = true; return m }},
		{"session not ready", func(m Model) Model { m.sessionID = ""; return m }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, res := remoteResult(tt.setup(newTestModel()), Command{Type: CommandPrompt, Text: "later please"})
			if !res.OK {
				t.Fatalf("prompt was refused: %q", res.Reason)
			}
			if m.queued != "later please" {
				t.Errorf("queued = %q, want the prompt held", m.queued)
			}
			if !strings.Contains(m.renderLog(), "queued: later please") {
				t.Error("a waiting prompt should be visible in the log")
			}
			if m.status().Queued == "" {
				t.Error("a waiting prompt should be visible to the workspace list")
			}
		})
	}
}

func TestQueuedPrompt_RunsWhenTheTurnEnds(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m, _ = remoteResult(m, Command{Type: CommandPrompt, Text: "run after"})

	m, cmd := applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	if m.queued != "" {
		t.Error("the queue should be drained")
	}
	if !m.turn {
		t.Error("expected the queued prompt to start its own turn")
	}
	if cmd == nil {
		t.Error("expected a prompt cmd")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != entryUser || last.text != "run after" {
		t.Errorf("last entry = %+v, want the queued prompt", last)
	}
}

func TestQueuedPrompt_RunsWhenTheSessionArrives(t *testing.T) {
	m := newTestModel()
	m.sessionID = ""
	m, _ = remoteResult(m, Command{Type: CommandPrompt, Text: "as soon as you can"})

	m, cmd := applyUpdate(m, sessionReadyMsg{id: "ses_new"})
	if m.queued != "" {
		t.Error("the queue should be drained once the session exists")
	}
	if !m.turn || cmd == nil {
		t.Error("expected the queued prompt to run")
	}
}

func TestQueuedPrompt_DroppedWhenTheTurnFails(t *testing.T) {
	// Firing a queued prompt into a session that just errored would stack a
	// second failure on top of the first.
	m := newTestModel()
	m.turn = true
	m, _ = remoteResult(m, Command{Type: CommandPrompt, Text: "never runs"})

	m, _ = applyUpdate(m, promptDoneMsg{err: errString("connection lost")})
	if m.queued != "" {
		t.Errorf("queued = %q, want it dropped after a failed turn", m.queued)
	}
	if m.turn {
		t.Error("no new turn should have started")
	}
}

func TestRemoteCommand_AcceptedPromptReportsOK(t *testing.T) {
	m, res := remoteResult(newTestModel(), Command{Type: CommandPrompt, Text: "run the tests"})
	if !res.OK {
		t.Fatalf("prompt was refused: %q", res.Reason)
	}
	if !m.turn {
		t.Error("expected the turn to start")
	}
}

// TestSend_SurfacesARefusal covers the wire: a refusal has to travel back to
// the list as an error, not be swallowed by a successful write.
func TestSend_SurfacesARefusal(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, func(Command) Result {
		return Result{Reason: "the agent is busy — interrupt it first"}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	err = Send(path, Command{Type: CommandPrompt, Text: "hello"})
	if err == nil {
		t.Fatal("Send reported success for a refused command")
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Errorf("error = %q, want the agent's reason", err)
	}
}

func TestSend_AcceptedCommandReturnsNil(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, func(Command) Result { return Result{OK: true} })
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	if err := Send(path, Command{Type: CommandInterrupt}); err != nil {
		t.Errorf("Send: %v", err)
	}
}

func TestRemoteCommand_AnswersPermission(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	m, _ = applyUpdate(m, perm)
	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPermission, OptionID: "reject"}})

	if m.perm() != nil {
		t.Error("permission should be cleared")
	}
	select {
	case got := <-perm.reply:
		if got != "reject" {
			t.Errorf("reply = %q, want reject", got)
		}
	default:
		t.Fatal("the agent was never answered")
	}
}

func TestRemoteCommand_StalePermissionIsIgnored(t *testing.T) {
	// The list's view is up to a refresh tick old, so it can answer a prompt
	// that was already handled in the chat window.
	m := newTestModel()
	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPermission, OptionID: "once"}})
	if m.perm() != nil {
		t.Error("nothing should have changed")
	}
}

func TestRemoteCommand_Prompt(t *testing.T) {
	m := newTestModel()
	m, cmd := applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPrompt, Text: "run the tests"}})

	if !m.turn {
		t.Error("expected a turn to start")
	}
	if cmd == nil {
		t.Error("expected a prompt cmd")
	}
	if len(m.entries) != 1 || m.entries[0].text != "run the tests" {
		t.Errorf("entries = %+v, want the prompt recorded", m.entries)
	}
}

func TestRemoteCommand_EmptyPromptIgnored(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPrompt, Text: "   "}})
	if m.turn {
		t.Error("whitespace should not start a turn")
	}
}

func TestRemoteCommand_UnknownTypeIsInert(t *testing.T) {
	m := newTestModel()
	before := len(m.entries)
	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: "something-new"}})
	if len(m.entries) != before {
		t.Error("an unrecognized command should do nothing")
	}
}

var _ tea.Model = Model{}
