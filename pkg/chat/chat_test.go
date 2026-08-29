package chat

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/config"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testAgent is a copy of a registry entry, safe for a test to modify without
// corrupting the shared registry for the tests that run after it.
func testAgent(name string) *config.PredefinedAgent {
	a := *config.FindAgent(name)
	return &a
}

// bareAgent is the default agent for tests: opencode's identity with no login
// command, so a test only sees a remedy it asked for.
func bareAgent() *config.PredefinedAgent {
	a := testAgent("opencode")
	a.ACP.AuthCommand = nil
	return a
}

// newTestModel builds a Model with no agent behind it. Tests that only exercise
// in-process logic should use this instead of Run, which spawns a subprocess.
func newTestModel() Model {
	m := Model{
		opts:      Options{Workspace: "fix-auth", Agent: bareAgent(), Cwd: "/repo"},
		toolIdx:   make(map[string]int),
		input:     newComposer(),
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
	return Model{
		opts:    Options{Workspace: "fix-auth", Agent: bareAgent()},
		toolIdx: make(map[string]int),
		input:   newComposer(),
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

func userChunk(c acp.ContentBlock) acpUpdateMsg {
	return acpUpdateMsg(acp.SessionUpdate{
		Type:    acp.UpdateUserMessage,
		Message: &acp.MessageChunk{MessageID: "m1", Content: c},
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
	m, _ = applyUpdate(m, promptDoneMsg{err: stringError("boom")})
	if m.err == nil {
		t.Fatal("expected the error to be surfaced")
	}
	if !strings.Contains(m.statusLine(), "boom") {
		t.Errorf("statusLine = %q, want it to show the error", m.statusLine())
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

// The four chunks below are a real session/load, captured from opencode 1.18.16
// replaying one prompt that mentioned a file: what was typed, the input the
// agent handed its Read tool, the whole file it inlined, and the mention as a
// link. All four share one messageId, and the middle two are addressed to the
// assistant. Replaying them verbatim handed back a conversation nobody had —
// on every attach, since a chat loads its session each time.
func TestReplayedUserMessage_KeepsOnlyWhatWasTyped(t *testing.T) {
	m := newTestModel()
	forAgent := &acp.Annotations{Audience: []string{"assistant"}}
	for _, c := range []acp.ContentBlock{
		{Type: "text", Text: "summarise "},
		{Type: "text", Text: `Called the Read tool with the following input: {"filePath":"/repo/NOTES.md"}`, Annotations: forAgent},
		{Type: "text", Text: "<path>/repo/NOTES.md</path>\n<content>\nthe whole file, quoted\n</content>", Annotations: forAgent},
		{Type: "resource_link", URI: "file:///repo/NOTES.md", Name: "NOTES.md"},
	} {
		m, _ = applyUpdate(m, userChunk(c))
	}

	var said []entry
	for _, e := range m.entries {
		if e.kind == entryUser {
			said = append(said, e)
		}
	}
	if len(said) != 1 {
		t.Fatalf("got %d user messages, want the one that was sent: %+v", len(said), said)
	}
	if said[0].text != "summarise @NOTES.md" {
		t.Errorf("replayed = %q, want the typed message with its mention intact", said[0].text)
	}
	if strings.Contains(m.viewport.View(), "the whole file, quoted") {
		t.Error("a file the agent inlined for itself must not come back as something you said")
	}
}

// The palette shows six, and opencode advertises ninety-odd commands. Six with
// nothing under them read as "that is all there is", which sent people looking
// elsewhere for a list that was already on screen — so the rest scroll past
// under the cursor instead of needing a narrowing prefix typed at them.
func TestCompletionView_ScrollsPastTheWindow(t *testing.T) {
	m := newTestModel()
	for i := range 12 {
		m.commands = append(m.commands, acp.Command{Name: fmt.Sprintf("cmd%02d", i)})
	}
	m.input.SetValue("/cmd")
	m = m.refreshCompletion()

	if got := len(m.completion.items); got != 12 {
		t.Errorf("items = %d, want all 12 kept for scrolling", got)
	}
	if view := m.completionView(); !strings.Contains(view, "1 of 12") {
		t.Errorf("completionView() = %q, want it to say where the cursor is", view)
	}
	if got, want := m.completionHeight(), completionWindow+1; got != want {
		t.Errorf("completionHeight = %d, want %d — the footer has to fit the line", got, want)
	}

	// Arrowing past the sixth scrolls the window instead of stopping.
	for range completionWindow {
		m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	view := m.completionView()
	if !strings.Contains(view, "› /cmd06") {
		t.Errorf("completionView() = %q, want the seventh command selected", view)
	}
	if strings.Contains(view, "/cmd00") {
		t.Errorf("completionView() = %q, want the first command scrolled off", view)
	}
}

// ACP says a client must not call session/load unless the agent advertised
// loadSession. Both agents opentree ships with do, so this is for the next one:
// asking anyway costs a round trip and comes back as a failure that reads like
// a lost conversation rather than an agent that never kept one.
func TestResume_NotAttemptedWithoutTheCapability(t *testing.T) {
	m := newTestModel()
	m.opts.SessionID = "ses_old"
	m.canResume = false
	if cmd := m.startSession(); cmd != nil {
		t.Error("with no client there is nothing to ask, so nothing should be issued")
	}

	m = m.withAgentInfo(&acp.InitializeResponse{
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true},
	})
	if !m.canResume {
		t.Error("the capability the agent advertised was dropped")
	}
}

// A session that was never created leaves nothing to send to, and the panel
// carrying the restart key only appears for an agent that is stopped — so the
// chat used to sit on "starting…" with no way forward but closing the window.
func TestSessionFailure_OffersARestart(t *testing.T) {
	m := newTestModel()
	m.sessionID = ""
	m, _ = applyUpdate(m, errMsg{err: stringError("session/new: refused"), fatal: true})

	if !m.stopped() {
		t.Error("a chat that never got a session has to offer a way out")
	}
	if !strings.Contains(m.footer(), "restart agent") {
		t.Errorf("footer = %q, want the restart key", m.footer())
	}
}

func planUpdate(entries ...acp.PlanEntry) acpUpdateMsg {
	return acpUpdateMsg(acp.SessionUpdate{Type: acp.UpdatePlan, Plan: entries})
}

// The whole plan arrives on every change — a live claude-agent-acp session sent
// six updates building a three-item list — so appending each would bury the
// conversation under its own table of contents.
func TestPlan_IsOneEntryPatchedInPlace(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, planUpdate(
		acp.PlanEntry{Content: "Extract the print into greet.go", Status: acp.PlanPending}))
	m, _ = applyUpdate(m, planUpdate(
		acp.PlanEntry{Content: "Extract the print into greet.go", Status: acp.PlanCompleted},
		acp.PlanEntry{Content: "Reduce main.go to the entrypoint", Status: acp.PlanInProgress}))

	var plans int
	for _, e := range m.entries {
		if e.kind == entryPlan {
			plans++
		}
	}
	if plans != 1 {
		t.Fatalf("got %d plan entries, want one kept up to date", plans)
	}

	view := m.viewport.View()
	for _, want := range []string{"☑ Extract the print", "▸ Reduce main.go"} {
		if !strings.Contains(view, want) {
			t.Errorf("plan missing %q\ngot:\n%s", want, view)
		}
	}
}

// The agent changes its own mode — answering its plan-mode dialog does it —
// and nothing comes back through the call that would normally report it. Left
// unread, the flag beside the input still said "plan" after the agent had
// switched to accepting edits without asking.
func TestCurrentModeUpdate_MovesTheFlag(t *testing.T) {
	m := newTestModel()
	m.configOptions = []acp.ConfigOption{{
		ID: "mode", Name: "Mode", Category: "mode", Type: "select", CurrentValue: "plan",
		Options: []acp.ConfigOptionValue{{Value: "plan", Name: "Plan"}, {Value: "auto", Name: "Auto"}},
	}}

	m, _ = applyUpdate(m, acpUpdateMsg(acp.SessionUpdate{
		Type: acp.UpdateMode, CurrentModeID: "auto",
	}))

	if got := m.configOptions[0].CurrentValue; got != "auto" {
		t.Errorf("mode = %q, want the agent's own change to have landed", got)
	}
	if !strings.Contains(strings.Join(m.flagsSummary(), " "), "auto") {
		t.Errorf("flags = %v, want them to follow the agent", m.flagsSummary())
	}
}

func TestConfigOptionUpdate_ReplacesTheSet(t *testing.T) {
	m := newTestModel()
	m.configOptions = []acp.ConfigOption{{ID: "mode", Category: "mode", CurrentValue: "plan"}}
	m, _ = applyUpdate(m, acpUpdateMsg(acp.SessionUpdate{
		Type: acp.UpdateConfigOptions,
		ConfigOptions: []acp.ConfigOption{
			{ID: "mode", Category: "mode", CurrentValue: "auto"},
			{ID: "effort", Category: "effort", CurrentValue: "high"},
		},
	}))
	if len(m.configOptions) != 2 || m.configOptions[0].CurrentValue != "auto" {
		t.Errorf("configOptions = %+v, want the agent's set", m.configOptions)
	}
}

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

// The Claude Code adapter's plan dialog offers two ways to allow always — "yes"
// and "yes, and auto-accept edits". Labelling both [A] pointed both rows at the
// first one optionForKey finds, so the key meant something other than the row
// it sat on, and silently picked the more permissive of the two.
func TestOptionHint_LetterBelongsToOneRow(t *testing.T) {
	auto := acp.PermissionOption{OptionID: "auto", Kind: acp.PermissionAllowAlways, Name: "Allow always, auto-accept"}
	options := []acp.PermissionOption{allowOnce, allowAlways, auto, rejectOnce}

	var hints []string
	for i := range options {
		hints = append(hints, optionHint(options, i))
	}
	seen := map[string]bool{}
	for i, h := range hints {
		if seen[h] {
			t.Errorf("hint %q labels more than one row: %v", h, hints)
		}
		seen[h] = true

		// Whatever key a row shows has to select that row.
		if id, ok := optionForKey(h, options); !ok || id != options[i].OptionID {
			t.Errorf("[%s] selects %q, want %q", h, id, options[i].OptionID)
		}
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

// The request carries the same blocks a finished call does. Showing only the
// title meant approving something you were never shown — the Claude Code
// adapter asks "Ready to code?" with the whole plan in a content block, and an
// edit's diff arrives the same way.
func TestPermissionView_ShowsWhatIsBeingApproved(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	perm.req.ToolCall.Content = []acp.ToolCallContent{
		{Type: "diff", Path: "/repo/pkg/auth/session.go", OldText: "timeout := 30", NewText: "timeout := 3600"},
		{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: "raises the session lifetime"}},
	}
	m, _ = applyUpdate(m, perm)

	view := m.permissionView()
	for _, want := range []string{"- timeout := 30", "+ timeout := 3600", "raises the session lifetime"} {
		if !strings.Contains(view, want) {
			t.Errorf("permissionView() missing %q\ngot:\n%s", want, view)
		}
	}
	if m.footerHeight() <= len(perm.req.Options)+5 {
		t.Error("the footer has to make room for the detail it draws")
	}
}

// A dialog sits over the conversation, so it cannot grow without bound: a
// hundred-line diff would push the transcript off the screen to ask one
// yes/no question.
func TestPermissionView_DetailIsCapped(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce)
	perm.req.ToolCall.Content = []acp.ToolCallContent{{
		Type: "diff", Path: "/repo/big.go",
		OldText: strings.TrimSuffix(strings.Repeat("was\n", 60), "\n"),
		NewText: strings.TrimSuffix(strings.Repeat("now\n", 60), "\n"),
	}}
	m, _ = applyUpdate(m, perm)

	if got := len(m.permDetail()); got > 9 {
		t.Errorf("permDetail() = %d lines, want it capped with a marker", got)
	}
	if h, tall := m.footerHeight(), m.height/2; h > tall {
		t.Errorf("footerHeight = %d, want the dialog to leave the conversation room (< %d)", h, tall)
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
	if !strings.Contains(got, "interrupted") || !strings.Contains(got, "3 in / 4 out") {
		t.Errorf("turnSummary() = %q", got)
	}
	if !strings.Contains(got, "2.5s") {
		t.Errorf("turnSummary() = %q, want the turn's duration", got)
	}
}

// Every stop reason but the ordinary one is the agent giving up early, and the
// protocol's own words for that do not say so — "max_turn_requests" also covers
// running out of budget.
func TestTurnSummary_ExplainsWhyItStoppedShort(t *testing.T) {
	for reason, want := range map[string]string{
		acp.StopMaxTokens:       "token limit",
		acp.StopMaxTurnRequests: "budget limit",
		acp.StopRefusal:         "declined",
		"something_new":         "something_new", // an unknown reason still surfaces
	} {
		got := turnSummary(&acp.PromptResponse{StopReason: reason}, time.Second)
		if !strings.Contains(got, want) {
			t.Errorf("turnSummary(%q) = %q, want it to mention %q", reason, got, want)
		}
	}
}

// Cache traffic is most of a real turn and neither of the counts beside it
// includes any of it: a live opencode turn reported 1 in and 19 out against
// 12,056 tokens written to cache, which read as a turn that did nothing.
func TestTurnSummary_CountsCachedTokens(t *testing.T) {
	got := turnSummary(&acp.PromptResponse{
		StopReason: acp.StopEndTurn,
		Usage: &acp.TokenUsage{
			InputTokens: 1, OutputTokens: 19, TotalTokens: 12076, CachedWriteTokens: 12056,
		},
	}, time.Second)
	if !strings.Contains(got, "12056 cached") {
		t.Errorf("turnSummary() = %q, want the cache traffic counted", got)
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

func TestCountChanges(t *testing.T) {
	tests := []struct {
		name             string
		old, updated     string
		wantAdd, wantRem int
	}{
		{"one line appended to a region", "a\nb", "a\nb\nc", 1, 0},
		{"pure insertion", "", "new line", 1, 0},
		{"pure deletion", "gone\naway", "", 0, 2},
		{"trailing newline is not a line", "a\n", "b\n", 1, 1},
		{"context on both sides is not a change", "a\nb\nc", "a\nB\nc", 1, 1},
		{"an unchanged region changed nothing", "a\nb\nc", "a\nb\nc", 0, 0},
		{"two changes twenty apart", "x\n" + strings.Repeat("k\n", 20) + "y",
			"X\n" + strings.Repeat("k\n", 20) + "Y", 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := countChanges(callDiff(diffCall(acp.StatusCompleted, tt.old, tt.updated)))
			if added != tt.wantAdd || removed != tt.wantRem {
				t.Errorf("countChanges() = +%d/-%d, want +%d/-%d", added, removed, tt.wantAdd, tt.wantRem)
			}
		})
	}
}

func TestRenderTool_ShowsDiffLinesAndStat(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(diffCall(acp.StatusCompleted, "old line", "new line"), 60, false)

	for _, want := range []string{"pkg/auth/session.go", "+1", "-1", "- old line", "+ new line"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTool() missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRenderDiffs_TruncatesLargeEdits(t *testing.T) {
	old := strings.TrimSuffix(strings.Repeat("was\n", 50), "\n")
	updated := strings.TrimSuffix(strings.Repeat("now\n", 50), "\n")
	lines := renderDiffs(diffCall(acp.StatusCompleted, old, updated), 80)

	if len(lines) > diffMaxLines+1 {
		t.Errorf("rendered %d lines, want at most %d plus a truncation marker", len(lines), diffMaxLines)
	}
	if !strings.Contains(lines[len(lines)-1], "more lines") {
		t.Errorf("last line = %q, want a count of what was held back", lines[len(lines)-1])
	}
}

// The display budget is twelve lines, and the Claude Code adapter pads every
// hunk with context on both sides — so a one-line edit used to render as seven
// removals and seven additions and never reach the line that changed.
func TestRenderDiffs_SpendsTheBudgetOnChangesNotContext(t *testing.T) {
	ctx := strings.Repeat("unchanged\n", 3)
	call := diffCall(acp.StatusCompleted, ctx+"before\n"+ctx, ctx+"after\n"+ctx)
	lines := renderDiffs(call, 80)

	if len(lines) != 2 {
		t.Fatalf("rendered %d lines, want just the removal and the addition:\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	for _, want := range []string{"- before", "+ after"} {
		if !strings.Contains(strings.Join(lines, "\n"), want) {
			t.Errorf("renderDiffs() missing %q\ngot:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	if strings.Contains(strings.Join(lines, "\n"), "unchanged") {
		t.Error("context the agent did not touch must not be rendered as a change")
	}
}

// A region big enough to defeat the matching table still renders, and still
// says so in the direction that overstates rather than hides.
func TestDiffLines_HugeRegionFallsBackWholesale(t *testing.T) {
	n := 400 // 400*400 = 160k cells, past maxDiffCells
	old := make([]string, n)
	updated := make([]string, n)
	for i := range old {
		old[i] = fmt.Sprintf("old %d", i)
		updated[i] = fmt.Sprintf("new %d", i)
	}
	if got := len(diffLines(old, updated)); got != 2*n {
		t.Errorf("diffLines() = %d changes, want the whole region reported as %d", got, 2*n)
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
	out := m.renderTool(call, 80, false)
	if !strings.Contains(out, "rejected permission") {
		t.Errorf("renderTool() should explain a failure\ngot:\n%s", out)
	}
}

// A tool that succeeded still produced something, and until this the log threw
// it away — the row said a command ran and nothing said what it printed.
func TestRenderTool_ShowsOutputOnSuccess(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(outputCall(acp.StatusCompleted, "ok  \tgithub.com/axelgar/opentree/pkg/acp\t0.4s"), 80, false)
	if !strings.Contains(out, "github.com/axelgar/opentree/pkg/acp") {
		t.Errorf("renderTool() should show what the tool produced\ngot:\n%s", out)
	}
}

// The whole of a failure is shown too, not just its first line: a stack trace
// truncated to "panic: runtime error:" names the category and hides the cause.
func TestRenderTool_ShowsEveryLineOfAFailure(t *testing.T) {
	m := newTestModel()
	out := m.renderTool(outputCall(acp.StatusFailed, "panic: runtime error\n\tat main.go:12\nexit status 2"), 80, false)
	for _, want := range []string{"panic: runtime error", "at main.go:12", "exit status 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTool() missing %q\ngot:\n%s", want, out)
		}
	}
}

// ctrl+x opens the most recent row that is holding lines back, and the same
// key closes it again. "Most recent" rather than a cursor: the row you want
// open is the one that just said "… 22 more lines".
func TestExpand_OpensAndClosesTheMostRecentCappedRow(t *testing.T) {
	m := newTestModel()
	body := strings.TrimSuffix(strings.Repeat("line\n", 30), "\n") + "\nthe last line"
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusCompleted, body)))

	if strings.Contains(m.View(), "the last line") {
		t.Fatal("the cap is not capping; the test is testing nothing")
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if !strings.Contains(m.View(), "the last line") {
		t.Error("ctrl+x did not show the held-back lines")
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if strings.Contains(m.View(), "the last line") {
		t.Error("a second ctrl+x did not fold the row back up")
	}
}

func TestExpand_PrefersTheNewestOfTwoCappedRows(t *testing.T) {
	m := newTestModel()
	old := outputCall(acp.StatusCompleted, strings.Repeat("early\n", 20)+"early tail")
	old.ToolCallID = "t-old"
	late := outputCall(acp.StatusCompleted, strings.Repeat("late\n", 20)+"late tail")
	late.ToolCallID = "t-late"
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, old))
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, late))

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	view := m.View()
	if !strings.Contains(view, "late tail") {
		t.Error("ctrl+x skipped the row nearest the reader")
	}
	if strings.Contains(view, "early tail") {
		t.Error("ctrl+x opened more than the one row")
	}
}

func TestExpand_DoesNothingWhenNothingIsHeldBack(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusCompleted, "short")))
	before := m.View()
	if m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlX}); m.View() != before {
		t.Error("ctrl+x changed a log with nothing to expand")
	}
}

func TestRenderOutput_CapsLongOutput(t *testing.T) {
	body := strings.TrimSuffix(strings.Repeat("line\n", 30), "\n")
	lines := renderOutput(outputCall(acp.StatusCompleted, body), 80, toolOutputStyle, outputMaxLines, true)

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
	out := m.renderTool(outputCall(acp.StatusFailed, "```console\nno such file or directory\n```"), 80, false)
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

	out := m.renderTool(call, 80, false)
	if strings.Contains(out, "Edit applied successfully") {
		t.Errorf("the diff is the output; the receipt says it twice\ngot:\n%s", out)
	}
	if !strings.Contains(out, "+ new line") {
		t.Errorf("renderTool() lost the diff\ngot:\n%s", out)
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

// Bubble Tea runs every command on its own goroutine, and the stopped panel
// looks identical before and after r — which is exactly what makes someone
// press it again. Twice used to mean two agents in one chat, both replaying
// history into one log and both loading a session that allows one client.
func TestStopped_SecondRestartIsIgnoredWhileOneIsUnderWay(t *testing.T) {
	m := newTestModel()
	m.launch = func() (*acp.Client, *acp.InitializeResponse, int, error) {
		return nil, nil, 2, nil
	}
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

	m, first := applyUpdate(m, keyMsg("r"))
	if first == nil {
		t.Fatal("expected r to issue a restart")
	}
	if _, second := applyUpdate(m, keyMsg("r")); second != nil {
		t.Error("a second r must not launch a second agent")
	}
	if !strings.Contains(m.stoppedView(), "restarting") {
		t.Errorf("the panel should say a restart is under way\ngot:\n%s", m.stoppedView())
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
		applyUpdate(m, keyMsg("1"))
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
	m.opts.Agent = testAgent("opencode")
	m.authMethods = []acp.AuthMethod{{ID: "opencode-login", Description: "Run `opencode auth login` in the terminal"}}
	m, _ = applyUpdate(m, errMsg{err: stringError("acp error -32000: Authentication required"), auth: true})

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
		{"failure", authDoneMsg{err: stringError("login cancelled")}},
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
	m, _ = applyUpdate(m, authDoneMsg{err: stringError("login cancelled")})
	if m.err == nil || !strings.Contains(m.err.Error(), "login cancelled") {
		t.Errorf("err = %v, want the login failure", m.err)
	}
}

func TestErrorText_FirstLineOnly(t *testing.T) {
	m := newTestModel()
	m.err = stringError("agent closed the connection\nstderr line 1\nstderr line 2")
	if got := m.errorText(); got != "agent closed the connection" {
		t.Errorf("errorText() = %q, want just the cause", got)
	}
}

// The other half of that decision. The panel keeps one line because a footer
// has one to give; everything after it used to be dropped on the floor, which
// on the launch path is the only account there is of what went wrong — acp
// folds the whole stderr ring into the error precisely so it travels, and
// errorText was the only thing that ever read it.
func TestFailedLaunch_KeepsTheWholeFailureInTheLog(t *testing.T) {
	m := newTestModel()
	m.launching = true
	m, _ = applyUpdate(m, errMsg{fatal: true, err: stringError(
		"ACP handshake failed: EOF\nnode: bad option: --acp\nsee `node --help`")})

	if got := m.errorText(); got != "ACP handshake failed: EOF" {
		t.Errorf("errorText() = %q, want the panel to keep its one line", got)
	}
	log := m.renderLog()
	for _, want := range []string{"node: bad option: --acp", "see `node --help`"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log dropped %q, which is the only account of what happened\ngot:\n%s", want, log)
		}
	}
}

// A failure that fits on one line is already on screen in the panel above; a
// notice repeating it word for word is the message twice.
func TestFailedLaunch_OneLineFailureIsNotSaidTwice(t *testing.T) {
	m := newTestModel()
	before := len(m.entries)
	m, _ = applyUpdate(m, errMsg{err: stringError("session/new: refused"), fatal: true})

	if len(m.entries) != before {
		t.Errorf("entries = %+v, want nothing echoing a one-line failure", m.entries)
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
	m.opts.Agent = testAgent("claude")
	m = m.relayout()

	view := m.View()
	for _, want := range []string{"Claude Code", "▝▜█████▛▘"} {
		if !strings.Contains(view, want) {
			t.Errorf("opening screen is missing %q:\n%s", want, view)
		}
	}
}

// An agent without branding still gets the layout, just without a drawing
// or a colour to call its own.
func TestEmptyChat_UnknownAgentStillNamed(t *testing.T) {
	m := newTestModel()
	m.opts.Agent = &config.PredefinedAgent{Name: "aider"}
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

// ctrl+c leaves the chat rather than being swallowed by "any key closes the
// key list". Outside tmux there is no list to go back to, so leaving is
// quitting — which is also what keeps this test from touching a real server.
func TestHelp_CtrlCLeaves(t *testing.T) {
	t.Setenv("TMUX", "")
	m := newTestModel()
	m.showHelp = true
	_, cmd := applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlC})

	if cmd == nil {
		t.Fatal("ctrl+c from the key list did nothing")
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
			m.opts.Agent.ACP.AuthCommand = tt.auth
			m.err = stringError("agent exited")
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
	for i := range 200 {
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

// ---------------------------------------------------------------------------
// Scroll pill
// ---------------------------------------------------------------------------

// scrolledModel is a chat with more log than fits, scrolled off the bottom.
func scrolledModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel()
	for i := range 200 {
		m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, fmt.Sprintf("line %d", i)))
		m, _ = applyUpdate(m, textUpdate(acp.UpdateUserMessage, "next"))
	}
	if !m.viewport.AtBottom() {
		t.Fatal("a fresh log should follow the agent to the bottom")
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.viewport.AtBottom() {
		t.Fatal("the log is too short to scroll, so this proves nothing")
	}
	return m
}

// At the bottom there is nothing to say, and the line the pill would take
// belongs to the conversation.
func TestScrollPill_SilentAtTheBottom(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "hello"))

	if pill := m.scrollPill(); pill != "" {
		t.Errorf("scrollPill() = %q at the bottom, want nothing", pill)
	}
	if strings.Contains(m.View(), "pgdn") {
		t.Errorf("the pill is on screen at the bottom:\n%s", m.View())
	}
}

func TestScrollPill_SaysWhereYouAreAndWhichKey(t *testing.T) {
	m := scrolledModel(t)

	if !strings.Contains(m.View(), "scrolled up") || !strings.Contains(m.View(), "pgdn") {
		t.Errorf("no pill after scrolling up:\n%s", m.View())
	}
	if m.newBelow {
		t.Error("nothing arrived, so this is a read-back, not missed activity")
	}
}

// The position alone is not the news. Something arriving while you are reading
// history is, and it reads differently.
func TestScrollPill_NamesNewActivity(t *testing.T) {
	m := scrolledModel(t)
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "an answer you cannot see"))

	if !m.newBelow {
		t.Fatal("content arrived below the fold and went unannounced")
	}
	if !strings.Contains(m.View(), "new activity") {
		t.Errorf("the pill still reads as a plain scroll position:\n%s", m.View())
	}

	// Back at the bottom the news is spent.
	m.viewport.GotoBottom()
	m = m.relayout()
	if m.newBelow {
		t.Error("newBelow survived a return to the bottom")
	}
	if strings.Contains(m.View(), "new activity") {
		t.Errorf("the pill outlived the thing it was announcing:\n%s", m.View())
	}
}

// The pill costs a footer line, and a footer that grows without a relayout is
// a viewport drawn over the input box.
func TestScrollPill_DoesNotOverlapTheInput(t *testing.T) {
	m := scrolledModel(t)

	lines := strings.Split(m.View(), "\n")
	if h := len(lines); h > m.height {
		t.Errorf("View() is %d lines in a %d-line terminal", h, m.height)
	}
	// The status line is the last thing drawn, whatever the footer is showing.
	last := lipgloss.NewStyle().Render(lines[len(lines)-1])
	if !strings.Contains(last, "send") {
		t.Errorf("last line is %q, want the status line", last)
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

// ---------------------------------------------------------------------------
// The render cache
// ---------------------------------------------------------------------------

// The cache is a memo of a pure function, so the one thing worth proving is
// that a cached render and a fresh one are the same string — served twice, so
// the second pass really is the memo speaking.
func TestRenderCache_AgreesWithRenderingFresh(t *testing.T) {
	feed := func(m Model) Model {
		m, _ = applyUpdate(m, textUpdate("agent_message_chunk", "some **prose**\n"))
		m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, acp.ToolCall{
			ToolCallID: "t1", Title: "go test ./...", Kind: "execute", Status: acp.StatusCompleted,
		}))
		m = m.appendNotice("a notice for the log")
		return m.relayout()
	}
	fresh := feed(newTestModel())

	cached := newTestModel()
	cached.cache = newRenderCache()
	cached = feed(cached)
	cached.relayout() // one pass to fill the memos

	if got, want := cached.renderLog(), fresh.renderLog(); got != want {
		t.Errorf("cached render disagrees with fresh:\n cached: %q\n fresh:  %q", got, want)
	}
}

// A patched tool call must repaint even though its entry was already cached —
// the merge stamps a new revision, which is the whole invalidation story.
func TestRenderCache_AMergedToolCallRepaints(t *testing.T) {
	m := newTestModel()
	m.cache = newRenderCache()
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, acp.ToolCall{
		ToolCallID: "t1", Title: "go vet ./...", Kind: "execute", Status: acp.StatusCompleted,
	}))
	if !strings.Contains(m.renderLog(), "✓") {
		t.Fatal("the completed call never showed its check mark")
	}
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCallUpdate, acp.ToolCall{
		ToolCallID: "t1", Status: acp.StatusFailed,
	}))
	if got := m.renderLog(); !strings.Contains(got, "✗") || strings.Contains(got, "✓") {
		t.Errorf("after the failure merge the log still shows the cached row:\n%s", got)
	}
}

