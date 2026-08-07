package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fakeAgent is a scripted ACP peer wired to a Client over in-memory pipes, so
// the protocol can be tested without spawning anything.
type fakeAgent struct {
	t      *testing.T
	client *Client
	toIn   *io.PipeWriter      // agent -> client
	sent   chan map[string]any // client -> agent, already decoded
}

func newFakeAgent(t *testing.T, h Handlers) *fakeAgent {
	t.Helper()

	inR, inW := io.Pipe()   // agent writes here, client reads
	outR, outW := io.Pipe() // client writes here, agent reads

	f := &fakeAgent{t: t, toIn: inW, sent: make(chan map[string]any, 32)}
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err == nil {
				f.sent <- m
			}
		}
		close(f.sent)
	}()

	f.client = newClient(inR, outW, h)
	t.Cleanup(func() {
		_ = f.client.Close()
		_ = inW.Close()
		_ = outW.Close()
	})
	return f
}

// next returns the next message the client wrote, failing the test if none
// arrives promptly.
func (f *fakeAgent) next() map[string]any {
	f.t.Helper()
	select {
	case m, ok := <-f.sent:
		if !ok {
			f.t.Fatal("client closed its output before sending a message")
		}
		return m
	case <-time.After(2 * time.Second):
		f.t.Fatal("timed out waiting for a message from the client")
		return nil
	}
}

// send pushes one raw JSON line to the client.
func (f *fakeAgent) send(line string) {
	f.t.Helper()
	if _, err := io.WriteString(f.toIn, line+"\n"); err != nil {
		f.t.Fatalf("send: %v", err)
	}
}

// reply answers the request the client just sent, echoing its id.
func (f *fakeAgent) reply(req map[string]any, result string) {
	f.t.Helper()
	id, _ := json.Marshal(req["id"])
	f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + result + `}`)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// updateFixture returns the recorded session/update whose kind (and optionally
// status) match, so tests read against real captured traffic.
func updateFixture(t *testing.T, kind, status string) SessionUpdate {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(string(readFixture(t, "session_updates.jsonl"))))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var env struct {
			Params sessionNotification `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			t.Fatalf("decode session_updates.jsonl: %v", err)
		}
		u := env.Params.Update
		if u.Type != kind {
			continue
		}
		if status != "" && (u.ToolCall == nil || u.ToolCall.Status != status) {
			continue
		}
		return u
	}
	t.Fatalf("no recorded %s update with status %q", kind, status)
	return SessionUpdate{}
}

// ---------------------------------------------------------------------------
// Wire decoding, against recorded opencode traffic
// ---------------------------------------------------------------------------

func TestInitializeResponse_Decode(t *testing.T) {
	var env struct {
		Result InitializeResponse `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, "initialize.json"), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := env.Result
	if got.ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", got.ProtocolVersion)
	}
	if !got.AgentCapabilities.LoadSession {
		t.Error("expected LoadSession capability")
	}
	if !got.AgentCapabilities.PromptCapabilities.Image {
		t.Error("expected image prompt capability")
	}
	if len(got.AuthMethods) != 1 || got.AuthMethods[0].ID != "opencode-login" {
		t.Errorf("AuthMethods = %+v, want one method with id opencode-login", got.AuthMethods)
	}
	if got.AgentInfo == nil || got.AgentInfo.Name != "OpenCode" {
		t.Errorf("AgentInfo = %+v, want name OpenCode", got.AgentInfo)
	}
}

func TestNewSessionResponse_Decode(t *testing.T) {
	var env struct {
		Result NewSessionResponse `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, "session_new.json"), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(env.Result.SessionID, "ses_") {
		t.Errorf("SessionID = %q, want a ses_ prefix", env.Result.SessionID)
	}
	var model *ConfigOption
	for i := range env.Result.ConfigOptions {
		if env.Result.ConfigOptions[i].ID == "model" {
			model = &env.Result.ConfigOptions[i]
		}
	}
	if model == nil {
		t.Fatal("no model config option decoded")
	}
	if model.CurrentValue == "" {
		t.Error("model option has no CurrentValue")
	}
	if len(model.Options) == 0 {
		t.Error("model option has no choices")
	}
}

func TestPromptResponse_Decode(t *testing.T) {
	var env struct {
		Result PromptResponse `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, "prompt_response.json"), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Result.StopReason != StopEndTurn {
		t.Errorf("StopReason = %q, want %q", env.Result.StopReason, StopEndTurn)
	}
	if env.Result.Usage == nil || env.Result.Usage.TotalTokens == 0 {
		t.Errorf("Usage = %+v, want non-zero TotalTokens", env.Result.Usage)
	}
}

