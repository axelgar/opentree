package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestModel builds a Model with no agent behind it. Tests that only exercise
// in-process logic should use this instead of Run, which spawns a subprocess.
func newTestModel() Model {
	ta := textarea.New()
	ta.SetHeight(inputHeight)
	ta.KeyMap.InsertNewline.SetKeys(keys.Newline.Keys()...)
	ta.Focus()
	m := Model{
		opts:      Options{Workspace: "fix-auth", Agent: "OpenCode", Cwd: "/repo"},
		toolIdx:   make(map[string]int),
		input:     ta,
		help:      help.New(),
		keys:      keys,
		sessionID: "ses_test",
		width:     100,
		height:    30,
		ready:     true,
	}
	return m.relayout()
}

// newUnsizedTestModel is a freshly constructed model that has not yet seen a
// WindowSizeMsg — the state Run is in between newModel and the first frame.
func newUnsizedTestModel() Model {
	ta := textarea.New()
	ta.SetHeight(inputHeight)
	ta.Focus()
	return Model{
		opts:    Options{Workspace: "fix-auth", Agent: "OpenCode"},
		toolIdx: make(map[string]int),
		input:   ta,
		help:    help.New(),
		keys:    keys,
	}
}

func applyUpdate(m Model, msg tea.Msg) (Model, tea.Cmd) {
	newM, cmd := m.Update(msg)
	return newM.(Model), cmd
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func textUpdate(kind, text string) acpUpdateMsg {
	return acpUpdateMsg(acp.SessionUpdate{
		Type:    kind,
		Message: &acp.MessageChunk{Content: acp.ContentBlock{Type: "text", Text: text}},
	})
}

func toolUpdate(kind string, call acp.ToolCall) acpUpdateMsg {
	return acpUpdateMsg(acp.SessionUpdate{Type: kind, ToolCall: &call})
}

func permission(options ...acp.PermissionOption) permissionMsg {
	return permissionMsg{
		req: acp.PermissionRequest{
			ToolCall: acp.ToolCall{ToolCallID: "t1", Title: "rm -rf dist", Kind: "execute"},
			Options:  options,
		},
		reply: make(chan string, 1),
	}
}

var (
	allowOnce   = acp.PermissionOption{OptionID: "once", Kind: acp.PermissionAllowOnce, Name: "Allow once"}
	allowAlways = acp.PermissionOption{OptionID: "always", Kind: acp.PermissionAllowAlways, Name: "Always allow"}
	rejectOnce  = acp.PermissionOption{OptionID: "reject", Kind: acp.PermissionRejectOnce, Name: "Reject"}
)

// ---------------------------------------------------------------------------
// Conversation log
// ---------------------------------------------------------------------------

func TestAgentChunks_MergeIntoOneEntry(t *testing.T) {
	m := newTestModel()
	for _, chunk := range []string{"Hello", ", ", "world"} {
		m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, chunk))
	}
	if len(m.entries) != 1 {
		t.Fatalf("entries = %d, want 1 merged entry", len(m.entries))
	}
	if m.entries[0].text != "Hello, world" {
		t.Errorf("text = %q, want %q", m.entries[0].text, "Hello, world")
	}
}

func TestThoughtAndMessage_StaySeparate(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentThought, "thinking"))
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "answer"))
	if len(m.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(m.entries))
	}
	if m.entries[0].kind != entryThought || m.entries[1].kind != entryAgent {
		t.Errorf("kinds = %v/%v, want thought then agent", m.entries[0].kind, m.entries[1].kind)
	}
}

func TestUserMessageReplay_AppendsEntry(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateUserMessage, "do the thing"))
	if len(m.entries) != 1 || m.entries[0].kind != entryUser {
		t.Fatalf("entries = %+v, want one user entry", m.entries)
	}
}

