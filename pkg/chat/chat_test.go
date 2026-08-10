package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// permissionTitled is permission() with a distinguishable call, for the tests
// that need two escalations in flight at once.
func permissionTitled(title string, options ...acp.PermissionOption) permissionMsg {
	p := permission(options...)
	p.req.ToolCall.Title = title
	return p
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
	if last.kind != entryNotice || !strings.Contains(last.text, "10 in / 20 out") {
		t.Errorf("last entry = %+v, want a notice summarising the turn", last)
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
	if m.perm() == nil {
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

			if m.perm() != nil {
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
	if m.perm() != nil {
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

	if m.perm() == nil {
		t.Error("permission should still be pending")
	}
	if len(perm.reply) != 0 {
		t.Error("no answer should have been sent")
	}
}

// An agent can have two tools waiting at once — a subagent asks independently
// of the turn that spawned it. Each request is blocked on its own reply channel,
// so a second escalation must not displace the first: whichever one is dropped
// is never answered, its prompt never returns, and the chat hangs with no way
// out but killing the window.
func TestPermission_SecondQueuesBehindTheFirst(t *testing.T) {
	m := newTestModel()
	first := permissionTitled("rm -rf dist", allowOnce, rejectOnce)
	second := permissionTitled("git push --force", allowOnce, rejectOnce)
	m, _ = applyUpdate(m, first)
	m, _ = applyUpdate(m, second)

	if got := m.perm().req.ToolCall.Title; got != "rm -rf dist" {
		t.Errorf("on screen = %q, want the first escalation to keep the dialog", got)
	}
	if !strings.Contains(m.permissionView(), "1 more waiting") {
		t.Errorf("the dialog must say one is queued behind it\ngot: %s", m.permissionView())
	}

	m, _ = applyUpdate(m, keyMsg("d"))
	select {
	case got := <-first.reply:
		if got != "reject" {
			t.Errorf("first reply = %q, want reject", got)
		}
	default:
		t.Fatal("the first request was never answered")
	}

	if got := m.perm().req.ToolCall.Title; got != "git push --force" {
		t.Errorf("on screen = %q, want the queued escalation promoted", got)
	}

	m, _ = applyUpdate(m, keyMsg("a"))
	select {
	case got := <-second.reply:
		if got != "once" {
			t.Errorf("second reply = %q, want once", got)
		}
	default:
		t.Fatal("the queued request was never answered")
	}
	if m.perm() != nil {
		t.Error("the queue should be empty once both are answered")
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
	if strings.Contains(view, "waiting") {
		t.Errorf("a lone escalation must not claim others are queued\ngot: %s", view)
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
		options: []acp.ConfigOption{{ID: "model", Category: "model", CurrentValue: "some/model"}},
		note:    "could not resume ses_old",
	})
	if m.sessionID != "ses_new" {
		t.Errorf("sessionID = %q, want ses_new", m.sessionID)
	}
	if got := m.settingsSummary(); len(got) != 1 || got[0] != "some/model" {
		t.Errorf("settingsSummary() = %v, want [some/model]", got)
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
	m.configOptions = []acp.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", CurrentValue: "github-copilot/claude-sonnet-4.6"},
	}
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

// The conversation starts at the top of the screen and grows downward, the way
// anything printed to a terminal does — not pinned to the bottom above the
// input with a void over it.
func TestConversation_StartsAtTheTop(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "first thing said"))

	first := strings.SplitN(m.viewport.View(), "\n", 2)[0]
	if strings.TrimSpace(first) == "" {
		t.Errorf("conversation does not start on the viewport's first line:\n%s", m.viewport.View())
	}
}

// Once the conversation outgrows the viewport the oldest lines scroll off the
// top and the newest stay in view, without the reader having to chase them.
func TestConversation_NewestStaysInViewOnceItOverflows(t *testing.T) {
	m := newTestModel()
	// User messages, because consecutive chunks of the same kind merge into one
	// entry and would give us a single wrapped blob instead of 60 lines.
	for i := range 60 {
		m, _ = applyUpdate(m, textUpdate(acp.UpdateUserMessage, fmt.Sprintf("line %d", i)))
	}
	view := m.viewport.View()
	if !strings.Contains(view, "line 59") {
		t.Errorf("newest line scrolled out of view:\n%s", view)
	}
	if strings.Contains(view, "line 0 ") {
		t.Errorf("oldest line should have scrolled off the top:\n%s", view)
	}
}

// Reading back through history must survive an arriving chunk: an agent that
// yanks you to the bottom mid-sentence is unusable during a long turn.
func TestConversation_ScrollbackIsNotYankedAway(t *testing.T) {
	m := newTestModel()
	// User messages, because consecutive chunks of the same kind merge into one
	// entry and would give us a single wrapped blob instead of 60 lines.
	for i := range 60 {
		m, _ = applyUpdate(m, textUpdate(acp.UpdateUserMessage, fmt.Sprintf("line %d", i)))
	}
	m.viewport.GotoTop()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "and one more"))

	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want the reader left where they were", m.viewport.YOffset)
	}
}