// A resize refolds every line, so memos from the old width must not survive.
func TestRenderCache_AWidthChangeDropsTheMemos(t *testing.T) {
	m := newTestModel()
	m.cache = newRenderCache()
	long := strings.Repeat("words that will fold differently ", 6)
	m, _ = applyUpdate(m, textUpdate("agent_message_chunk", long))
	m = m.relayout()

	m, _ = applyUpdate(m, tea.WindowSizeMsg{Width: 48, Height: 30})
	fresh := newTestModel()
	fresh.width = 48
	fresh, _ = applyUpdate(fresh, textUpdate("agent_message_chunk", long))
	if got, want := m.renderLog(), fresh.renderLog(); got != want {
		t.Errorf("after a resize the cached log kept the old fold:\n got:  %q\n want: %q", got, want)
	}
}

// The agent's prose arrives in chunks and is re-rendered as markdown on each
// one. Half a fence is the interesting state: the opener has arrived, the
// closer has not, and the code between them must already read as code.
func TestAgentMessage_RendersMarkdownWhileStreaming(t *testing.T) {
	m := newTestModel()
	m.turn = true
	for _, chunk := range []string{"use **make", " check** first:\n``", "`\nmake check\n"} {
		m, _ = applyUpdate(m, textUpdate("agent_message_chunk", chunk))
	}

	view := m.View()
	if !strings.Contains(view, "use make check first:") {
		t.Errorf("view kept the ** markers:\n%s", view)
	}
	if !strings.Contains(view, "make check\n") && !strings.Contains(view, "make check ") {
		t.Errorf("the open fence's code line is missing:\n%s", view)
	}
	if strings.Contains(view, "```") {
		t.Errorf("the fence delimiter leaked into the view:\n%s", view)
	}
}

