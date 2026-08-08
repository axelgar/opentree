package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
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

func TestAnswerKey_SaysWhyWithoutAPendingPermission(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("a"))
	if m.answering {
		t.Error("a should not open the dialog when nothing is pending")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "not waiting on a permission") {
		t.Errorf("err = %v, want an explanation of why a did nothing", m.err)
	}
}

func TestInterruptKey_OnlyWhileWorking(t *testing.T) {
	idle := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateIdle}))
	idle, _ = applyUpdate(idle, keyMsg("c"))
	if idle.err == nil || !strings.Contains(idle.err.Error(), "not working") {
		t.Errorf("err = %v, want interrupting an idle agent to say so", idle.err)
	}

	busy := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateWorking}))
	busy, cmd := applyUpdate(busy, keyMsg("c"))
	if cmd == nil {
		t.Error("expected an interrupt to be sent")
	}
	if busy.err != nil {
		t.Errorf("interrupting a working agent complained: %v", busy.err)
	}
}

// Only ACP agents have a chat, so in a mixed repo these keys apply to some rows
// and not others. Each one says so rather than appearing broken.
func TestChatKeys_ExplainThemselvesWithoutAChat(t *testing.T) {
	for _, k := range []string{"m", "a", "c"} {
		t.Run(k, func(t *testing.T) {
			m := newTestModel(testWS("fix-auth"))
			m, _ = applyUpdate(m, keyMsg(k))

			if m.prompting || m.answering {
				t.Fatalf("%q opened a dialog for a workspace with no chat", k)
			}
			if m.err == nil || !strings.Contains(m.err.Error(), "has no chat") {
				t.Errorf("err = %v, want %q to explain that there is no chat", m.err, k)
			}
		})
	}
}