func TestPermissionRequest_Decode(t *testing.T) {
	var env struct {
		Params PermissionRequest `json:"params"`
	}
	if err := json.Unmarshal(readFixture(t, "request_permission.json"), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Params.ToolCall.Kind != "execute" {
		t.Errorf("ToolCall.Kind = %q, want execute", env.Params.ToolCall.Kind)
	}
	// The recorded agent offers three options and no reject_always, which is
	// why the UI must render what it receives.
	kinds := make([]string, len(env.Params.Options))
	for i, o := range env.Params.Options {
		kinds[i] = o.Kind
	}
	want := []string{PermissionAllowOnce, PermissionAllowAlways, PermissionRejectOnce}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("option kinds = %v, want %v", kinds, want)
	}
}

func TestSessionUpdate_MessageChunk(t *testing.T) {
	u := updateFixture(t, UpdateAgentMessage, "")
	if u.Message == nil {
		t.Fatal("Message is nil")
	}
	if u.Message.Content.Type != "text" || u.Message.Content.Text == "" {
		t.Errorf("Content = %+v, want a non-empty text block", u.Message.Content)
	}
	if u.ToolCall != nil {
		t.Error("ToolCall should be nil for a message chunk")
	}
}

func TestSessionUpdate_ToolCallWithDiff(t *testing.T) {
	// The completed edit is the only recorded update carrying a diff block.
	sc := bufio.NewScanner(strings.NewReader(string(readFixture(t, "session_updates.jsonl"))))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var diff *ToolCallContent
	for sc.Scan() {
		var env struct {
			Params sessionNotification `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Params.Update.ToolCall == nil {
			continue
		}
		for i, c := range env.Params.Update.ToolCall.Content {
			if c.Type == "diff" {
				diff = &env.Params.Update.ToolCall.Content[i]
			}
		}
	}
	if diff == nil {
		t.Fatal("no diff content block found in recorded traffic")
	}
	if diff.Path == "" {
		t.Error("diff block has no Path")
	}
	if diff.NewText == diff.OldText {
		t.Errorf("diff OldText and NewText are identical: %q", diff.OldText)
	}
}

func TestSessionUpdate_WrappedTextContent(t *testing.T) {
	u := updateFixture(t, UpdateToolCallUpdate, StatusCompleted)
	if u.ToolCall == nil || len(u.ToolCall.Content) == 0 {
		t.Fatal("expected a completed tool call carrying content")
	}
	var found bool
	for _, c := range u.ToolCall.Content {
		if c.Type == "content" {
			found = true
			if c.Content == nil || c.Content.Text == "" {
				t.Errorf("wrapped content = %+v, want a nested text block", c.Content)
			}
		}
	}
	if !found {
		t.Error("no wrapped content block; the nesting may have changed")
	}
}

func TestSessionUpdate_Usage(t *testing.T) {
	u := updateFixture(t, UpdateUsage, "")
	if u.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if u.Usage.Size == 0 {
		t.Error("Usage.Size = 0, want the context window size")
	}
	if u.Usage.Cost == nil || u.Usage.Cost.Currency == "" {
		t.Errorf("Cost = %+v, want an amount and currency", u.Usage.Cost)
	}
}

func TestSessionUpdate_Commands(t *testing.T) {
	u := updateFixture(t, UpdateCommands, "")
	if len(u.Commands) == 0 {
		t.Fatal("no commands decoded")
	}
	if u.Commands[0].Name == "" {
		t.Errorf("command = %+v, want a name", u.Commands[0])
	}
}

func TestSessionUpdate_UnknownKindIsInert(t *testing.T) {
	var u SessionUpdate
	if err := json.Unmarshal([]byte(`{"sessionUpdate":"future_thing","whatever":1}`), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.Type != "future_thing" {
		t.Errorf("Type = %q, want future_thing", u.Type)
	}
	if u.Message != nil || u.ToolCall != nil || u.Usage != nil || u.Commands != nil {
		t.Error("unknown update kind should leave every payload nil")
	}
}

// ---------------------------------------------------------------------------
// ToolCall.Merge
// ---------------------------------------------------------------------------

func TestToolCall_Merge(t *testing.T) {
	tests := []struct {
		name  string
		base  ToolCall
		patch ToolCall
		want  ToolCall
	}{
		{
			name:  "status advances",
			base:  ToolCall{ToolCallID: "t1", Title: "bash", Kind: "execute", Status: StatusPending},
			patch: ToolCall{ToolCallID: "t1", Status: StatusInProgress},
			want:  ToolCall{ToolCallID: "t1", Title: "bash", Kind: "execute", Status: StatusInProgress},
		},
		{
			name:  "absent kind and title are kept",
			base:  ToolCall{ToolCallID: "t1", Title: "read", Kind: "read", Status: StatusInProgress},
			patch: ToolCall{ToolCallID: "t1", Status: StatusCompleted},
			want:  ToolCall{ToolCallID: "t1", Title: "read", Kind: "read", Status: StatusCompleted},
		},
		{
			name:  "title is overwritten when present",
			base:  ToolCall{ToolCallID: "t1", Title: "bash"},
			patch: ToolCall{ToolCallID: "t1", Title: "echo hi"},
			want:  ToolCall{ToolCallID: "t1", Title: "echo hi"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.base
			got.Merge(tt.patch)
			if got.Title != tt.want.Title || got.Kind != tt.want.Kind || got.Status != tt.want.Status {
				t.Errorf("Merge() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestToolCall_MergeReplacesContent(t *testing.T) {
	base := ToolCall{ToolCallID: "t1", Content: []ToolCallContent{{Type: "content"}}}
	base.Merge(ToolCall{ToolCallID: "t1", Content: []ToolCallContent{
		{Type: "content"}, {Type: "diff", Path: "a.go"},
	}})
	// Content arrives cumulatively, so a two-item patch must not become three.
	if len(base.Content) != 2 {
		t.Errorf("Content length = %d, want 2 (replaced, not appended)", len(base.Content))
	}
}

func TestToolCall_MergeRealSequence(t *testing.T) {
	// Replay the recorded pending -> in_progress -> completed sequence for one
	// tool call and check nothing is lost along the way.
	sc := bufio.NewScanner(strings.NewReader(string(readFixture(t, "session_updates.jsonl"))))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var call ToolCall
	var sawPending, sawCompleted bool
	for sc.Scan() {
		var env struct {
			Params sessionNotification `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		tc := env.Params.Update.ToolCall
		if tc == nil {
			continue
		}
		switch tc.Status {
		case StatusPending:
			call = *tc
			sawPending = true
		case StatusCompleted:
			if sawPending && !sawCompleted {
				call.Merge(*tc)
				sawCompleted = true
			}
		}
	}
	if !sawPending || !sawCompleted {
		t.Skip("recorded traffic lacks a pending->completed pair")
	}
	if call.Kind == "" {
		t.Error("Kind was lost merging the terminal update, which omits it")
	}
	if call.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", call.Status, StatusCompleted)
	}
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

func TestInitialize_RoundTrip(t *testing.T) {
	f := newFakeAgent(t, Handlers{})

	type result struct {
		resp *InitializeResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := f.client.Initialize(context.Background(), "opentree", "1.2.3")
		done <- result{resp, err}
	}()

	req := f.next()
	if req["method"] != methodInitialize {
		t.Fatalf("method = %v, want %s", req["method"], methodInitialize)
	}
	params := req["params"].(map[string]any)
	if params["protocolVersion"] != float64(ProtocolVersion) {
		t.Errorf("protocolVersion = %v, want %d", params["protocolVersion"], ProtocolVersion)
	}
	// Capabilities we do not have must be sent as explicit false, since an
	// omitted capability and a false one mean the same thing to the agent but
	// only one of them is unambiguous to read.
	caps := params["clientCapabilities"].(map[string]any)
	if caps["terminal"] != false {
		t.Errorf("terminal capability = %v, want explicit false", caps["terminal"])
	}
	fs := caps["fs"].(map[string]any)
	if fs["readTextFile"] != false || fs["writeTextFile"] != false {
		t.Errorf("fs capabilities = %v, want explicit false", fs)
	}

	f.reply(req, `{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"authMethods":[]}`)

	got := <-done
	if got.err != nil {
		t.Fatalf("Initialize: %v", got.err)
	}
	if !got.resp.AgentCapabilities.LoadSession {
		t.Error("expected LoadSession capability")
	}
}

func TestCall_AgentError(t *testing.T) {
	f := newFakeAgent(t, Handlers{})

	errc := make(chan error, 1)
	go func() {
		_, err := f.client.NewSession(context.Background(), "/tmp/x")
		errc <- err
	}()

	req := f.next()
	id, _ := json.Marshal(req["id"])
	f.send(`{"jsonrpc":"2.0","id":` + string(id) +
		`,"error":{"code":-32000,"message":"Authentication required"}}`)

	err := <-errc
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsAuthRequired(err) {
		t.Errorf("IsAuthRequired(%v) = false, want true", err)
	}
}

func TestIsAuthRequired_OtherCodes(t *testing.T) {
	if IsAuthRequired(&Error{Code: -32601, Message: "Method not found"}) {
		t.Error("method-not-found should not read as auth required")
	}
	if IsAuthRequired(nil) {
		t.Error("nil should not read as auth required")
	}
}

func TestUpdates_DeliveredInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	f := newFakeAgent(t, Handlers{Update: func(u SessionUpdate) {
		mu.Lock()
		defer mu.Unlock()
		if u.Message != nil {
			got = append(got, u.Message.Content.Text)
		}
	}})

	for _, word := range []string{"one", "two", "three"} {
		f.send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_1",` +
			`"update":{"sessionUpdate":"agent_message_chunk","messageId":"m1",` +
			`"content":{"type":"text","text":"` + word + `"}}}}`)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != "one,two,three" {
		t.Errorf("updates = %v, want [one two three] in order", got)
	}
}

