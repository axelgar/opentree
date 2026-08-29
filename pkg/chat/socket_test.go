package chat

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/notify"
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
	srv, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	srv.publish(Status{Workspace: "fix-auth", State: StateWorking, Tool: "go test ./...", Cost: 0.42})

	got, ok := Query(path, "fix-auth")
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
	if _, ok := Query(filepath.Join(t.TempDir(), "absent"), "fix-auth"); ok {
		t.Error("expected a missing socket to report not-running")
	}
}

func TestServe_ReplacesStaleSocket(t *testing.T) {
	// A killed chat process leaves its socket file behind; the next one must
	// still be able to bind.
	path := socketPath(t)
	first, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	_ = first.ln.Close() // die without cleaning up

	second, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve over a stale socket: %v", err)
	}
	defer func() { _ = second.Close() }()

	second.publish(Status{State: StateIdle})
	if _, ok := Query(path, "fix-auth"); !ok {
		t.Error("expected the replacement socket to answer")
	}
}

func TestSend_DeliversCommand(t *testing.T) {
	path := socketPath(t)

	var mu sync.Mutex
	var got []Command
	srv, err := serve(path, "fix-auth", func(c Command) Result {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, c)
		return Result{OK: true}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	if err := Send(path, "fix-auth", Command{Type: CommandPermission, OptionID: "once"}); err != nil {
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
	if err := Send(filepath.Join(t.TempDir(), "absent"), "fix-auth", Command{Type: CommandInterrupt}); err == nil {
		t.Error("expected an error sending to a socket nobody is listening on")
	}
}

func TestClose_RemovesSocket(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := Query(path, "fix-auth"); ok {
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

	applyUpdate(m, permission(allowOnce))
	mu.Lock()
	defer mu.Unlock()
	if len(published) == 0 {
		t.Fatal("nothing was published")
	}
	if published[len(published)-1].State != StateAwaiting {
		t.Errorf("last published state = %q, want %q", published[len(published)-1].State, StateAwaiting)
	}
}

// TestStatus_SinceStampsTheChange is what the dashboard's "blocked 12m" rests
// on: the moment belongs to the chat, and a reader that arrives later must not
// be able to reset it.
func TestStatus_SinceStampsTheChange(t *testing.T) {
	m := newTestModel()
	var published []Status
	m.publish = func(st Status) { published = append(published, st) }

	// A first message with no state change still stamps: nothing had been
	// published before it.
	m, _ = applyUpdate(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	first := published[len(published)-1]
	if first.Since.IsZero() {
		t.Fatal("Since was never stamped")
	}

	// Another message that changes nothing about the state keeps it.
	m, _ = applyUpdate(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	if got := published[len(published)-1]; !got.Since.Equal(first.Since) {
		t.Errorf("Since moved without a state change: %v then %v", first.Since, got.Since)
	}

	// A permission is a new state, and a new moment.
	_, _ = applyUpdate(m, permission(allowOnce))
	last := published[len(published)-1]
	if last.State != StateAwaiting {
		t.Fatalf("State = %q, want %q", last.State, StateAwaiting)
	}
	if !last.Since.After(first.Since) {
		t.Errorf("Since = %v, want a later moment than %v", last.Since, first.Since)
	}
}

// ---------------------------------------------------------------------------
// What a notifier sees
// ---------------------------------------------------------------------------

func TestSignalOf(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   notify.Signal
	}{
		{"idle", Status{State: StateIdle}, notify.Signal{State: notify.StateIdle}},
		{"working", Status{State: StateWorking}, notify.Signal{State: notify.StateWorking}},
		{"stopped", Status{State: StateStopped}, notify.Signal{State: notify.StateStopped}},
		{"starting is nothing anyone is told about", Status{State: StateStarting}, notify.Signal{State: notify.StateOther}},
		{"nor is a setup that is still running", Status{State: StateSettingUp}, notify.Signal{State: notify.StateOther}},
		{
			// Nothing further happens in that window until somebody deals with
			// it, which is what stopped means to whoever is waiting.
			"a setup that failed is a stopped agent",
			Status{State: StateSettingUp, Error: "fix-auth: pnpm install exited 1"},
			notify.Signal{State: notify.StateStopped, Detail: "fix-auth: pnpm install exited 1"},
		},
		{
			"blocked carries the question, which is also its fingerprint",
			Status{State: StateAwaiting, Permission: &Permission{Title: "rm -rf dist"}},
			notify.Signal{State: notify.StateBlocked, Detail: "rm -rf dist"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signalOf(tt.status); got != tt.want {
				t.Errorf("signalOf() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestUpdate_NotifiesEveryReading is the funnel argument: the notifier is
// edge-triggered itself, so what it needs from the chat is to be told
// everything rather than to be told what changed.
func TestUpdate_NotifiesEveryReading(t *testing.T) {
	m := newTestModel()
	var seen []notify.Signal
	m.opts.Notify = func(sig notify.Signal) { seen = append(seen, sig) }

	m, _ = applyUpdate(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	_, _ = applyUpdate(m, permission(allowOnce))

	if len(seen) < 2 {
		t.Fatalf("saw %d readings, want one per update", len(seen))
	}
	if got := seen[len(seen)-1]; got.State != notify.StateBlocked {
		t.Errorf("last reading = %+v, want the escalation", got)
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
			if len(m.queue) != 1 || m.queue[0].text != "later please" {
				t.Errorf("queue = %+v, want the prompt held", m.queue)
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
	if len(m.queue) != 0 {
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
	if len(m.queue) != 0 {
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

	m, _ = applyUpdate(m, promptDoneMsg{err: stringError("connection lost")})
	if len(m.queue) != 0 {
		t.Errorf("queue = %+v, want it dropped after a failed turn", m.queue)
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
	srv, err := serve(path, "fix-auth", func(Command) Result {
		return Result{Reason: "the agent is busy — interrupt it first"}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	err = Send(path, "fix-auth", Command{Type: CommandPrompt, Text: "hello"})
	if err == nil {
		t.Fatal("Send reported success for a refused command")
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Errorf("error = %q, want the agent's reason", err)
	}
}

func TestSend_AcceptedCommandReturnsNil(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, "fix-auth", func(Command) Result { return Result{OK: true} })
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	if err := Send(path, "fix-auth", Command{Type: CommandInterrupt}); err != nil {
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

// Regression: the path used to be the workspace name truncated to 32 bytes, so
// two branches sharing a 32-character prefix — one afternoon of
// feature/<ticket>-<description> — resolved to one socket. The second chat to
// start unlinked the first's live socket and bound over it, and prompts meant
// for one worktree ran in the other.
func TestSocketPath_LongNamesStayDistinct(t *testing.T) {
	const prefix = "feature/a-very-long-branch-name-"
	a := SocketPath("/repos/one", prefix+"that-keeps-going")
	b := SocketPath("/repos/one", prefix+"x")

	if a == b {
		t.Fatalf("two workspaces share a socket path: %q", a)
	}
	if len(a) > 90 || len(b) > 90 {
		t.Errorf("paths are %d and %d bytes, want comfortably under the ~104 limit", len(a), len(b))
	}
}

// The suffix is only added when the name had to be shortened, so every
// ordinary workspace keeps the path it already has — a chat started by the
// previous binary stays reachable across the upgrade.
func TestSocketPath_ShortNamesAreUnchanged(t *testing.T) {
	for _, name := range []string{"main", "fix/auth", "feature/exactly-32-chars-long-a"} {
		got := SocketPath("/repos/one", name)
		want := strings.NewReplacer("/", "-", ":", "-").Replace(name)
		if filepath.Base(got) != want {
			t.Errorf("SocketPath(%q) base = %q, want %q", name, filepath.Base(got), want)
		}
	}
}

// The list's view of a session is up to a refresh tick old. An answer given
// for one permission must not land on whichever one is waiting by the time it
// arrives.
func TestRemoteCommand_StaleToolCallIDIsRefused(t *testing.T) {
	m := newTestModel()
	m.perms = []permissionMsg{{
		req:   acp.PermissionRequest{ToolCall: acp.ToolCall{ToolCallID: "call_2"}},
		reply: make(chan string, 1),
	}}

	_, _, res := m.applyRemoteCommand(Command{
		Type: CommandPermission, OptionID: "allow", ToolCallID: "call_1",
	})
	if res.OK {
		t.Error("an answer for a permission that has gone was applied")
	}
	if len(m.perms[0].reply) != 0 {
		t.Error("the pending permission was answered anyway")
	}
}

// A dashboard older than this chat sends no id at all. Refusing those would
// break remote answering for everyone mid-upgrade, and a chat window is never
// relaunched while it is still serving — so mid-upgrade is the normal state.
func TestRemoteCommand_EmptyToolCallIDIsAccepted(t *testing.T) {
	m := newTestModel()
	m.perms = []permissionMsg{{
		req:   acp.PermissionRequest{ToolCall: acp.ToolCall{ToolCallID: "call_2"}},
		reply: make(chan string, 1),
	}}
	replies := m.perms[0].reply

	_, _, res := m.applyRemoteCommand(Command{Type: CommandPermission, OptionID: "allow"})
	if !res.OK {
		t.Fatalf("an id-less answer was refused: %q", res.Reason)
	}
	if got := <-replies; got != "allow" {
		t.Errorf("reply = %q, want %q", got, "allow")
	}
}

// The matching id is the ordinary case and must still go through.
func TestRemoteCommand_MatchingToolCallIDIsAccepted(t *testing.T) {
	m := newTestModel()
	m.perms = []permissionMsg{{
		req:   acp.PermissionRequest{ToolCall: acp.ToolCall{ToolCallID: "call_2"}},
		reply: make(chan string, 1),
	}}
	replies := m.perms[0].reply

	_, _, res := m.applyRemoteCommand(Command{
		Type: CommandPermission, OptionID: "allow", ToolCallID: "call_2",
	})
	if !res.OK {
		t.Fatalf("the right answer was refused: %q", res.Reason)
	}
	if got := <-replies; got != "allow" {
		t.Errorf("reply = %q, want %q", got, "allow")
	}
}

// A socket that answers for a different workspace is not this workspace's
// chat. Reading its status would show another branch's agent in this row, and
// sending it a prompt would run that prompt in the wrong worktree.
func TestQuery_WrongWorkspace(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, "fix-auth", func(Command) Result { return Result{OK: true} })
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()
	srv.publish(Status{Workspace: "fix-auth", State: StateIdle})

	if _, ok := Query(path, "some-other-branch"); ok {
		t.Error("Query accepted a chat belonging to another workspace")
	}
	if err := Send(path, "some-other-branch", Command{Type: CommandInterrupt}); err == nil {
		t.Error("Send delivered a command to another workspace's chat")
	}

	// And the workspace it really is still works.
	if _, ok := Query(path, "fix-auth"); !ok {
		t.Error("Query refused the chat it was asking for")
	}
}

// A chat that has bound but not published yet says who it is from the first
// moment — otherwise the "starting…" state, which lasts as long as the agent
// takes to answer, could never be attributed to a workspace.
func TestServe_GreetingIsNamed(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	st, ok := Query(path, "fix-auth")
	if !ok {
		t.Fatal("Query failed against a freshly served socket")
	}
	if st.Workspace != "fix-auth" || st.State != StateStarting {
		t.Errorf("greeting = %+v, want workspace fix-auth in state %q", st, StateStarting)
	}
}

// ---------------------------------------------------------------------------
// Protocol version
// ---------------------------------------------------------------------------

// A chat window is never relaunched on upgrade, so the two ends of this socket
// are routinely different binaries. Both facts travel: the number is what code
// compares, and the release is what a row can show somebody.
func TestStatus_SaysWhichOpentreePublishedIt(t *testing.T) {
	m := newTestModel()
	m.opts.Version = "0.5.0"

	st := m.status()
	if st.Protocol != ProtocolVersion {
		t.Errorf("Protocol = %d, want %d", st.Protocol, ProtocolVersion)
	}
	if st.Version != "0.5.0" {
		t.Errorf("Version = %q, want the release this chat is running", st.Version)
	}
	if st.Behind() {
		t.Error("a chat of the reader's own age reported itself out of date")
	}
}

// A chat started before these fields existed publishes neither, and that is the
// case they exist for. Reading a zero as "older than me" is the whole point;
// refusing it would break exactly the upgrade the version is here to survive.
func TestStatus_NoProtocolIsAnOlderChat(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()
	srv.publish(Status{Workspace: "fix-auth", State: StateIdle, Tool: "grep"})

	st, ok := Query(path, "fix-auth")
	if !ok {
		t.Fatal("a status with no protocol was refused")
	}
	if !st.Behind() {
		t.Error("a chat that publishes no protocol should read as an older one")
	}
	if st.State != StateIdle || st.Tool != "grep" {
		t.Errorf("status = %+v, want everything else read as it always was", st)
	}
}

// The greeting carries it before anything has been published, so a chat that is
// still starting can already be told apart from one that cannot answer.
func TestServe_GreetingCarriesTheProtocol(t *testing.T) {
	path := socketPath(t)
	srv, err := serve(path, "fix-auth", nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()

	st, ok := Query(path, "fix-auth")
	if !ok {
		t.Fatal("Query failed against a freshly served socket")
	}
	if st.Protocol != ProtocolVersion {
		t.Errorf("greeting Protocol = %d, want %d", st.Protocol, ProtocolVersion)
	}
}

// Stamped by Send rather than by each caller, so the one that forgot would not
// look like a dashboard from before the field existed.
func TestSend_StampsTheProtocol(t *testing.T) {
	path := socketPath(t)
	got := make(chan Command, 1)
	srv, err := serve(path, "fix-auth", func(c Command) Result {
		got <- c
		return Result{OK: true}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = srv.Close() }()
	srv.publish(Status{Workspace: "fix-auth", State: StateWorking})

	if err := Send(path, "fix-auth", Command{Type: CommandInterrupt}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case cmd := <-got:
		if cmd.Protocol != ProtocolVersion {
			t.Errorf("Protocol = %d, want %d", cmd.Protocol, ProtocolVersion)
		}
	case <-time.After(time.Second):
		t.Fatal("the command never arrived")
	}
}

// A command this chat has no case for is an unknown command from a peer of its
// own age, and an out-of-date window from a newer one. Saying "unknown command"
// to the second sends somebody hunting for a bug that is not there.
func TestRemoteCommand_UnknownFromANewerPeerNamesTheOldWindow(t *testing.T) {
	m := newTestModel()
	m.opts.Version = "0.4.1"

	_, _, res := m.applyRemoteCommand(Command{Type: "teleport", Protocol: ProtocolVersion + 1})
	if res.OK {
		t.Fatal("a command this chat has no case for was accepted")
	}
	for _, want := range []string{"0.4.1", "teleport"} {
		if !strings.Contains(res.Reason, want) {
			t.Errorf("reason = %q, want it to mention %q", res.Reason, want)
		}
	}
}