func TestToolCall_UpdatesInPlace(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, acp.ToolCall{
		ToolCallID: "t1", Title: "bash", Kind: "execute", Status: acp.StatusPending,
	}))
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCallUpdate, acp.ToolCall{
		ToolCallID: "t1", Status: acp.StatusCompleted, Title: "echo hi",
	}))

	if len(m.entries) != 1 {
		t.Fatalf("entries = %d, want the tool call patched in place, not duplicated", len(m.entries))
	}
	got := m.entries[0].tool
	if got.Status != acp.StatusCompleted {
		t.Errorf("Status = %q, want %q", got.Status, acp.StatusCompleted)
	}
	// Kind is absent from the terminal update and must survive the merge.
	if got.Kind != "execute" {
		t.Errorf("Kind = %q, want execute to be retained", got.Kind)
	}
}

func TestUsageUpdate_IsRecorded(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, acpUpdateMsg(acp.SessionUpdate{
		Type:  acp.UpdateUsage,
		Usage: &acp.ContextUsage{Used: 500, Size: 1000, Cost: &acp.Cost{Amount: 1.5, Currency: "USD"}},
	}))
	if m.usage == nil {
		t.Fatal("usage was not recorded")
	}
	if !strings.Contains(m.header(), "50% ctx") {
		t.Errorf("header = %q, want it to show context usage", m.header())
	}
	if !strings.Contains(m.header(), "$1.5000") {
		t.Errorf("header = %q, want it to show cost", m.header())
	}
}

func TestAcpUpdate_ReissuesTheReader(t *testing.T) {
	// Losing this cmd stops the stream dead, so it is worth pinning.
	m := newTestModel()
	_, cmd := applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "hi"))
	if cmd == nil {
		t.Error("expected the update handler to re-issue waitForMsg")
	}
}

// ---------------------------------------------------------------------------
// Sending
// ---------------------------------------------------------------------------

func TestSend_AppendsUserEntryAndStartsTurn(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("make login idempotent")

	m, cmd := applyUpdate(m, keyMsg("enter"))
	if !m.turn {
		t.Error("expected a turn to be in flight")
	}
	if cmd == nil {
		t.Error("expected a prompt cmd")
	}
	if len(m.entries) != 1 || m.entries[0].kind != entryUser {
		t.Fatalf("entries = %+v, want one user entry", m.entries)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared after send", m.input.Value())
	}
}

func TestSend_EmptyInputIsIgnored(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("   ")
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.turn {
		t.Error("whitespace should not start a turn")
	}
	if len(m.entries) != 0 {
		t.Errorf("entries = %d, want 0", len(m.entries))
	}
}

func TestSend_IgnoredWhileTurnInFlight(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m.input.SetValue("second prompt")
	m, _ = applyUpdate(m, keyMsg("enter"))
	if len(m.entries) != 0 {
		t.Error("a second prompt should not queue while the agent is working")
	}
}

func TestSend_IgnoredBeforeSessionExists(t *testing.T) {
	m := newTestModel()
	m.sessionID = ""
	m.input.SetValue("too early")
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.turn {
		t.Error("cannot prompt before the session is ready")
	}
}

func TestPromptDone_EndsTurnAndSummarizes(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{
		StopReason: acp.StopEndTurn,
		Usage:      &acp.TokenUsage{InputTokens: 10, OutputTokens: 20},
	}})
	if m.turn {
		t.Error("turn should be over")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != entryNotice || !strings.Contains(last.text, "end_turn") {
		t.Errorf("last entry = %+v, want a notice naming the stop reason", last)
	}
}