// A notice is often an error line from an adapter or a dumped stderr ring, and
// those run long. Unwrapped, it was the one entry kind that could push the
// whole log sideways.
func TestNotice_WrapsToTheColumn(t *testing.T) {
	m := newTestModel()
	long := strings.Repeat("the adapter said something ", 10)
	for _, line := range strings.Split(m.renderEntry(entry{kind: entryNotice, text: long}, 40), "\n") {
		if got := lipgloss.Width(line); got > 40 {
			t.Fatalf("a notice line rendered %d cells wide at width 40", got)
		}
	}
}

// Esc has always meant "stop": with a turn in flight it interrupts the agent,
// and with nothing running it clears a message not yet sent. The clear is
// recorded first, so ↑ is its undo.
func TestCancel_ClearsAnUnsentMessage(t *testing.T) {
	m := typeInto(newTestModel(), "half a thought")

	m, _ = applyUpdate(m, keyMsg("esc"))
	if got := m.input.Value(); got != "" {
		t.Fatalf("after esc the box holds %q, want it empty", got)
	}
	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "half a thought" {
		t.Fatalf("after ↑ the box holds %q, want the cleared message back", got)
	}
}

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

// dropped is what a terminal sends when a file is dragged onto it: one
// bracketed paste carrying the whole path.
func dropped(path string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(path), Paste: true}
}