func TestPermission_HandlerChoosesOption(t *testing.T) {
	f := newFakeAgent(t, Handlers{Permission: func(req PermissionRequest) string {
		for _, o := range req.Options {
			if o.Kind == PermissionAllowOnce {
				return o.OptionID
			}
		}
		return ""
	}})

	// The agent's request ids are their own space and start at 0, overlapping
	// ids the client uses for its own calls.
	f.send(`{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":{` +
		`"sessionId":"ses_1","toolCall":{"toolCallId":"t1","title":"rm -rf dist","kind":"execute"},` +
		`"options":[{"optionId":"once","kind":"allow_once","name":"Allow once"},` +
		`{"optionId":"reject","kind":"reject_once","name":"Reject"}]}}`)

	resp := f.next()
	if fmt.Sprint(resp["id"]) != "0" {
		t.Errorf("response id = %v, want 0 (echoed from the request)", resp["id"])
	}
	outcome := resp["result"].(map[string]any)["outcome"].(map[string]any)
	if outcome["outcome"] != "selected" {
		t.Errorf("outcome = %v, want selected", outcome["outcome"])
	}
	if outcome["optionId"] != "once" {
		t.Errorf("optionId = %v, want once", outcome["optionId"])
	}
}

func TestPermission_HandlerDeclines(t *testing.T) {
	f := newFakeAgent(t, Handlers{Permission: func(PermissionRequest) string { return "" }})

	f.send(`{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":{` +
		`"sessionId":"ses_1","toolCall":{"toolCallId":"t1"},` +
		`"options":[{"optionId":"once","kind":"allow_once","name":"Allow once"}]}}`)

	outcome := f.next()["result"].(map[string]any)["outcome"].(map[string]any)
	if outcome["outcome"] != "cancelled" {
		t.Errorf("outcome = %v, want cancelled", outcome["outcome"])
	}
	if _, ok := outcome["optionId"]; ok {
		t.Error("a cancelled outcome should carry no optionId")
	}
}