func TestPromptError_Surfaces(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m, _ = applyUpdate(m, promptDoneMsg{err: errString("boom")})
	if m.err == nil {
		t.Fatal("expected the error to be surfaced")
	}
	if !strings.Contains(m.statusLine(), "boom") {
		t.Errorf("statusLine = %q, want it to show the error", m.statusLine())
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

func TestPermission_HeldUntilAnswered(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	m, cmd := applyUpdate(m, perm)
	if m.perm == nil {
		t.Fatal("permission was not held")
	}
	if cmd == nil {
		t.Error("expected the reader to be re-issued while a permission is pending")
	}
	if len(perm.reply) != 0 {
		t.Error("nothing should be sent to the agent before the user answers")
	}
}

func TestPermission_AnswerByKind(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"a", "once"},
		{"A", "always"},
		{"d", "reject"},
		{"1", "once"},
		{"3", "reject"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m := newTestModel()
			perm := permission(allowOnce, allowAlways, rejectOnce)
			m, _ = applyUpdate(m, perm)
			m, _ = applyUpdate(m, keyMsg(tt.key))

			if m.perm != nil {
				t.Error("permission should be cleared once answered")
			}
			select {
			case got := <-perm.reply:
				if got != tt.want {
					t.Errorf("reply = %q, want %q", got, tt.want)
				}
			default:
				t.Fatal("the agent was never answered")
			}
		})
	}
}

func TestPermission_EscCancels(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	m, _ = applyUpdate(m, perm)
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.perm != nil {
		t.Error("permission should be cleared after esc")
	}

	select {
	case got := <-perm.reply:
		if got != "" {
			t.Errorf("reply = %q, want an empty id to cancel", got)
		}
	default:
		t.Fatal("esc must still answer the agent, or the turn hangs")
	}
}

func TestPermission_UnofferedKindIsIgnored(t *testing.T) {
	// opencode offers no reject_always; pressing a key for a kind that was not
	// offered must do nothing rather than pick the wrong option.
	m := newTestModel()
	perm := permission(allowOnce)
	m, _ = applyUpdate(m, perm)
	m, _ = applyUpdate(m, keyMsg("d"))

	if m.perm == nil {
		t.Error("permission should still be pending")
	}
	if len(perm.reply) != 0 {
		t.Error("no answer should have been sent")
	}
}

func TestOptionForKey_OutOfRangeDigit(t *testing.T) {
	if _, ok := optionForKey("9", []acp.PermissionOption{allowOnce}); ok {
		t.Error("digit beyond the option count should not match")
	}
}

func TestPermissionView_RendersOfferedOptionsOnly(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, permission(allowOnce, rejectOnce))
	view := m.permissionView()

	for _, want := range []string{"rm -rf dist", "Allow once", "Reject", "[a]", "[d]"} {
		if !strings.Contains(view, want) {
			t.Errorf("permissionView() missing %q\ngot: %s", want, view)
		}
	}
	if strings.Contains(view, "Always allow") {
		t.Error("an option the agent did not offer must not be rendered")
	}
}

func TestFooterHeight_GrowsWithOptions(t *testing.T) {
	m := newTestModel()
	base := m.footerHeight()
	m, _ = applyUpdate(m, permission(allowOnce, allowAlways, rejectOnce))
	if got := m.footerHeight(); got <= base {
		t.Errorf("footerHeight with a dialog = %d, want more than %d", got, base)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle and rendering
// ---------------------------------------------------------------------------

func TestAgentGone_SurfacesError(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m.opts.Command = "opencode"
	m, _ = applyUpdate(m, agentGoneMsg{})
	if m.err == nil || !strings.Contains(m.err.Error(), "opencode") {
		t.Errorf("err = %v, want it to name the agent that exited", m.err)
	}
	if m.turn {
		t.Error("a dead agent ends the turn")
	}
}

func TestSessionReady_SetsModelAndNote(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, sessionReadyMsg{
		id:      "ses_new",
		options: []acp.ConfigOption{{ID: "model", CurrentValue: "some/model"}},
		note:    "could not resume ses_old",
	})
	if m.sessionID != "ses_new" {
		t.Errorf("sessionID = %q, want ses_new", m.sessionID)
	}
	if m.modelName != "some/model" {
		t.Errorf("modelName = %q, want some/model", m.modelName)
	}
	if len(m.entries) != 1 || m.entries[0].kind != entryNotice {
		t.Errorf("entries = %+v, want the resume failure recorded as a notice", m.entries)
	}
}

func TestCancel_OnlyWhileTurnInFlight(t *testing.T) {
	// With no client wired up, calling Cancel would panic; reaching the end of
	// this test proves the idle path does not touch it.
	m := newTestModel()
	m.turn = false
	if _, cmd := applyUpdate(m, keyMsg("esc")); cmd != nil {
		t.Error("esc while idle should do nothing")
	}
}

func TestView_ShowsHeaderAndHelp(t *testing.T) {
	m := newTestModel()
	m.modelName = "github-copilot/claude-sonnet-4.6"
	m = m.relayout()
	view := m.View()

	for _, want := range []string{"fix-auth", "OpenCode", "claude-sonnet-4.6", "enter", "send"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q", want)
		}
	}
}