// Dragging a file in should read the same as pasting one. Before this the drop
// sat in the composer as eighty characters of /Users/… until it was sent.
func TestDroppedImage_BecomesALabelInTheInput(t *testing.T) {
	m := newPastingModel()
	path := writeFile(t, "swatch.png", onePixelPNG)

	m, _ = applyUpdate(m, dropped(path))

	if got := m.input.Value(); !strings.Contains(got, "[image · swatch.png") {
		t.Errorf("input = %q, want the path replaced by its label", got)
	}
	if len(m.pending) != 1 {
		t.Errorf("pending = %d, want the image held", len(m.pending))
	}
}

// Terminals escape the spaces in a dragged path, and add one after it.
func TestDroppedImage_SurvivesEscapesAndATrailingSpace(t *testing.T) {
	m := newPastingModel()
	path := writeFile(t, "Screenshot 2026-08-10.png", onePixelPNG)

	m, _ = applyUpdate(m, dropped(strings.ReplaceAll(path, " ", `\ `)+" "))

	if got := m.input.Value(); !strings.Contains(got, "[image · Screenshot 2026-08-10.png") {
		t.Errorf("input = %q, want the escaped path resolved", got)
	}
}

// Typing a path by hand must not convert: the label would appear mid-word, and
// taking it back would take the typed path with it.
func TestTypedPath_IsNotConvertedWhileTyping(t *testing.T) {
	m := newPastingModel()
	path := writeFile(t, "swatch.png", onePixelPNG)

	for _, r := range path {
		m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.input.Value(); got != path {
		t.Errorf("input = %q, want the typed path left alone", got)
	}
	// It still travels as an image — it is only the label that waits.
	blocks, _ := m.composeTurn(m.input.Value(), typedHere)
	if len(blocks) != 1 || blocks[0].Type != acp.BlockImage {
		t.Errorf("blocks = %+v, want the typed path still sent as an image", blocks)
	}
}

// A dropped file that is not an image already reads perfectly well as a path.
func TestDroppedNonImage_StaysAPath(t *testing.T) {
	m := newPastingModel()
	path := writeFile(t, "notes.txt", []byte("plain words"))

	m, _ = applyUpdate(m, dropped(path))

	if got := m.input.Value(); got != path {
		t.Errorf("input = %q, want the path left as typed", got)
	}
}

// newPastingModel is a test model wired the way a paste needs: an agent that
// said it takes images.
func newPastingModel() Model {
	m := newTestModel()
	m.canSendImages = true
	return m
}

// mustCompose is the blocks the model would send for whatever is typed.
func mustCompose(m Model) []acp.ContentBlock {
	blocks, _ := m.composeTurn(strings.TrimSpace(m.input.Value()), typedHere)
	return blocks
}

func pastedImage() pastedImageMsg {
	return pastedImageMsg{block: acp.ImageBlock(
		base64.StdEncoding.EncodeToString(onePixelPNG), "image/png")}
}

// A pasted image lands in the message being written, not beside it. Anything
// less and the only way to learn whether ctrl+v worked is to send and ask.
func TestPastedImage_LandsInTheInput(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())

	if len(m.pending) != 1 {
		t.Fatalf("pending = %d, want the image held", len(m.pending))
	}
	if got := m.input.Value(); !strings.Contains(got, "[image") {
		t.Errorf("input = %q, want the attachment showing in it", got)
	}
}

