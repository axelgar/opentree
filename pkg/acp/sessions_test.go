package acp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Capabilities, against a recorded claude-agent-acp handshake
// ---------------------------------------------------------------------------

func TestSessionCapabilities_Decode(t *testing.T) {
	var env struct {
		Result InitializeResponse `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, "initialize_claude.json"), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	caps := env.Result.AgentCapabilities

	if !caps.CanList() {
		t.Error("sessionCapabilities.list was advertised and did not survive decoding")
	}
	if !caps.CanReopen() {
		t.Error("an agent advertising loadSession can reopen a conversation")
	}
	// The ones opentree does not act on are ignored rather than rejected: the
	// same handshake carries close, delete, fork and additionalDirectories.
	if caps.SessionCapabilities.Resume == nil {
		t.Error("sessionCapabilities.resume was advertised and did not survive decoding")
	}
}

// ACP spells these capabilities as presence: {} is yes, null and omitted are
// no. Decoding them as booleans would have read every one of them as false.
func TestSessionCapabilities_PresenceIsTheSignal(t *testing.T) {
	tests := []struct {
		name             string
		json             string
		list, reopenable bool
	}{
		{"empty object means supported", `{"sessionCapabilities":{"list":{},"resume":{}}}`, true, true},
		{"null means unsupported", `{"sessionCapabilities":{"list":null,"resume":null}}`, false, false},
		{"omitted means unsupported", `{"sessionCapabilities":{}}`, false, false},
		{"no capabilities at all", `{}`, false, false},
		// The split the spec says will be unified later: load lives on the
		// top-level flag, resume in the session set, and either one reopens.
		{"loadSession alone still reopens", `{"loadSession":true}`, false, true},
		{"resume alone still reopens", `{"sessionCapabilities":{"resume":{}}}`, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var caps AgentCapabilities
			if err := json.Unmarshal([]byte(tc.json), &caps); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if caps.CanList() != tc.list {
				t.Errorf("CanList() = %v, want %v", caps.CanList(), tc.list)
			}
			if caps.CanReopen() != tc.reopenable {
				t.Errorf("CanReopen() = %v, want %v", caps.CanReopen(), tc.reopenable)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Reopening
// ---------------------------------------------------------------------------

type reopenResult struct {
	resp *ReopenResponse
	err  error
}

func reopen(t *testing.T, f *fakeAgent) chan reopenResult {
	t.Helper()
	done := make(chan reopenResult, 1)
	go func() {
		resp, err := f.client.Reopen(context.Background(), "ses_old", "/repo")
		done <- reopenResult{resp, err}
	}()
	return done
}

func TestReopen_PrefersTheMethodThatReplaysHistory(t *testing.T) {
	f := newFakeAgent(t, Handlers{})
	// Both advertised, which is what both agents opentree ships with do.
	f.client.caps = AgentCapabilities{
		LoadSession:         true,
		SessionCapabilities: SessionCapabilities{Resume: &Capability{}},
	}

	done := reopen(t, f)
	req := f.next()
	if req["method"] != "session/load" {
		t.Errorf("method = %v, want session/load — it brings the conversation back", req["method"])
	}
	f.reply(req, `{"configOptions":[{"id":"model","currentValue":"opus"}]}`)

	got := <-done
	if got.err != nil {
		t.Fatalf("Reopen: %v", got.err)
	}
	if !got.resp.Replayed {
		t.Error("a load replays the history, and the caller has to know it did")
	}
	if len(got.resp.ConfigOptions) != 1 {
		t.Errorf("ConfigOptions = %+v, want the one the agent sent", got.resp.ConfigOptions)
	}
}

func TestReopen_FallsBackToResume(t *testing.T) {
	f := newFakeAgent(t, Handlers{})
	// The case the spec describes: an agent that keeps sessions but cannot
	// replay them.
	f.client.caps = AgentCapabilities{
		SessionCapabilities: SessionCapabilities{Resume: &Capability{}},
	}

	done := reopen(t, f)
	req := f.next()
	if req["method"] != "session/resume" {
		t.Errorf("method = %v, want session/resume", req["method"])
	}
	params := req["params"].(map[string]any)
	if params["sessionId"] != "ses_old" || params["cwd"] != "/repo" {
		t.Errorf("params = %v, want the session and its worktree", params)
	}
	f.reply(req, `{}`)

	got := <-done
	if got.err != nil {
		t.Fatalf("Reopen: %v", got.err)
	}
	if got.resp.Replayed {
		t.Error("a resume brings no history, and saying it did leaves an empty log looking like a lost one")
	}
}

// ACP says a client must not call a method the agent never advertised. Asking
// anyway costs a round trip and comes back as a failure that reads like a lost
// conversation rather than an agent that never kept one.
func TestReopen_WithoutEitherCapability_AsksNothing(t *testing.T) {
	f := newFakeAgent(t, Handlers{})

	resp, err := f.client.Reopen(context.Background(), "ses_old", "/repo")
	if !errors.Is(err, ErrCannotReopen) {
		t.Fatalf("err = %v, want ErrCannotReopen", err)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nothing", resp)
	}
	select {
	case m := <-f.sent:
		t.Errorf("wrote %v to an agent that cannot serve it", m)
	case <-time.After(100 * time.Millisecond):
	}
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

func TestListSessionsResponse_Decode(t *testing.T) {
	var env struct {
		Result ListSessionsResponse `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, "session_list.json"), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Result.Sessions) != 3 {
		t.Fatalf("Sessions = %d, want the 3 in the fixture", len(env.Result.Sessions))
	}
	got := env.Result.Sessions[0]
	if got.SessionID == "" || got.Cwd == "" {
		t.Errorf("session = %+v, want an id and a cwd", got)
	}
	if got.Title == "" {
		t.Error("the title is the whole reason a list of ids is worth showing")
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v, want the recorded timestamp parsed", got.UpdatedAt)
	}
}

func TestListSessions_ScopesToTheWorktree(t *testing.T) {
	f := newFakeAgent(t, Handlers{})

	done := make(chan *ListSessionsResponse, 1)
	go func() {
		resp, err := f.client.ListSessions(context.Background(), "/repo/worktree", "")
		if err != nil {
			t.Errorf("ListSessions: %v", err)
		}
		done <- resp
	}()

	req := f.next()
	if req["method"] != "session/list" {
		t.Fatalf("method = %v, want session/list", req["method"])
	}
	params := req["params"].(map[string]any)
	if params["cwd"] != "/repo/worktree" {
		t.Errorf("cwd = %v, want the worktree — the sessions next door are somebody else's work", params["cwd"])
	}
	// An empty cursor is omitted rather than sent as "": the schema types it as
	// an opaque token from a previous response, and "" is not one.
	if _, ok := params["cursor"]; ok {
		t.Errorf("cursor = %v, want it omitted on the first page", params["cursor"])
	}
	f.reply(req, `{"sessions":[{"sessionId":"ses_a","cwd":"/repo/worktree","title":"first","updatedAt":"2026-08-12T12:27:41.764Z"}]}`)

	resp := <-done
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionID != "ses_a" {
		t.Errorf("Sessions = %+v, want the one the agent listed", resp.Sessions)
	}
}