func TestTurnSummary(t *testing.T) {
	if got := turnSummary(nil, time.Second); got != "" {
		t.Errorf("turnSummary(nil) = %q, want empty", got)
	}
	got := turnSummary(&acp.PromptResponse{
		StopReason: acp.StopCancelled,
		Usage:      &acp.TokenUsage{InputTokens: 3, OutputTokens: 4},
	}, 2500*time.Millisecond)
	if !strings.Contains(got, acp.StopCancelled) || !strings.Contains(got, "3 in / 4 out") {
		t.Errorf("turnSummary() = %q", got)
	}
	if !strings.Contains(got, "2.5s") {
		t.Errorf("turnSummary() = %q, want the turn's duration", got)
	}
}

// A turn that ends the ordinary way says how long it took, not "end_turn":
// the stop reason is protocol vocabulary and is only news when it is unusual.
func TestTurnSummary_HidesTheOrdinaryStopReason(t *testing.T) {
	got := turnSummary(&acp.PromptResponse{StopReason: acp.StopEndTurn}, 1200*time.Millisecond)
	if strings.Contains(got, acp.StopEndTurn) {
		t.Errorf("turnSummary() = %q, want no stop reason", got)
	}
	if !strings.Contains(got, "1.2s") {
		t.Errorf("turnSummary() = %q, want the turn's duration", got)
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

// outputCall is a call that produced text, which is how agents return a
// command's stdout, a file preview, or an explanation of a failure.
func outputCall(status, text string) acp.ToolCall {
	return acp.ToolCall{
		ToolCallID: "t1", Title: "go test ./...", Kind: "execute", Status: status,
		Content: []acp.ToolCallContent{
			{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: text}},
		},
	}
}

func TestRenderTool_ShowsFailureReason(t *testing.T) {
	m := newTestModel()
	call := outputCall(acp.StatusFailed, "The user rejected permission to use this specific tool call.")
	out := m.renderTool(call, 80)
	if !strings.Contains(out, "rejected permission") {
		t.Errorf("renderTool() should explain a failure\ngot:\n%s", out)
	}
}

// A tool that succeeded still produced something, and until this the log threw
// it away — the row said a command ran and nothing said what it printed.
func TestRenderTool_ShowsOutputOnSuccess(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(outputCall(acp.StatusCompleted, "ok  \tgithub.com/axelgar/opentree/pkg/acp\t0.4s"), 80)
	if !strings.Contains(out, "github.com/axelgar/opentree/pkg/acp") {
		t.Errorf("renderTool() should show what the tool produced\ngot:\n%s", out)
	}
}

// The whole of a failure is shown too, not just its first line: a stack trace
// truncated to "panic: runtime error:" names the category and hides the cause.
func TestRenderTool_ShowsEveryLineOfAFailure(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(outputCall(acp.StatusFailed, "panic: runtime error\n\tat main.go:12\nexit status 2"), 80)
	for _, want := range []string{"panic: runtime error", "at main.go:12", "exit status 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTool() missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderOutput_CapsLongOutput(t *testing.T) {
	body := strings.TrimSuffix(strings.Repeat("line\n", 30), "\n")
	lines := renderOutput(outputCall(acp.StatusCompleted, body), 80, toolOutputStyle)

	if len(lines) != outputMaxLines+1 {
		t.Fatalf("rendered %d lines, want %d plus a count of what was held back", len(lines), outputMaxLines)
	}
	if want := "22 more lines"; !strings.Contains(lines[len(lines)-1], want) {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], want)
	}
}