// The label goes in at the cursor, so an image can be pasted mid-sentence
// rather than only at the end.
func TestPastedImage_GoesInAtTheCursor(t *testing.T) {
	m := newPastingModel()
	m.input.SetValue("why does ")
	m, _ = applyUpdate(m, pastedImage())
	m.input.InsertString("look wrong?")

	blocks, _ := m.composeTurn(m.input.Value(), typedHere)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v, want text, image, text", blocks)
	}
	if blocks[0].Text != "why does " || blocks[1].Type != acp.BlockImage {
		t.Errorf("blocks = %+v, want the image between the two halves", blocks)
	}
}

func TestPastedImage_IsSentOnceAndCleared(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())
	m.input.InsertString("what is wrong here?")

	// What the turn will actually put on the wire, captured before sending
	// clears the state it is built from.
	var sent struct{ image bool }
	for _, b := range mustCompose(m) {
		sent.image = sent.image || b.Type == acp.BlockImage
	}

	m, _ = applyUpdate(m, keyMsg("enter"))

	if len(m.pending) != 0 {
		t.Errorf("pending = %d, want it cleared; a second turn would send the image twice", len(m.pending))
	}
	if len(m.entries) != 1 {
		t.Fatalf("entries = %+v, want the one message", m.entries)
	}
	text := m.entries[0].text
	if !strings.Contains(text, "[image") || !strings.Contains(text, "what is wrong here?") {
		t.Errorf("logged %q, want it to show both the image and the question", text)
	}
	// The log alone cannot tell the difference: an attachment left behind as
	// literal text logs the identical line, because the label in the input is
	// exactly what the log prints. Only the blocks say which happened.
	if !sent.image {
		t.Error("no image block was composed; the label went as text")
	}
}