func TestView_NotReady(t *testing.T) {
	m := newUnsizedTestModel()
	if !strings.Contains(m.View(), "starting") {
		t.Errorf("View() before the first resize = %q, want a starting message", m.View())
	}
}

func TestToolLabel(t *testing.T) {
	tests := []struct {
		name string
		call acp.ToolCall
		want string
	}{
		{
			name: "title wins",
			call: acp.ToolCall{Title: "echo hi", Kind: "execute"},
			want: "echo hi",
		},
		{
			name: "kind is the fallback",
			call: acp.ToolCall{Kind: "read"},
			want: "read",
		},
		{
			name: "diff paths are appended and shortened",
			call: acp.ToolCall{Title: "edit", Content: []acp.ToolCallContent{
				{Type: "diff", Path: "/repo/pkg/auth/session.go"},
			}},
			want: "edit (pkg/auth/session.go)",
		},
		{
			name: "an absolute title is shortened",
			call: acp.ToolCall{Title: "/repo/pkg/auth/session.go", Kind: "edit"},
			want: "pkg/auth/session.go",
		},
		{
			name: "the diff path is not repeated after the title",
			call: acp.ToolCall{Title: "main.go", Kind: "edit", Content: []acp.ToolCallContent{
				{Type: "diff", Path: "/repo/main.go"},
			}},
			want: "main.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolLabel(tt.call, "/repo"); got != tt.want {
				t.Errorf("toolLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShortPath_LeavesOutsidePathsAlone(t *testing.T) {
	if got := shortPath("/elsewhere/x.go", "/repo"); got != "/elsewhere/x.go" {
		t.Errorf("shortPath() = %q, want the path untouched", got)
	}
}

func TestPadToBottom(t *testing.T) {
	if got := padToBottom("a\nb\n", 5); !strings.HasPrefix(got, "\n\n\n") {
		t.Errorf("padToBottom() = %q, want leading blank lines", got)
	}
	long := strings.Repeat("x\n", 10)
	if got := padToBottom(long, 3); got != long {
		t.Error("content taller than the viewport should not be padded")
	}
}

func TestTurnSummary(t *testing.T) {
	if got := turnSummary(nil); got != "" {
		t.Errorf("turnSummary(nil) = %q, want empty", got)
	}
	got := turnSummary(&acp.PromptResponse{
		StopReason: acp.StopCancelled,
		Usage:      &acp.TokenUsage{InputTokens: 3, OutputTokens: 4},
	})
	if !strings.Contains(got, acp.StopCancelled) || !strings.Contains(got, "3 in / 4 out") {
		t.Errorf("turnSummary() = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Diffs and tool detail
// ---------------------------------------------------------------------------

func diffCall(status, old, updated string) acp.ToolCall {
	return acp.ToolCall{
		ToolCallID: "t1", Title: "edit", Kind: "edit", Status: status,
		Content: []acp.ToolCallContent{
			{Type: "diff", Path: "/repo/pkg/auth/session.go", OldText: old, NewText: updated},
		},
	}
}

func TestDiffStat(t *testing.T) {
	tests := []struct {
		name             string
		old, updated     string
		wantAdd, wantRem int
	}{
		{"replacement", "a\nb", "a\nb\nc", 3, 2},
		{"pure insertion", "", "new line", 1, 0},
		{"pure deletion", "gone\naway", "", 0, 2},
		{"trailing newline is not a line", "a\n", "b\n", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := diffStat(diffCall(acp.StatusCompleted, tt.old, tt.updated))
			if added != tt.wantAdd || removed != tt.wantRem {
				t.Errorf("diffStat() = +%d/-%d, want +%d/-%d", added, removed, tt.wantAdd, tt.wantRem)
			}
		})
	}
}

func TestRenderTool_ShowsDiffLinesAndStat(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(diffCall(acp.StatusCompleted, "old line", "new line"), 60)

	for _, want := range []string{"pkg/auth/session.go", "+1", "-1", "- old line", "+ new line"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTool() missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderDiffs_TruncatesLargeEdits(t *testing.T) {
	big := strings.TrimSuffix(strings.Repeat("line\n", 50), "\n")
	lines := renderDiffs(diffCall(acp.StatusCompleted, big, big), 80)

	if len(lines) > diffMaxLines+1 {
		t.Errorf("rendered %d lines, want at most %d plus a truncation marker", len(lines), diffMaxLines)
	}
	if !strings.Contains(lines[len(lines)-1], "truncated") {
		t.Errorf("last line = %q, want a truncation marker", lines[len(lines)-1])
	}
}

func TestRenderTool_ShowsFailureReason(t *testing.T) {
	m := newTestModel()
	call := acp.ToolCall{
		ToolCallID: "t1", Title: "rm -rf dist", Kind: "execute", Status: acp.StatusFailed,
		Content: []acp.ToolCallContent{
			{Type: "content", Content: &acp.ContentBlock{Type: "text",
				Text: "The user rejected permission to use this specific tool call."}},
		},
	}
	out := m.renderTool(call, 80)
	if !strings.Contains(out, "rejected permission") {
		t.Errorf("renderTool() should explain a failure\ngot:\n%s", out)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 40); got != "short" {
		t.Errorf("truncate() = %q, want it untouched", got)
	}
	got := truncate(strings.Repeat("x", 50), 10)
	if len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncate() = %q, want 10 runes ending in an ellipsis", got)
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines(""); got != nil {
		t.Errorf("splitLines(\"\") = %v, want nil", got)
	}
	if got := splitLines("a\nb\n"); len(got) != 2 {
		t.Errorf("splitLines() = %v, want 2 lines", got)
	}
}

// ---------------------------------------------------------------------------
// Stopped agent: restart and auth
// ---------------------------------------------------------------------------

func TestAgentGone_FromOldGenerationIsIgnored(t *testing.T) {
	// A restarted agent supersedes its predecessor, whose death notice lands
	// afterwards. Acting on it would kill the healthy replacement.
	m := newTestModel()
	m.generation = 2
	m, _ = applyUpdate(m, agentGoneMsg{generation: 1})

	if m.dead {
		t.Error("a stale generation's exit must not mark the current agent dead")
	}
	if m.err != nil {
		t.Errorf("err = %v, want none", m.err)
	}
}

func TestChannelMessages_AlwaysReissueTheReader(t *testing.T) {
	// Every message that arrives down the handler channel has to hand the
	// reader back, or the stream stops dead at that message. agentGoneMsg got
	// this wrong once: the log came back empty after a restart because the
	// replay had nothing listening for it.
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{"update", textUpdate(acp.UpdateAgentMessage, "hi")},
		{"permission", permission(allowOnce)},
		{"agent gone", agentGoneMsg{generation: 1}},
		{"agent gone from an old generation", agentGoneMsg{generation: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.generation = 1
			if _, cmd := applyUpdate(m, tt.msg); cmd == nil {
				t.Error("expected the reader to be re-issued")
			}
		})
	}
}

func TestAgentGone_CurrentGenerationStops(t *testing.T) {
	m := newTestModel()
	m.generation = 2
	m.turn = true
	m, _ = applyUpdate(m, agentGoneMsg{generation: 2})

	if !m.dead || !m.stopped() {
		t.Error("expected the agent to be marked dead")
	}
	if m.turn {
		t.Error("a dead agent ends the turn")
	}
}

func TestStopped_RestartKey(t *testing.T) {
	m := newTestModel()
	m.launch = func() (*acp.Client, *acp.InitializeResponse, int, error) {
		return nil, nil, 2, nil
	}
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

	_, cmd := applyUpdate(m, keyMsg("r"))
	if cmd == nil {
		t.Fatal("expected r to issue a restart")
	}
	if _, ok := cmd().(clientReadyMsg); !ok {
		t.Errorf("restart produced %T, want clientReadyMsg", cmd())
	}
}

func TestStopped_TypingDoesNotReachTheInput(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})
	m, _ = applyUpdate(m, keyMsg("h"))

	if m.input.Value() != "" {
		t.Errorf("input = %q, want keystrokes ignored while the agent is gone", m.input.Value())
	}
}

func TestClientReady_ClearsLogBeforeReplay(t *testing.T) {
	// session/load replays the whole conversation, so keeping the old entries
	// would render it twice.
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "old history"))
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

	m, cmd := applyUpdate(m, clientReadyMsg{generation: 7})
	if len(m.entries) != 0 {
		t.Errorf("entries = %d, want the log cleared for replay", len(m.entries))
	}
	if m.dead || m.err != nil {
		t.Error("a fresh client clears the stopped state")
	}
	if m.generation != 7 {
		t.Errorf("generation = %d, want 7", m.generation)
	}
	if cmd == nil {
		t.Error("expected the session to be reopened after a restart")
	}
}

func TestAuthRequired_OffersLogin(t *testing.T) {
	m := newTestModel()
	m.opts.Command = "opencode"
	m.opts.AuthCommand = []string{"auth", "login"}
	m.authMethods = []acp.AuthMethod{{ID: "opencode-login", Description: "Run `opencode auth login` in the terminal"}}
	m, _ = applyUpdate(m, errMsg{err: errString("acp error -32000: Authentication required"), auth: true})

	if !m.stopped() {
		t.Fatal("an auth failure should stop the view")
	}
	view := m.footer()
	for _, want := range []string{"Authentication required", "opencode auth login", "[l]", "[r]"} {
		if !strings.Contains(view, want) {
			t.Errorf("footer() missing %q\ngot:\n%s", want, view)
		}
	}
}

func TestAuthDone_TriggersRestart(t *testing.T) {
	m := newTestModel()
	m.launch = func() (*acp.Client, *acp.InitializeResponse, int, error) {
		return nil, nil, 3, nil
	}
	_, cmd := applyUpdate(m, authDoneMsg{})
	if cmd == nil {
		t.Fatal("a successful login should restart the agent")
	}
	if _, ok := cmd().(clientReadyMsg); !ok {
		t.Errorf("produced %T, want clientReadyMsg", cmd())
	}
}

func TestAuthDone_FailureIsSurfaced(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, authDoneMsg{err: errString("login cancelled")})
	if m.err == nil || !strings.Contains(m.err.Error(), "login cancelled") {
		t.Errorf("err = %v, want the login failure", m.err)
	}
}

func TestErrorText_FirstLineOnly(t *testing.T) {
	m := newTestModel()
	m.err = errString("agent closed the connection\nstderr line 1\nstderr line 2")
	if got := m.errorText(); got != "agent closed the connection" {
		t.Errorf("errorText() = %q, want just the cause", got)
	}
}

func TestFooterHeight_GrowsWhenStopped(t *testing.T) {
	m := newTestModel()
	base := m.footerHeight()
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})
	if got := m.footerHeight(); got <= base {
		t.Errorf("footerHeight when stopped = %d, want more than %d", got, base)
	}
}

func TestWindowResize_MakesModelReady(t *testing.T) {
	m := newUnsizedTestModel()
	m, _ = applyUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.ready {
		t.Error("expected the model to be ready after a resize")
	}
	if m.viewport.Height < 1 {
		t.Errorf("viewport height = %d, want at least 1", m.viewport.Height)
	}
}