// The Claude Code adapter fences command output in markdown whenever the client
// has not asked for streamed terminals, which opentree never does. Rendered
// literally, the first line of a failed command reads "```console".
func TestRenderTool_StripsTheAdaptersCodeFence(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(outputCall(acp.StatusFailed, "```console\nno such file or directory\n```"), 80)
	if strings.Contains(out, "```") {
		t.Errorf("renderTool() should not render the fence\ngot:\n%s", out)
	}
	if !strings.Contains(out, "no such file or directory") {
		t.Errorf("renderTool() lost the output inside the fence\ngot:\n%s", out)
	}
}

// A call that drew a diff has already shown what it did; opencode's content
// block beside it is a receipt reading "Edit applied successfully.".
func TestRenderTool_DiffCallDoesNotAlsoPrintItsReceipt(t *testing.T) {
	m := newTestModel()
	call := diffCall(acp.StatusCompleted, "old line", "new line")
	call.Content = append(call.Content, acp.ToolCallContent{
		Type: "content", Content: &acp.ContentBlock{Type: "text", Text: "Edit applied successfully."},
	})

	out := m.renderTool(call, 80)
	if strings.Contains(out, "Edit applied successfully") {
		t.Errorf("the diff is the output; the receipt says it twice\ngot:\n%s", out)
	}
	if !strings.Contains(out, "+ new line") {
		t.Errorf("renderTool() lost the diff\ngot:\n%s", out)
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
		{"socket command", socketCommandMsg{cmd: Command{Type: CommandInterrupt}}},
		{"socket command with nothing to do", socketCommandMsg{cmd: Command{Type: CommandPermission}}},
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

// A dead agent cannot act on an answer, so leaving its dialog up offers to
// allow a tool call that will never run — and leaves its request blocked.
func TestAgentGone_ReleasesPendingPermissions(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	m, _ = applyUpdate(m, perm)
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

	if m.perm() != nil {
		t.Error("a dead agent's escalation must not stay on screen")
	}
	select {
	case got := <-perm.reply:
		if got != "" {
			t.Errorf("reply = %q, want an empty id to decline", got)
		}
	default:
		t.Fatal("the blocked request was never released")
	}
}

// The panel the footer draws and the panel the keyboard drives were decided
// separately, in different orders. With the settings picker open when an
// escalation arrived the picker stayed on screen while the keys answered the
// permission — so a digit, which is how you pick a setting, silently allowed a
// tool call nobody was shown.
func TestOverlay_ScreenAndKeyboardAgree(t *testing.T) {
	t.Run("a permission outranks the settings picker", func(t *testing.T) {
		m := newTestModel()
		m.settings.open = true
		perm := permission(allowOnce, rejectOnce)
		m, _ = applyUpdate(m, perm)

		if !strings.Contains(m.footer(), "Allow once") {
			t.Errorf("the footer must show the dialog its keys answer\ngot:\n%s", m.footer())
		}
		m, _ = applyUpdate(m, keyMsg("1"))
		select {
		case got := <-perm.reply:
			if got != "once" {
				t.Errorf("reply = %q, want once", got)
			}
		default:
			t.Fatal("the digit did not reach the dialog on screen")
		}
	})

	t.Run("a stopped agent outranks the settings picker", func(t *testing.T) {
		m := newTestModel()
		m.settings.open = true
		m.launch = func() (*acp.Client, *acp.InitializeResponse, int, error) {
			return nil, nil, 2, nil
		}
		m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

		if !strings.Contains(m.footer(), "restart agent") {
			t.Errorf("the footer must show the panel its keys drive\ngot:\n%s", m.footer())
		}
		if _, cmd := applyUpdate(m, keyMsg("r")); cmd == nil {
			t.Error("r should restart, since the stopped panel is what is drawn")
		}
	})
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

	m, cmd := applyUpdate(m, clientReadyMsg{client: &acp.Client{}, generation: 7})
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
	if !hasMsgType(batchMsgs(cmd), "chat.clientReadyMsg") {
		t.Error("no clientReadyMsg: the agent was not restarted")
	}
}

// batchMsgs flattens every message a cmd produces, descending into tea.Batch.
// Each command runs under a deadline because a batch routinely carries a
// tea.Tick alongside the message under test, and blocking on a three-second
// timer nobody asked about would make this the slowest test in the package.
func batchMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			return []tea.Msg{msg}
		}
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, batchMsgs(c)...)
		}
		return out
	case <-time.After(100 * time.Millisecond):
		return nil // a timer, not a message
	}
}