// Deleting the label is how an attachment is taken back — the only undo a
// marker sitting in a text box can have.
func TestPastedImage_DeletingTheLabelDropsTheImage(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())
	m.input.SetValue("never mind")

	blocks, _ := m.composeTurn(m.input.Value(), typedHere)
	for _, b := range blocks {
		if b.Type == acp.BlockImage {
			t.Fatalf("blocks = %+v, want the image gone with its label", blocks)
		}
	}
}

// One backspace, not twenty-five. A label reads as one thing, so a keystroke
// that breaks it takes the whole thing rather than leaving a chewed remnant.
func TestPastedImage_OneBackspaceTakesTheWholeLabel(t *testing.T) {
	m := newPastingModel()
	m.input.SetValue("why does ")
	m, _ = applyUpdate(m, pastedImage())

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyBackspace}) // the trailing space
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyBackspace}) // into the label

	if got := m.input.Value(); got != "why does " {
		t.Errorf("input = %q, want the label gone whole and the message intact", got)
	}
}

// The repair must not fire on ordinary typing, or every keystroke after a paste
// would eat the attachment.
func TestPastedImage_OrdinaryTypingLeavesTheLabelAlone(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())
	before := m.input.Value()

	for _, r := range "looks odd" {
		m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.input.Value(); got != before+"looks odd" {
		t.Errorf("input = %q, want %q", got, before+"looks odd")
	}
}

// Two pastes of the same image share a label, so the labels have to be matched
// in order rather than both resolving to the first block.
func TestPastedImage_TwoOfTheSameResolveInOrder(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())
	m.input.InsertString("and ")
	m, _ = applyUpdate(m, pastedImage())

	blocks, _ := m.composeTurn(m.input.Value(), typedHere)
	images := 0
	for _, b := range blocks {
		if b.Type == acp.BlockImage {
			images++
		}
	}
	if images != 2 {
		t.Errorf("images = %d, want both sent", images)
	}
}

// Two different attachments have to come out in the order they appear in the
// message, not the order they were pasted — they can be rearranged by editing.
func TestPastedImage_ResolveInMessageOrderNotPasteOrder(t *testing.T) {
	m := newPastingModel()
	first := acp.ImageBlock(base64.StdEncoding.EncodeToString(onePixelPNG), "image/png")
	second := acp.ImageBlock(base64.StdEncoding.EncodeToString(append(onePixelPNG, 0, 0)), "image/png")
	m.pending = []acp.ContentBlock{first, second}
	m.input.SetValue(imageLabel(second) + " then " + imageLabel(first))

	var got []string
	for _, b := range mustCompose(m) {
		if b.Type == acp.BlockImage {
			got = append(got, b.Data)
		}
	}
	if len(got) != 2 || got[0] != second.Data || got[1] != first.Data {
		t.Errorf("images came out in paste order, not the order they are written in")
	}
}

// The size ceiling is reported rather than swallowed: a paste that silently did
// nothing is indistinguishable from a key that is not bound.
func TestPastedImage_TooLargeSaysSo(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, pastedImageMsg{err: "that image is 9.1 MB — too large to send"})

	if len(m.pending) != 0 {
		t.Errorf("pending = %d, want nothing attached", len(m.pending))
	}
	if len(m.entries) != 1 || m.entries[0].kind != entryNotice {
		t.Fatalf("entries = %+v, want one notice", m.entries)
	}
}

// ctrl+v has to be taken from the textarea before the fallthrough, or the text
// paste happens first and an image can never be noticed.
func TestCtrlV_IsClaimedBeforeTheTextarea(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("before")

	// The textarea answers ctrl+v with a command of its own, so a non-nil one
	// proves nothing. Only reaching this does.
	claimed := false
	original := pasteCmd
	pasteCmd = func() tea.Cmd {
		claimed = true
		return func() tea.Msg { return nil }
	}
	defer func() { pasteCmd = original }()

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlV})
	if !claimed {
		t.Error("ctrl+v fell through to the textarea, so an image can never be noticed")
	}
	if got := m.input.Value(); got != "before" {
		t.Errorf("input = %q, want ctrl+v to leave it to the command", got)
	}
}

// A message the client renders nothing for must not leave a marker where a
// paragraph would be — a bare coloured bullet on a line of its own reads as a
// rendering fault.
func TestAgentBlock_WithNothingToShowDrawsNoEntry(t *testing.T) {
	m := newTestModel()
	m = m.appendNotice("session resumed")

	m, _ = applyUpdate(m, acpUpdateMsg(acp.SessionUpdate{
		Type:    acp.UpdateAgentMessage,
		Message: &acp.MessageChunk{Content: acp.ContentBlock{Type: "audio", Data: "…"}},
	}))

	if len(m.entries) != 1 {
		t.Errorf("entries = %+v, want only the notice", m.entries)
	}
}

