package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/chat"
)

func wsWithChat(name string, st *chat.Status) WorkspaceItem {
	ws := testWS(name)
	ws.ChatStatus = st
	return ws
}

var awaitingStatus = &chat.Status{
	State: chat.StateAwaiting,
	Permission: &chat.Permission{
		Title: "rm -rf dist",
		Options: []acp.PermissionOption{
			{OptionID: "once", Kind: acp.PermissionAllowOnce, Name: "Allow once"},
			{OptionID: "reject", Kind: acp.PermissionRejectOnce, Name: "Reject"},
		},
	},
}

func TestRenderChatBadge(t *testing.T) {
	tests := []struct {
		name   string
		status *chat.Status
		want   string
	}{
		{"no chat", nil, ""},
		{"idle", &chat.Status{State: chat.StateIdle}, "idle"},
		{"starting", &chat.Status{State: chat.StateStarting}, "starting"},
		{"stopped", &chat.Status{State: chat.StateStopped}, "agent stopped"},
		{"working names the tool", &chat.Status{State: chat.StateWorking, Tool: "go test ./..."}, "go test ./..."},
		{"awaiting names the call", awaitingStatus, "PERMISSION"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderChatBadge(tt.status)
			if tt.want == "" {
				if got != "" {
					t.Errorf("badge = %q, want empty so the file-based badge takes over", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("badge = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestTruncateBadge(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := renderChatBadge(&chat.Status{State: chat.StateWorking, Tool: long})
	if len([]rune(got)) > 60 {
		t.Errorf("badge is %d runes, want it capped so the row still fits", len([]rune(got)))
	}
}

func TestChatMeta(t *testing.T) {
	if got := chatMeta(nil); got != "" {
		t.Errorf("chatMeta(nil) = %q, want empty", got)
	}
	if got := chatMeta(&chat.Status{State: chat.StateIdle}); got != "" {
		t.Errorf("chatMeta with no usage = %q, want empty", got)
	}
	got := chatMeta(&chat.Status{ContextPct: 42, Cost: 1.5})
	for _, want := range []string{"42% ctx", "$1.50"} {
		if !strings.Contains(got, want) {
			t.Errorf("chatMeta() = %q, want it to contain %q", got, want)
		}
	}
}

func TestPendingPermission(t *testing.T) {
	if wsWithChat("a", nil).pendingPermission() != nil {
		t.Error("no chat means no pending permission")
	}
	if wsWithChat("a", &chat.Status{State: chat.StateWorking}).pendingPermission() != nil {
		t.Error("a working agent is not awaiting permission")
	}
	if wsWithChat("a", awaitingStatus).pendingPermission() == nil {
		t.Error("expected the pending permission")
	}
}

func TestAnswerDialog_OpensOnlyWhenPending(t *testing.T) {
	m := newTestModel()
	if m.openAnswerDialog(wsWithChat("a", nil)).answering {
		t.Error("dialog should not open without a pending permission")
	}
	if m.openAnswerDialog(wsWithChat("a", &chat.Status{State: chat.StateAwaiting})).answering {
		t.Error("dialog should not open with no options to choose from")
	}

	opened := m.openAnswerDialog(wsWithChat("fix-auth", awaitingStatus))
	if !opened.answering {
		t.Fatal("expected the dialog to open")
	}
	if opened.answerWs != "fix-auth" {
		t.Errorf("answerWs = %q, want fix-auth", opened.answerWs)
	}
}

func TestAnswerDialog_Navigation(t *testing.T) {
	m := newTestModel().openAnswerDialog(wsWithChat("fix-auth", awaitingStatus))

	m, _ = m.handleAnswerKey(keyMsg("down"))
	if m.answerCursor != 1 {
		t.Errorf("cursor = %d, want 1", m.answerCursor)
	}
	m, _ = m.handleAnswerKey(keyMsg("down"))
	if m.answerCursor != 0 {
		t.Errorf("cursor = %d, want it to wrap to 0", m.answerCursor)
	}
	m, _ = m.handleAnswerKey(keyMsg("up"))
	if m.answerCursor != 1 {
		t.Errorf("cursor = %d, want it to wrap to 1", m.answerCursor)
	}
}

func TestAnswerDialog_EscCloses(t *testing.T) {
	m := newTestModel().openAnswerDialog(wsWithChat("fix-auth", awaitingStatus))
	m, cmd := m.handleAnswerKey(keyMsg("esc"))
	if m.answering {
		t.Error("esc should close the dialog")
	}
	if cmd != nil {
		t.Error("cancelling must not send anything to the agent")
	}
}

func TestAnswerDialog_EnterSends(t *testing.T) {
	m := newTestModel().openAnswerDialog(wsWithChat("fix-auth", awaitingStatus))
	m, cmd := m.handleAnswerKey(keyMsg("enter"))
	if m.answering {
		t.Error("dialog should close once answered")
	}
	if cmd == nil {
		t.Fatal("expected a command to be sent")
	}
	// No chat process is listening, so the send fails — which is itself proof
	// the dialog tried to reach one.
	if _, ok := cmd().(errMsg); !ok {
		t.Errorf("cmd produced %T, want an errMsg from the unreachable socket", cmd())
	}
}

func TestAnswerDialog_NumberKeySends(t *testing.T) {
	m := newTestModel().openAnswerDialog(wsWithChat("fix-auth", awaitingStatus))
	m, cmd := m.handleAnswerKey(keyMsg("2"))
	if m.answering {
		t.Error("dialog should close once answered")
	}
	if cmd == nil {
		t.Error("expected the second option to be sent")
	}
}

func TestAnswerDialog_UnboundKeyIsIgnored(t *testing.T) {
	m := newTestModel().openAnswerDialog(wsWithChat("fix-auth", awaitingStatus))
	m, cmd := m.handleAnswerKey(keyMsg("9"))
	if !m.answering {
		t.Error("an out-of-range option should leave the dialog open")
	}
	if cmd != nil {
		t.Error("nothing should have been sent")
	}
}

func TestChatWorking(t *testing.T) {
	if wsWithChat("a", nil).chatWorking() {
		t.Error("no chat is not working")
	}
	if !wsWithChat("a", &chat.Status{State: chat.StateWorking}).chatWorking() {
		t.Error("expected working")
	}
}

func TestAnswerKey_OpensDialogFromTheList(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", awaitingStatus))
	m, _ = applyUpdate(m, keyMsg("a"))
	if !m.answering {
		t.Error("a should open the permission dialog")
	}
}

func TestAnswerKey_NoopWithoutAPendingPermission(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("a"))
	if m.answering {
		t.Error("a should do nothing when nothing is pending")
	}
}

func TestInterruptKey_OnlyWhileWorking(t *testing.T) {
	idle := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	if _, cmd := applyUpdate(idle, keyMsg("c")); cmd != nil {
		t.Error("interrupt should do nothing when the agent is idle")
	}

	busy := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateWorking}))
	if _, cmd := applyUpdate(busy, keyMsg("c")); cmd == nil {
		t.Error("expected an interrupt to be sent")
	}
}

func TestMessageKey_OpensPromptForChatWorkspaces(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("m"))
	if !m.prompting {
		t.Fatal("m should open the message dialog")
	}
	if m.promptWs != "fix-auth" {
		t.Errorf("promptWs = %q, want fix-auth", m.promptWs)
	}
}

func TestMessageKey_IgnoredWithoutAChat(t *testing.T) {
	m := newTestModel(testWS("plain-agent"))
	m, _ = applyUpdate(m, keyMsg("m"))
	if m.prompting {
		t.Error("m should do nothing for a workspace with no chat process")
	}
}

func TestPromptDialog_EscCancels(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("m"))
	m.input.SetValue("half a thought")
	m, _ = applyUpdate(m, keyMsg("esc"))

	if m.prompting {
		t.Error("esc should close the message dialog")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared", m.input.Value())
	}
}

