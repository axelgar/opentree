package cmd

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/axelgar/opentree/pkg/chat"
)

func TestDispatchBranchName(t *testing.T) {
	nothing := func(string) bool { return false }

	if got := dispatchBranchName("Add dark mode to the settings page", nothing); got != "auto-add-dark-mode-to-the-settings-page" {
		t.Errorf("name = %q", got)
	}
	long := dispatchBranchName(strings.Repeat("very long prompt ", 10), nothing)
	if len(long) > len("auto-")+40 {
		t.Errorf("name = %q, want the slug capped at 40", long)
	}
	if got := dispatchBranchName("!!!", nothing); got != "auto-task" {
		t.Errorf("name = %q, want the fallback for a prompt with no slug in it", got)
	}

	taken := map[string]bool{"auto-fix-login": true, "auto-fix-login-2": true}
	if got := dispatchBranchName("fix login", func(n string) bool { return taken[n] }); got != "auto-fix-login-3" {
		t.Errorf("name = %q, want the first free suffix", got)
	}
}

func TestIssueArg(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"42"}, true},
		{[]string{"fix", "42"}, false},
		{[]string{"0"}, false},
		{[]string{"-3"}, false},
		{[]string{"fix the login page"}, false},
	}
	for _, c := range cases {
		if _, ok := issueArg(c.args); ok != c.want {
			t.Errorf("issueArg(%v) = %v, want %v", c.args, ok, c.want)
		}
	}
}

// statusServer answers chat.Query the way a chat's socket does: each dial gets
// the current Status as a greeting. The test scripts what "current" means.
type statusServer struct {
	mu sync.Mutex
	st chat.Status
	ln net.Listener
}

func newStatusServer(t *testing.T, name string) (*statusServer, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &statusServer{st: chat.Status{Workspace: name, State: chat.StateWorking}, ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			st := s.st
			s.mu.Unlock()
			_ = json.NewEncoder(conn).Encode(st)
			_ = conn.Close()
		}
	}()
	return s, sock
}

func (s *statusServer) set(st chat.Status) {
	st.Workspace = s.st.Workspace
	s.mu.Lock()
	s.st = st
	s.mu.Unlock()
}

func TestWaitForChat_AnswersWhenTheSocketDoes(t *testing.T) {
	_, sock := newStatusServer(t, "ws")
	if err := waitForChat(sock, "ws", time.Second); err != nil {
		t.Fatalf("waitForChat: %v", err)
	}
	if err := waitForChat(filepath.Join(t.TempDir(), "nothing"), "ws", 700*time.Millisecond); err == nil {
		t.Fatal("a socket nobody serves should time out")
	}
}

func TestWaitHeadless_ExitContract(t *testing.T) {
	cases := []struct {
		name     string
		st       chat.Status
		wantCode int // 0 = success
		wantMsg  string
	}{
		{"published", chat.Status{
			State:     chat.StateIdle,
			Autopilot: &chat.AutopilotStatus{Enabled: true, Outcome: "published", PRURL: "https://x/pull/1"},
		}, 0, ""},
		{"halted", chat.Status{
			State:     chat.StateIdle,
			Autopilot: &chat.AutopilotStatus{Enabled: true, Phase: "halted"},
		}, 1, "halted"},
		{"error", chat.Status{State: chat.StateIdle, Error: "ws: autopilot: boom"}, 1, "boom"},
		{"stopped", chat.Status{State: chat.StateStopped}, 2, "stopped"},
		{"blocked", chat.Status{
			State: chat.StateAwaiting,
			Since: time.Now().Add(-2 * time.Minute),
			Permission: &chat.Permission{
				Title: "rm -rf dist",
			},
		}, 3, "rm -rf dist"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, sock := newStatusServer(t, "ws")
			s.set(c.st)
			err := waitHeadless(sock, "ws", 5*time.Second, 10*time.Millisecond)
			if c.wantCode == 0 {
				if err != nil {
					t.Fatalf("waitHeadless: %v, want the published success", err)
				}
				return
			}
			var coded ExitCodeError
			if !errors.As(err, &coded) {
				t.Fatalf("err = %v, want an ExitCodeError", err)
			}
			if coded.Code != c.wantCode || !strings.Contains(coded.Msg, c.wantMsg) {
				t.Errorf("exit = (%d, %q), want (%d, …%q…)", coded.Code, coded.Msg, c.wantCode, c.wantMsg)
			}
		})
	}
}

func TestWaitHeadless_TimesOutWithTheWorkspaceAlive(t *testing.T) {
	_, sock := newStatusServer(t, "ws") // stays working forever
	err := waitHeadless(sock, "ws", 100*time.Millisecond, 10*time.Millisecond)
	var coded ExitCodeError
	if !errors.As(err, &coded) || coded.Code != 4 || !strings.Contains(coded.Msg, "still running") {
		t.Fatalf("err = %v, want code 4 naming the workspace as still alive", err)
	}
}

func TestWaitHeadless_WaitsOutABriefPermission(t *testing.T) {
	// A permission the human answers from the dashboard within the grace
	// period must not fail the run.
	s, sock := newStatusServer(t, "ws")
	s.set(chat.Status{State: chat.StateAwaiting, Since: time.Now(), Permission: &chat.Permission{Title: "go test"}})
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.set(chat.Status{
			State:     chat.StateIdle,
			Autopilot: &chat.AutopilotStatus{Enabled: true, Outcome: "published", PRURL: "https://x/pull/2"},
		})
	}()
	if err := waitHeadless(sock, "ws", 5*time.Second, 10*time.Millisecond); err != nil {
		t.Fatalf("waitHeadless: %v — an answered permission is not a failure", err)
	}
}

func TestIssuePromptCarriesTheTask(t *testing.T) {
	// No gh on PATH in this test, so the body fetch quietly fails — the
	// prompt must still stand on the number and title alone.
	t.Setenv("PATH", t.TempDir())
	p := issuePrompt(42, "Add dark mode")
	if !strings.Contains(p, "#42") || !strings.Contains(p, "Add dark mode") {
		t.Errorf("prompt = %q, want the issue number and title", p)
	}
	if !strings.Contains(p, "end your turn") {
		t.Errorf("prompt = %q, want the end-of-turn instruction autopilot keys on", p)
	}
}

// The fake above must keep answering chat.Query the way a real chat does, or
// every test here tests the fake.
func TestStatusServer_AnswersQuery(t *testing.T) {
	s, sock := newStatusServer(t, "ws")
	s.set(chat.Status{State: chat.StateIdle})
	st, ok := chat.Query(sock, "ws")
	if !ok || st.State != chat.StateIdle {
		t.Fatalf("Query = (%+v, %v), want the scripted status", st, ok)
	}
	if _, ok := chat.Query(sock, "other"); ok {
		t.Error("a status for one workspace answered for another — the collision check is gone")
	}
}