// hasMsgType matches on the type's name because the one that matters here,
// bubbletea's enableMouseCellMotionMsg, is unexported and cannot be named.
func hasMsgType(msgs []tea.Msg, want string) bool {
	for _, msg := range msgs {
		if fmt.Sprintf("%T", msg) == want {
			return true
		}
	}
	return false
}

const mouseCaptureMsg = "tea.enableMouseCellMotionMsg"

// The login runs through tea.ExecProcess, whose terminal restore drops mouse
// mode — and mouse mode is what stops the wheel escaping the alt screen into
// the shell's scrollback.
func TestAuthDone_RestoresMouseCapture(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  authDoneMsg
	}{
		{"success", authDoneMsg{}},
		{"failure", authDoneMsg{err: errString("login cancelled")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.launch = func() (*acp.Client, *acp.InitializeResponse, int, error) {
				return nil, nil, 3, nil
			}
			_, cmd := applyUpdate(m, tt.msg)
			if !hasMsgType(batchMsgs(cmd), mouseCaptureMsg) {
				t.Error("mouse capture was not restored after the login exec")
			}
		})
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

// ---------------------------------------------------------------------------
// Text editing
// ---------------------------------------------------------------------------

// TestEditingKeys_ReachTheInput pins the keys the textarea needs for word and
// line deletion. Every one of these was, or could be, shadowed by a Model-level
// binding — and an input box that cannot erase a word is worse than one missing
// a scroll shortcut.
func TestEditingKeys_ReachTheInput(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyMsg
		start string
		want  string
	}{
		{
			name:  "alt+backspace deletes the previous word",
			key:   tea.KeyMsg{Type: tea.KeyBackspace, Alt: true},
			start: "fix the login bug",
			want:  "fix the login ",
		},
		{
			name:  "ctrl+w deletes the previous word",
			key:   tea.KeyMsg{Type: tea.KeyCtrlW},
			start: "fix the login bug",
			want:  "fix the login ",
		},
		{
			name:  "ctrl+u deletes to the start of the line",
			key:   tea.KeyMsg{Type: tea.KeyCtrlU},
			start: "a whole sentence to erase",
			want:  "",
		},
		{
			name:  "ctrl+k deletes to the end of the line",
			key:   tea.KeyMsg{Type: tea.KeyCtrlK},
			start: "keep this",
			want:  "keep this", // cursor is already at the end
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.input.SetValue(tt.start)
			m.input.CursorEnd()

			m, _ = applyUpdate(m, tt.key)
			if got := m.input.Value(); got != tt.want {
				t.Errorf("input = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScrollKeys_DoNotShadowEditing(t *testing.T) {
	// Scrolling moved to shift+arrows precisely so these stay with the input.
	for _, b := range [][]string{m2s(keys.ScrollUp), m2s(keys.ScrollDn), m2s(keys.Thoughts)} {
		for _, k := range b {
			switch k {
			case "ctrl+u", "ctrl+d", "ctrl+w", "ctrl+k", "ctrl+a", "ctrl+e", "ctrl+t", "alt+backspace":
				t.Errorf("%q is a text-editing key and must not be bound to navigation", k)
			}
		}
	}
}

func m2s(b key.Binding) []string { return b.Keys() }

func TestThoughtsToggle_OnItsNewKey(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentThought, "reasoning aloud"))
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if strings.Contains(m.renderLog(), "reasoning aloud") {
		t.Error("ctrl+o should hide reasoning")
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

// ---------------------------------------------------------------------------
// Orientation
// ---------------------------------------------------------------------------

func TestEmptyChat_ShowsWhatToDo(t *testing.T) {
	m := newTestModel()
	view := m.View()

	for _, want := range []string{"OpenCode", "fix-auth", "/", "@", "?"} {
		if !strings.Contains(view, want) {
			t.Errorf("empty chat is missing %q:\n%s", want, view)
		}
	}
}

// The opening screen is the one place with room to draw the agent, and a
// worktree you attached to without opening should say what it is running.
func TestEmptyChat_DrawsTheAgent(t *testing.T) {
	m := newTestModel()
	m.opts.Agent = "claude" // as a workspace records it, not as it is shown
	m = m.relayout()

	view := m.View()
	for _, want := range []string{"Claude Code", "▝▜█████▛▘"} {
		if !strings.Contains(view, want) {
			t.Errorf("opening screen is missing %q:\n%s", want, view)
		}
	}
}

// An agent outside the registry still gets the layout, just without a drawing
// or a colour to call its own.
func TestEmptyChat_UnknownAgentStillNamed(t *testing.T) {
	m := newTestModel()
	m.opts.Agent = "aider"
	m = m.relayout()

	if !strings.Contains(m.View(), "aider") {
		t.Errorf("opening screen dropped an unregistered agent:\n%s", m.View())
	}
}

// ACP reports the version of whatever serves the protocol, which for an agent
// behind an adapter is the adapter's — a number that does not belong to the
// agent it is named after.
func TestEmptyChat_DoesNotClaimTheAgentsVersion(t *testing.T) {
	m := newTestModel()
	m.agentVersion = "0.66.0"
	m = m.relayout()

	if strings.Contains(m.View(), "0.66.0") {
		t.Errorf("empty chat put the ACP server's version on the agent:\n%s", m.View())
	}
}

// The status line drops whole bindings when the flags crowd it, because a cut
// through the rendered line leaves a separator dangling off the end.
func TestStatusLine_NarrowTerminalDropsWholeBindings(t *testing.T) {
	m := newTestModel()
	m.width = 62
	m.configOptions = []acp.ConfigOption{
		{ID: "mode", Name: "Mode", Category: "mode", CurrentValue: "plan"},
	}
	m = m.relayout()

	line := m.statusLine()
	if strings.Contains(line, "•  ") || strings.HasSuffix(strings.TrimSpace(line), "•") {
		t.Errorf("status line has a dangling separator: %q", line)
	}
	if !strings.Contains(line, "enter") {
		t.Errorf("status line dropped everything: %q", line)
	}
	if !strings.Contains(line, "plan") {
		t.Errorf("status line dropped the flags it was making room for: %q", line)
	}
}

func TestEmptyChat_HintGoesAwayOnceTheChatStarts(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate("agent_message_chunk", "hello"))

	if strings.Contains(m.View(), "point it at a file") {
		t.Errorf("the opening hint outlived the first message:\n%s", m.View())
	}
}

func TestHelp_QuestionMarkOpensTheKeyList(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, keyMsg("?"))

	if !m.showHelp {
		t.Fatal("? on an empty message did not open the key list")
	}
	view := m.View()
	for _, want := range []string{"ctrl+o", "shift+tab", "pgup", "attach a file"} {
		if !strings.Contains(view, want) {
			t.Errorf("key list is missing %q:\n%s", want, view)
		}
	}
}

// A question mark is a character before it is a shortcut.
func TestHelp_QuestionMarkTypesWhenTheMessageIsNotEmpty(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("why")
	m, _ = applyUpdate(m, keyMsg("?"))

	if m.showHelp {
		t.Error("? opened the key list instead of reaching the message")
	}
	if got := m.input.Value(); got != "why?" {
		t.Errorf("input = %q, want %q", got, "why?")
	}
}

func TestHelp_AnyKeyCloses(t *testing.T) {
	m := newTestModel()
	m.showHelp = true
	m, _ = applyUpdate(m, keyMsg("x"))

	if m.showHelp {
		t.Error("the key list stayed open")
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("the dismissing key leaked into the input: %q", got)
	}
}

func TestHelp_CtrlCStillQuits(t *testing.T) {
	m := newTestModel()
	m.showHelp = true
	_, cmd := applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlC})

	if cmd == nil {
		t.Fatal("ctrl+c from the key list did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}

// ---------------------------------------------------------------------------
// Stopped panel
// ---------------------------------------------------------------------------

func TestStopped_LogInOnlyWhenCredentialsAreTheProblem(t *testing.T) {
	tests := []struct {
		name      string
		dead      bool
		authNeed  bool
		auth      []string
		wantLogin bool
	}{
		{name: "agent wants credentials", authNeed: true, auth: []string{"auth", "login"}, wantLogin: true},
		{name: "agent crashed", dead: true, auth: []string{"auth", "login"}},
		{name: "agent has no login command", authNeed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.dead, m.authNeed = tt.dead, tt.authNeed
			m.opts.AuthCommand = tt.auth
			m.err = errString("agent exited")
			m = m.relayout()

			got := strings.Contains(m.View(), "log in")
			if got != tt.wantLogin {
				t.Errorf("stopped panel offers log in = %v, want %v:\n%s", got, tt.wantLogin, m.View())
			}
			if !strings.Contains(m.View(), "restart") {
				t.Error("stopped panel does not offer a restart")
			}
		})
	}
}

// ? is the binding that leads to the others, so it should outlast esc and
// ctrl+c when the line has to shed something.
func TestStatusLine_KeepsTheKeyListPointerLongest(t *testing.T) {
	m := newTestModel()
	m.width = 62
	m.configOptions = []acp.ConfigOption{
		{ID: "mode", Name: "Mode", Category: "mode", CurrentValue: "plan"},
	}
	m = m.relayout()

	line := m.statusLine()
	if strings.Contains(line, "ctrl+c") {
		t.Fatalf("nothing was dropped, so this proves nothing: %q", line)
	}
	if !strings.Contains(line, "? keys") {
		t.Errorf("? was dropped before less useful bindings: %q", line)
	}
}

// A failed resume lands before the first word, which is exactly when someone
// still wants to be told what the chat can do.
func TestEmptyChat_HintSurvivesANotice(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, sessionReadyMsg{id: "ses_new", note: "could not resume"})

	if !strings.Contains(m.View(), "point it at a file") {
		t.Errorf("a notice suppressed the opening hint:\n%s", m.View())
	}
}

// ---------------------------------------------------------------------------
// Mouse
// ---------------------------------------------------------------------------

func wheel(button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: button}
}