func TestChatKeys_ExplainAStoppedAgent(t *testing.T) {
	m := newTestModel(wsWithChat("fix-auth", &chat.Status{State: chat.StateStopped}))
	m, _ = applyUpdate(m, keyMsg("m"))

	if m.prompting {
		t.Fatal("m opened a prompt for a stopped agent")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "attach to restart") {
		t.Errorf("err = %v, want a stopped agent to point at attaching", m.err)
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

// ---------------------------------------------------------------------------
// Adapter install from the agent picker
// ---------------------------------------------------------------------------

func TestAgentReadiness(t *testing.T) {
	// opencode serves ACP itself, so being installed is the whole story.
	opencode := *config.FindAgent("opencode")
	status, _ := agentReadiness(opencode)
	if opencode.IsInstalled() && status != "installed" {
		t.Errorf("opencode status = %q, want installed", status)
	}

	// An agent with no ACP mode is judged the same way it always was.
	if status, ready := agentReadiness(*config.FindAgent("pi")); ready || status != "not found" {
		t.Errorf("pi = %q/%v, want not found", status, ready)
	}
}

func TestAgentReadiness_AdapterMissingIsItsOwnState(t *testing.T) {
	// The distinction the picker exists to show: the agent is there, the thing
	// that speaks ACP for it is not. Reporting that as "installed" sends you
	// into a chat that cannot start.
	claude := *config.FindAgent("claude")
	if !claude.IsInstalled() {
		t.Skip("claude is not installed here")
	}

	status, ready := agentReadiness(claude)
	if claude.ACPInstalled() {
		if status != "installed" || !ready {
			t.Errorf("with the adapter present = %q/%v, want installed", status, ready)
		}
		return
	}
	if status != "adapter missing" || ready {
		t.Errorf("without the adapter = %q/%v, want \"adapter missing\"", status, ready)
	}
}

func TestAgentPicker_InstallKey(t *testing.T) {
	m := newTestModel()
	m.agentSelecting = true

	// Land on an agent that needs no adapter: the key should say so rather
	// than silently doing nothing.
	for i := range config.PredefinedAgents {
		if config.PredefinedAgents[i].Command == "opencode" {
			m.agentCursor = i
		}
	}
	m, _ = applyUpdate(m, keyMsg("i"))
	if m.err == nil {
		t.Error("expected an explanation for an agent with no adapter")
	}
	if !m.agentSelecting {
		t.Error("the picker should stay open when nothing was installed")
	}
}

func TestAgentPicker_ShowsInstallHint(t *testing.T) {
	m := newTestModel()
	m.agentSelecting = true
	if !strings.Contains(m.View(), "i install adapter") {
		t.Errorf("the picker should advertise the install key\ngot: %s", m.View())
	}
}

func TestAdapterInstalled_Reported(t *testing.T) {
	m := newTestModel()
	m, cmd := applyUpdate(m, adapterInstalledMsg{adapter: "claude-agent-acp"})
	if !strings.Contains(m.notice, "claude-agent-acp") {
		t.Errorf("notice = %q, want it to name what was installed", m.notice)
	}
	if cmd == nil {
		t.Error("expected the notice to be scheduled for clearing")
	}
	// The cmd is a timer; running it here would block for its duration.
}

func TestAdapterInstall_FailureIsReported(t *testing.T) {
	// Assert on the model, not by running the cmd: transientErrCmd sets the
	// error synchronously and returns a tea.Tick that only clears it later —
	// executing that blocks the test for the whole timer.
	m := newTestModel()
	m, _ = applyUpdate(m, adapterInstalledMsg{adapter: "claude-agent-acp", err: errStr("npm ERR!")})

	if m.err == nil {
		t.Fatal("expected the install failure to surface")
	}
	for _, want := range []string{"claude-agent-acp", "npm ERR!"} {
		if !strings.Contains(m.err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", m.err, want)
		}
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// Enter means "use this agent"
// ---------------------------------------------------------------------------

func readinessOf(status string) func(config.PredefinedAgent) (string, bool) {
	return func(config.PredefinedAgent) (string, bool) { return status, status == agentReady }
}

// pickerOn opens the agent picker with the cursor on Claude Code, whose
// readiness is stubbed so the test does not depend on this machine.
func pickerOn(t *testing.T, status string) Model {
	t.Helper()
	t.Chdir(t.TempDir()) // selecting persists to config; keep it out of the repo
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 1 // Claude Code
	m.agentReadiness = readinessOf(status)
	return m
}

func TestEnter_UninstalledAgentIsRefused(t *testing.T) {
	// The old behaviour recorded the choice anyway, leaving you "using" an
	// agent that cannot start.
	m := pickerOn(t, agentNotFound)
	before := m.cfg.Agent.Command

	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.cfg.Agent.Command != before {
		t.Errorf("agent = %q, want it unchanged at %q", m.cfg.Agent.Command, before)
	}
	if !m.agentSelecting {
		t.Error("the picker should stay open so another agent can be chosen")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "not installed") {
		t.Errorf("err = %v, want it to say the agent is not installed", m.err)
	}
	// The overlay covers the list, so the message has to appear on the overlay
	// itself or enter reads as a dead key.
	if !strings.Contains(m.View(), "not installed") {
		t.Errorf("the picker should show why enter was refused\ngot:\n%s", m.View())
	}
}

func TestEnter_AdapterMissingAsksFirst(t *testing.T) {
	m := pickerOn(t, agentAdapterMissing)
	before := m.cfg.Agent.Command

	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.agentInstallConfirm == nil {
		t.Fatal("expected a confirmation before a 300MB download")
	}
	if m.cfg.Agent.Command != before {
		t.Errorf("agent = %q, want nothing committed until the install is agreed", m.cfg.Agent.Command)
	}
	view := m.View()
	for _, want := range []string{"Install adapter", "claude-agent-acp", "303MB"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation missing %q\ngot:\n%s", want, view)
		}
	}
}

func TestEnter_ReadyAgentIsSelected(t *testing.T) {
	m := pickerOn(t, agentReady)
	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.agentSelecting {
		t.Error("the picker should close")
	}
	if m.cfg.Agent.Command != "claude" {
		t.Errorf("agent = %q, want claude", m.cfg.Agent.Command)
	}
}

func TestInstallConfirm_Declined(t *testing.T) {
	m := pickerOn(t, agentAdapterMissing)
	m, _ = applyUpdate(m, keyMsg("enter"))
	m, cmd := applyUpdate(m, keyMsg("n"))

	if m.agentInstallConfirm != nil {
		t.Error("declining should close the confirmation")
	}
	if cmd != nil {
		t.Error("nothing should be downloaded")
	}
	if m.agentPendingSelect != nil {
		t.Error("no selection should be pending")
	}
}

func TestInstallConfirm_AcceptedInstallsThenSelects(t *testing.T) {
	m := pickerOn(t, agentAdapterMissing)
	m, _ = applyUpdate(m, keyMsg("enter"))
	m, cmd := applyUpdate(m, keyMsg("y"))

	if cmd == nil {
		t.Fatal("expected the install to run")
	}
	if m.agentPendingSelect == nil {
		t.Fatal("the agent to switch to should be remembered across the install")
	}
	if m.cfg.Agent.Command == "claude" {
		t.Error("the switch should wait until the adapter is actually there")
	}

	// The install finishes; enter meant "use this agent", so finish the job.
	m, _ = applyUpdate(m, adapterInstalledMsg{adapter: "claude-agent-acp"})
	if m.cfg.Agent.Command != "claude" {
		t.Errorf("agent = %q, want claude selected once the adapter landed", m.cfg.Agent.Command)
	}
	if m.agentPendingSelect != nil {
		t.Error("the pending selection should be cleared")
	}
	if !strings.Contains(m.notice, "now using") {
		t.Errorf("notice = %q, want it to say the switch happened", m.notice)
	}
}

func TestInstallFailed_DoesNotSwitchAgent(t *testing.T) {
	m := pickerOn(t, agentAdapterMissing)
	m, _ = applyUpdate(m, keyMsg("enter"))
	m, _ = applyUpdate(m, keyMsg("y"))
	before := m.cfg.Agent.Command

	m, _ = applyUpdate(m, adapterInstalledMsg{adapter: "claude-agent-acp", err: errStr("npm ERR!")})

	if m.cfg.Agent.Command != before {
		t.Errorf("agent = %q, want it unchanged when the adapter never arrived", m.cfg.Agent.Command)
	}
	if m.agentPendingSelect != nil {
		t.Error("the pending selection should not survive a failed install")
	}
}