// A replay arrives one block per notification where sending composed them in
// one pass, and the two have to end up reading the same. opencode really does
// replay a pasted image followed by the question as two chunks.
func TestReplayedImage_DoesNotRunIntoTheQuestion(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, userChunk(acp.ImageBlock(
		base64.StdEncoding.EncodeToString(onePixelPNG), "image/png")))
	m, _ = applyUpdate(m, userChunk(acp.TextBlock("describe this image")))

	if len(m.entries) != 1 {
		t.Fatalf("entries = %+v, want the one message", m.entries)
	}
	if !strings.Contains(m.entries[0].text, "]\ndescribe this image") {
		t.Errorf("replayed %q, want the question on its own line", m.entries[0].text)
	}
}

// Captured from opencode replaying a pasted image: it was sent with no uri at
// all, and comes back wearing one that ends in "/image".
func TestImageLabel_IgnoresAnInventedURI(t *testing.T) {
	got := imageLabel(acp.ContentBlock{
		Type:     acp.BlockImage,
		MimeType: "image/png",
		URI:      "file:///repo/.opentree/imgcheck/image",
		Data:     base64.StdEncoding.EncodeToString(onePixelPNG),
	})
	if strings.Contains(got, "image · image") {
		t.Errorf("imageLabel = %q, want the invented name dropped", got)
	}
	if !strings.Contains(got, "png") {
		t.Errorf("imageLabel = %q, want it to fall back to the kind of image", got)
	}
}

func TestImageLabel_KeepsARealFileName(t *testing.T) {
	got := imageLabel(acp.ContentBlock{
		Type: acp.BlockImage, MimeType: "image/png",
		URI: "file:///repo/swatch.png",
	})
	if !strings.Contains(got, "swatch.png") {
		t.Errorf("imageLabel = %q, want it to name the file", got)
	}
}

// An image inside a tool call is the one thing a screenshot-taking tool has to
// show, and it used to finish with nothing underneath it.
func TestToolOutput_NamesAnImageItReturned(t *testing.T) {
	img := acp.ImageBlock(base64.StdEncoding.EncodeToString(onePixelPNG), "image/png")
	call := acp.ToolCall{
		ToolCallID: "t1", Title: "screenshot", Status: acp.StatusCompleted,
		Content: []acp.ToolCallContent{{Type: "content", Content: &img}},
	}
	if got := toolOutput(call); !strings.Contains(got, "image") {
		t.Errorf("toolOutput = %q, want it to say an image came back", got)
	}
}

// ACP says a client must restrict what it sends to the capabilities the agent
// declared, so this bool is the difference between a feature and a violation.
func TestWithAgentInfo_BelievesTheAgentAboutImages(t *testing.T) {
	m := newTestModel()

	m = m.withAgentInfo(&acp.InitializeResponse{})
	if m.canSendImages {
		t.Error("canSendImages is set for an agent that declared nothing")
	}

	m = m.withAgentInfo(&acp.InitializeResponse{AgentCapabilities: acp.AgentCapabilities{
		PromptCapabilities: acp.PromptCapabilities{Image: true},
	}})
	if !m.canSendImages {
		t.Error("canSendImages is unset for an agent that advertised image support")
	}
}

// The window between opening and having an agent used to render an ordinary
// composer with nothing behind it — enter discarded whatever was typed,
// because Send is inert with no session, and nothing said why. For an adapter
// that accepts stdio and never completes the handshake, that was the whole
// experience of the window.
func TestOverlay_LaunchingSaysSoInsteadOfInvitingAMessage(t *testing.T) {
	m := newTestModel()
	m.sessionID = ""
	m.launching = true

	if got := m.overlay(); got != overlayLaunching {
		t.Fatalf("overlay() = %v, want overlayLaunching", got)
	}
	if _, ok := overlayDefs[overlayLaunching]; !ok {
		t.Fatal("overlayLaunching has no row in overlayDefs — its keys, height and view would not be wired")
	}
	if !strings.Contains(m.launchingView(), "starting") {
		t.Errorf("launching panel = %q, want it to say the agent is starting", m.launchingView())
	}
}

// A launch that failed hands over to the stopped panel, which is the one that
// says what went wrong and offers the restart. Left set, the launching panel
// would sit on top of it forever.
func TestLaunching_ClearsOnAFailedLaunch(t *testing.T) {
	m := newTestModel()
	m.launching = true

	next, _ := m.update(errMsg{err: errors.New("adapter not installed"), fatal: true})
	nm, ok := next.(Model)
	if !ok {
		t.Fatal("update did not return a Model")
	}
	if nm.launching {
		t.Error("a failed launch left the launching panel up")
	}
	if got := nm.overlay(); got != overlayStopped {
		t.Errorf("overlay() = %v, want overlayStopped", got)
	}
}

// And a launch that succeeded gives the window back to the conversation.
func TestLaunching_ClearsWhenTheClientArrives(t *testing.T) {
	m := newTestModel()
	m.launching = true

	next, _ := m.update(clientReadyMsg{generation: 1})
	nm, ok := next.(Model)
	if !ok {
		t.Fatal("update did not return a Model")
	}
	if nm.launching {
		t.Error("the launching panel outlived the launch")
	}
}

// ---------------------------------------------------------------------------
// The spinner
// ---------------------------------------------------------------------------

// The tick chain sustains itself but has to be begun, and the only thing that
// ever began one was a turn. A setup phase runs before any turn exists, so the
// panel saying "setting up…" sat on frame zero for the whole of an install that
// can run for minutes — a window that reads as hung rather than as working.
func TestSetup_SpinnerRunsWhileTheCommandsDo(t *testing.T) {
	m := setupModel(t, approvedSetup("pnpm install"))
	m, _ = applyUpdate(m, setupBeginMsg{})

	if !m.spinning {
		t.Fatal("the commands are running and nothing asked for a frame")
	}
	before := m.spinnerFrame
	m, cmd := applyUpdate(m, spinnerTickMsg{})
	if m.spinnerFrame == before {
		t.Error("a tick during setup did not move the spinner on")
	}
	if cmd == nil {
		t.Error("the chain stopped after one frame")
	}
}

// One chain at a time, and a new one once it has ended. Two are reachable —
// the setup retry key arrives while the previous run's last tick may still be
// in flight, and a queued prompt begins its turn in the same update that ended
// the one before it — and two self-sustaining chains never stop.
func TestSpin_OneChainAtATime(t *testing.T) {
	m := newTestModel()

	m, first := m.spin()
	if first == nil {
		t.Fatal("nothing started the spinner")
	}
	if _, second := m.spin(); second != nil {
		t.Error("a second chain was started over the first; both would tick for ever")
	}

	// Nothing is running any more, so the chain ends — and whatever needs one
	// next has to be able to start a fresh one.
	m, cmd := applyUpdate(m, spinnerTickMsg{})
	if cmd != nil {
		t.Error("the spinner kept ticking with nothing to spin for")
	}
	if m.spinning {
		t.Fatal("the chain ended but the model still thinks one is running")
	}
	if _, again := m.spin(); again == nil {
		t.Error("the spinner could never be started again")
	}
}

// The frame lives inside rendered entries, and the viewport caches whatever
// the last relayout gave it. A tick that only advanced the counter left the
// thinking line and every running tool's glyph frozen until the agent next
// said something — exactly the quiet stretch the spinner exists for.
func TestSpin_TickRepaintsTheLog(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m.spinning = true
	m = m.relayout()

	before := m.View()
	m, _ = applyUpdate(m, spinnerTickMsg{})
	if m.View() == before {
		t.Error("a tick moved the frame on but the visible log never changed")
	}
}