// The chat captures the mouse so the terminal stops scrolling its own buffer
// out from under the alt screen. Having taken it, the wheel has to move the
// conversation, or scrolling looks broken instead of contained.
func TestWheel_ScrollsTheConversation(t *testing.T) {
	m := newTestModel()
	for i := 0; i < 200; i++ {
		m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, fmt.Sprintf("line %d", i)))
		m, _ = applyUpdate(m, textUpdate(acp.UpdateUserMessage, "next"))
	}
	if m.viewport.YOffset == 0 {
		t.Fatal("the log is too short to scroll, so this proves nothing")
	}

	bottom := m.viewport.YOffset
	m, _ = applyUpdate(m, wheel(tea.MouseButtonWheelUp))
	if m.viewport.YOffset >= bottom {
		t.Errorf("YOffset = %d after wheel up, want less than %d", m.viewport.YOffset, bottom)
	}

	up := m.viewport.YOffset
	m, _ = applyUpdate(m, wheel(tea.MouseButtonWheelDown))
	if m.viewport.YOffset <= up {
		t.Errorf("YOffset = %d after wheel down, want more than %d", m.viewport.YOffset, up)
	}
}

// A click is not a scroll, and the chat has nothing to do with it.
func TestWheel_ClicksAreIgnored(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("half a sentence")
	m, _ = applyUpdate(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if got := m.input.Value(); got != "half a sentence" {
		t.Errorf("input = %q, want a click to leave it alone", got)
	}
}

// The band behind your own message has to run the whole column. Stopping at
// the end of the text turns a block into a highlight, and a one-word question
// stops looking like a question at all.
func TestUserMessage_BandRunsTheFullColumn(t *testing.T) {
	m := newTestModel()
	for _, width := range []int{40, 60, 98} {
		got := lipgloss.Width(m.renderEntry(entry{kind: entryUser, text: "hi"}, width))
		if got != width {
			t.Errorf("user message at width %d rendered %d cells wide", width, got)
		}
	}
}