func TestPromptDialog_EmptySendIsANoop(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("m"))
	m.input.SetValue("   ")
	m, cmd := applyUpdate(m, keyMsg("enter"))

	if m.prompting {
		t.Error("dialog should close")
	}
	if cmd != nil {
		t.Error("whitespace should not be sent to the agent")
	}
}

func TestPromptDialog_View(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("m"))
	if !strings.Contains(m.View(), "Message agent: fix-auth") {
		t.Errorf("View() should show the message dialog\ngot: %s", m.View())
	}
}

func TestAnswerDialog_View(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", awaitingStatus))
	m, _ = applyUpdate(m, keyMsg("a"))
	view := m.View()
	for _, want := range []string{"Agent permission: fix-auth", "rm -rf dist", "[1] Allow once", "[2] Reject"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q\ngot: %s", want, view)
		}
	}
}

func TestChatBadge_TakesPrecedenceInTheRow(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateWorking, Tool: "edit main.go"}))
	if !strings.Contains(m.View(), "edit main.go") {
		t.Errorf("View() should show the chat badge\ngot: %s", m.View())
	}
}

func TestAgentCommandSent_ShowsNoticeAndRefreshes(t *testing.T) {
	m := newTestModel()
	m, cmd := applyUpdate(m, agentCommandSentMsg{wsName: "fix-auth", action: "answered"})
	if !strings.Contains(m.notice, "fix-auth") {
		t.Errorf("notice = %q, want it to name the workspace", m.notice)
	}
	if cmd == nil {
		t.Error("expected a refresh so the badge updates immediately")
	}
}

var _ tea.Model = Model{}