func TestUnsupportedRequest_IsAnswered(t *testing.T) {
	// We declare no fs capability, but an agent asking anyway must not be left
	// blocked forever.
	f := newFakeAgent(t, Handlers{})
	f.send(`{"jsonrpc":"2.0","id":7,"method":"fs/read_text_file","params":{"path":"/tmp/x"}}`)

	resp := f.next()
	if fmt.Sprint(resp["id"]) != "7" {
		t.Errorf("response id = %v, want 7", resp["id"])
	}
	if _, ok := resp["result"]; !ok {
		t.Errorf("expected a result, got %v", resp)
	}
}

func TestIncomingRequestIDDoesNotResolvePendingCall(t *testing.T) {
	// The client's first call takes id 1. An agent request that also uses id 1
	// must not be mistaken for its response.
	f := newFakeAgent(t, Handlers{Permission: func(PermissionRequest) string { return "once" }})

	errc := make(chan error, 1)
	go func() {
		_, err := f.client.NewSession(context.Background(), "/tmp/x")
		errc <- err
	}()

	req := f.next()
	if fmt.Sprint(req["id"]) != "1" {
		t.Fatalf("first call id = %v, want 1", req["id"])
	}

	f.send(`{"jsonrpc":"2.0","id":1,"method":"session/request_permission","params":{` +
		`"sessionId":"ses_1","toolCall":{"toolCallId":"t1"},` +
		`"options":[{"optionId":"once","kind":"allow_once","name":"Allow once"}]}}`)

	// That was the permission answer, not a session/new response.
	if got := f.next(); got["method"] != nil {
		t.Fatalf("expected the permission response, got %v", got)
	}

	select {
	case err := <-errc:
		t.Fatalf("NewSession returned early with %v; the agent's request was mistaken for a response", err)
	case <-time.After(150 * time.Millisecond):
	}

	f.reply(req, `{"sessionId":"ses_real"}`)
	if err := <-errc; err != nil {
		t.Fatalf("NewSession: %v", err)
	}
}

