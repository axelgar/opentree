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
	srv, err := serve(path, func(c Command) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, c)
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

func TestRemoteCommand_AnswersPermission(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	m, _ = applyUpdate(m, perm)
	m, _ = applyUpdate(m, socketCommandMsg{Type: CommandPermission, OptionID: "reject"})

	if m.perm != nil {
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
	m, _ = applyUpdate(m, socketCommandMsg{Type: CommandPermission, OptionID: "once"})
	if m.perm != nil {
		t.Error("nothing should have changed")
	}
}

func TestRemoteCommand_Prompt(t *testing.T) {
	m := newTestModel()
	m, cmd := applyUpdate(m, socketCommandMsg{Type: CommandPrompt, Text: "run the tests"})

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

func TestRemoteCommand_PromptIgnoredWhileBusy(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m, _ = applyUpdate(m, socketCommandMsg{Type: CommandPrompt, Text: "queue this"})
	if len(m.entries) != 0 {
		t.Error("a prompt must not queue while the agent is working")
	}
}

func TestRemoteCommand_EmptyPromptIgnored(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, socketCommandMsg{Type: CommandPrompt, Text: "   "})
	if m.turn {
		t.Error("whitespace should not start a turn")
	}
}

func TestRemoteCommand_UnknownTypeIsInert(t *testing.T) {
	m := newTestModel()
	before := len(m.entries)
	m, _ = applyUpdate(m, socketCommandMsg{Type: "something-new"})
	if len(m.entries) != before {
		t.Error("an unrecognized command should do nothing")
	}
}

var _ tea.Model = Model{}