// ---------------------------------------------------------------------------
// Whose message a turn is carrying
// ---------------------------------------------------------------------------

// A prompt from the workspace list is somebody else's sentence. Attaching the
// image the user is halfway through composing to it sends a screenshot nobody
// meant to send — and leaves the label for it sitting in a message box whose
// attachment has gone.
func TestRemotePrompt_LeavesThePastedImageAlone(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())
	m.input.InsertString("what is wrong here?")
	writing := m.input.Value()

	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPrompt, Text: "run the tests"}})

	if len(m.pending) != 1 {
		t.Errorf("pending = %d, want the user's attachment still waiting for its own message", len(m.pending))
	}
	if got := m.input.Value(); got != writing {
		t.Errorf("input = %q, want the half-written message untouched", got)
	}
	if len(m.entries) != 1 {
		t.Fatalf("entries = %+v, want the one remote prompt", m.entries)
	}
	if strings.Contains(m.entries[0].text, "image") {
		t.Errorf("logged %q, want a remote prompt to carry no attachment", m.entries[0].text)
	}
}

// ctrl+r retries a failed turn with the message exactly as it went — the
// blocks, not the text, so a pasted image is retried too, which ↑-and-enter
// cannot do: recall returns an image's label, this returns the image.
func TestRetry_ResendsTheFailedMessage(t *testing.T) {
	m := sendMessage(newTestModel(), "flaky request")
	m, _ = applyUpdate(m, promptDoneMsg{err: stringError("connection lost")})

	if !strings.Contains(m.View(), "ctrl+r to retry") {
		t.Error("a failed turn should offer the retry key")
	}
	m, cmd := applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if !m.turn || cmd == nil {
		t.Fatal("ctrl+r did not restart the turn")
	}
	if m.err != nil {
		t.Error("the old failure should clear when the retry goes")
	}
	if last := m.entries[len(m.entries)-1]; last.kind != entryUser || last.text != "flaky request" {
		t.Errorf("last entry = %+v, want the message sent again", last)
	}
}

func TestRetry_OnlyAfterAFailure(t *testing.T) {
	m := sendMessage(newTestModel(), "all fine")
	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})

	m, cmd := applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.turn || cmd != nil {
		t.Error("ctrl+r with nothing failed should do nothing")
	}
}

// Enter during a live turn used to be a silent no-op — the one key that did
// nothing and said nothing. Now the message queues where you can see it, and
// fires when the agent is free.
func TestTypedPrompt_QueuesWhileTheAgentWorks(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m = typeInto(m, "and update the docs")
	m, _ = applyUpdate(m, keyMsg("enter"))

	if got := m.input.Value(); got != "" {
		t.Errorf("input = %q, want the box cleared once the message is queued", got)
	}
	if !strings.Contains(m.View(), "⏳ and update the docs") {
		t.Error("a queued message should wait above the box, visibly")
	}

	m, cmd := applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	if !m.turn || cmd == nil {
		t.Fatal("the queued message did not start its turn when the agent freed up")
	}
	if last := m.entries[len(m.entries)-1]; last.kind != entryUser || last.text != "and update the docs" {
		t.Errorf("last entry = %+v, want the queued message sent", last)
	}
}

// A message typed here keeps its attachments even through the wait: the image
// is captured at enter, so pasting another for the next message cannot cross.
func TestTypedPrompt_QueuedImageTravelsWithItsMessage(t *testing.T) {
	m := newPastingModel()
	m.turn = true
	m, _ = applyUpdate(m, pastedImage())
	m = typeInto(m, "what is this")
	m, _ = applyUpdate(m, keyMsg("enter"))

	if len(m.pending) != 0 {
		t.Fatalf("pending = %d, want the image committed to the queued message", len(m.pending))
	}
	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	if !m.turn {
		t.Fatal("the queued message did not run")
	}
	if last := m.entries[len(m.entries)-1]; !strings.Contains(last.text, "image") {
		t.Errorf("sent %q, want the image along with the message", last.text)
	}
}

// Backspace on an empty box takes the newest queued message back to be edited
// — or deleted, which is the same gesture one keypress longer.
func TestTypedPrompt_BackspaceTakesTheNewestBack(t *testing.T) {
	m := newTestModel()
	m.turn = true
	for _, text := range []string{"first thought", "second thought"} {
		m = typeInto(m, text)
		m, _ = applyUpdate(m, keyMsg("enter"))
	}

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.input.Value(); got != "second thought" {
		t.Fatalf("input = %q, want the newest queued message back", got)
	}
	if len(m.queue) != 1 || m.queue[0].text != "first thought" {
		t.Errorf("queue = %+v, want the older message still waiting", m.queue)
	}
}

// The queue drains oldest first, whoever queued: answers should land in the
// order the questions were asked, typed here or sent from the list.
func TestTypedPrompt_DrainsInArrivalOrder(t *testing.T) {
	m := newTestModel()
	m.turn = true
	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPrompt, Text: "from the list"}})
	m = typeInto(m, "typed after")
	m, _ = applyUpdate(m, keyMsg("enter"))

	m, _ = applyUpdate(m, promptDoneMsg{resp: &acp.PromptResponse{StopReason: acp.StopEndTurn}})
	if last := m.entries[len(m.entries)-1]; last.text != "from the list" {
		t.Fatalf("first drained = %q, want the older message first", last.text)
	}
	if len(m.queue) != 1 || m.queue[0].text != "typed after" {
		t.Errorf("queue = %+v, want the newer message still waiting its turn", m.queue)
	}
}

// And the same when it waited in the queue first: the wait makes the case
// stronger, since the image was pasted after the prompt was already accepted.
func TestQueuedPrompt_LeavesThePastedImageAlone(t *testing.T) {
	m := newPastingModel()
	m.turn = true
	m, _ = applyUpdate(m, socketCommandMsg{cmd: Command{Type: CommandPrompt, Text: "run the tests"}})
	if len(m.queue) != 1 || m.queue[0].text != "run the tests" {
		t.Fatalf("queue = %+v, want the prompt held while the agent works", m.queue)
	}

	m, _ = applyUpdate(m, pastedImage())
	m, _ = applyUpdate(m, promptDoneMsg{}) // the turn ends and the queue drains

	if !m.turn {
		t.Fatal("the queued prompt did not run")
	}
	if len(m.pending) != 1 {
		t.Errorf("pending = %d, want the image still waiting for the message it belongs to", len(m.pending))
	}
}

// The message typed here still takes its attachments with it, and still clears
// them — a second turn must not send the same image twice.
func TestTypedPrompt_StillCarriesThePastedImage(t *testing.T) {
	m := newPastingModel()
	m, _ = applyUpdate(m, pastedImage())
	m.input.InsertString("what is wrong here?")

	m, _ = applyUpdate(m, keyMsg("enter"))

	if len(m.pending) != 0 {
		t.Errorf("pending = %d, want it cleared by the message that carried it", len(m.pending))
	}
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, "image") {
		t.Errorf("entries = %+v, want the image sent with what was typed", m.entries)
	}
}
