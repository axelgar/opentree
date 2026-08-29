package cmd

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/axelgar/opentree/pkg/chat"
)

func TestNewFlagConflict(t *testing.T) {
	cases := []struct {
		name       string
		fromRemote bool
		agent      string
		agents     []string
		prompt     string
		wantErr    string
	}{
		{name: "plain", wantErr: ""},
		{name: "agent alone", agent: "claude", wantErr: ""},
		{name: "agents alone", agents: []string{"claude", "gemini"}, wantErr: ""},
		{name: "agents with prompt", agents: []string{"claude"}, prompt: "task", wantErr: ""},
		{name: "agent and agents", agent: "claude", agents: []string{"gemini"}, wantErr: "--agent and --agents"},
		{name: "agents and remote", fromRemote: true, agents: []string{"claude"}, wantErr: "--agents cannot be combined with --remote"},
		{name: "agent and remote", fromRemote: true, agent: "claude", wantErr: "--agent cannot be combined with --remote"},
		{name: "prompt without agents", prompt: "task", wantErr: "--prompt only makes sense with --agents"},
	}
	for _, c := range cases {
		err := newFlagConflict(c.fromRemote, c.agent, c.agents, c.prompt)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.wantErr)
		}
	}
}

// promptServer answers chat.Send the way a chat's socket does — greeting,
// then one command acknowledged OK — and records what arrived. statusServer
// is not enough here: Send waits for a Result after the command, and a server
// that hangs up after the greeting would read as a dead chat.
type promptServer struct {
	mu  sync.Mutex
	got []chat.Command
}

func newPromptServer(t *testing.T, name string) (*promptServer, string) {
	t.Helper()
	sock := shortSocketPath(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &promptServer{}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			enc := json.NewEncoder(conn)
			dec := json.NewDecoder(conn)
			_ = enc.Encode(chat.Status{Workspace: name, State: chat.StateIdle})
			var cmd chat.Command
			// A Query connection closes after the greeting; only a Send
			// delivers a command worth recording and answering.
			if dec.Decode(&cmd) == nil {
				s.mu.Lock()
				s.got = append(s.got, cmd)
				s.mu.Unlock()
				_ = enc.Encode(chat.Result{OK: true})
			}
			_ = conn.Close()
		}
	}()
	return s, sock
}

func (s *promptServer) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.got {
		if c.Type == chat.CommandPrompt {
			out = append(out, c.Text)
		}
	}
	return out
}

func TestSendFanoutPrompts_DeliversToEverySibling(t *testing.T) {
	alpha, alphaSock := newPromptServer(t, "feat/x-claude")
	beta, betaSock := newPromptServer(t, "feat/x-gemini")
	socks := map[string]string{"feat/x-claude": alphaSock, "feat/x-gemini": betaSock}
	sockFor := func(name string) string { return socks[name] }

	errs := sendFanoutPrompts(sockFor, []string{"feat/x-claude", "feat/x-gemini"}, "the task", 2*time.Second)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	for name, srv := range map[string]*promptServer{"feat/x-claude": alpha, "feat/x-gemini": beta} {
		if got := srv.prompts(); len(got) != 1 || got[0] != "the task" {
			t.Errorf("%s received prompts %v, want exactly the task", name, got)
		}
	}
}

func TestSendFanoutPrompts_ReportsDeadSiblingAndContinues(t *testing.T) {
	alpha, alphaSock := newPromptServer(t, "feat/x-claude")
	socks := map[string]string{
		"feat/x-claude": alphaSock,
		"feat/x-dead":   shortSocketPath(t), // nothing listens here
	}
	sockFor := func(name string) string { return socks[name] }

	errs := sendFanoutPrompts(sockFor, []string{"feat/x-dead", "feat/x-claude"}, "the task", 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly the dead sibling's", errs)
	}
	if !strings.Contains(errs[0].Error(), "feat/x-dead") {
		t.Errorf("err = %v, want it to name the sibling it failed for", errs[0])
	}
	// The dead sibling must not stop the live one from getting the task.
	if got := alpha.prompts(); len(got) != 1 || got[0] != "the task" {
		t.Errorf("live sibling received prompts %v, want exactly the task", got)
	}
}