func TestAgentExit_FailsPendingCall(t *testing.T) {
	f := newFakeAgent(t, Handlers{})

	errc := make(chan error, 1)
	go func() {
		_, err := f.client.NewSession(context.Background(), "/tmp/x")
		errc <- err
	}()
	f.next() // let the request go out first

	_ = f.toIn.Close() // agent dies

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected an error when the agent exits mid-call")
		}
		if !strings.Contains(err.Error(), "closed the connection") {
			t.Errorf("error = %q, want it to mention the closed connection", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call was never failed")
	}
}

func TestCall_ContextCancelled(t *testing.T) {
	f := newFakeAgent(t, Handlers{})
	ctx, cancel := context.WithCancel(context.Background())

	errc := make(chan error, 1)
	go func() {
		_, err := f.client.Prompt(ctx, "ses_1", []ContentBlock{TextBlock("hello")})
		errc <- err
	}()
	f.next()
	cancel()

	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt ignored its context")
	}
}

func TestPrompt_SendsTextBlock(t *testing.T) {
	f := newFakeAgent(t, Handlers{})
	go func() {
		_, _ = f.client.Prompt(context.Background(), "ses_1", []ContentBlock{TextBlock("do the thing")})
	}()

	params := f.next()["params"].(map[string]any)
	if params["sessionId"] != "ses_1" {
		t.Errorf("sessionId = %v, want ses_1", params["sessionId"])
	}
	blocks := params["prompt"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("prompt blocks = %d, want 1", len(blocks))
	}
	block := blocks[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "do the thing" {
		t.Errorf("block = %v, want a text block carrying the prompt", block)
	}
}

func TestSpawn_MissingBinary(t *testing.T) {
	_, err := Spawn(context.Background(), "opentree-no-such-agent", nil, t.TempDir(), Handlers{})
	if err == nil {
		t.Fatal("expected an error spawning a binary that does not exist")
	}
}

func TestSpawn_HandshakeWithStubAgent(t *testing.T) {
	// A stub that answers exactly one initialize, proving the subprocess path
	// works end to end without needing a real agent installed.
	dir := t.TempDir()
	script := filepath.Join(dir, "stub-agent")
	body := "#!/bin/sh\nread -r line\n" +
		`printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,` +
		`"agentCapabilities":{"loadSession":true},"authMethods":[]}}\n'` + "\nsleep 5\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}

	client, err := Spawn(context.Background(), script, nil, dir, Handlers{})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Initialize(ctx, "opentree", "test")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !resp.AgentCapabilities.LoadSession {
		t.Error("expected LoadSession from the stub agent")
	}
}

func TestRing_KeepsLastLines(t *testing.T) {
	r := &ring{max: 2}
	r.drain(strings.NewReader("first\nsecond\nthird\n"))
	if got := r.String(); got != "second\nthird" {
		t.Errorf("ring = %q, want %q", got, "second\nthird")
	}
}
