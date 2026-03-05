package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/state"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestModel builds a Model with no external dependencies. Tests that only
// exercise in-process logic (state transitions, View rendering, pure functions)
// should use this instead of NewModel, which requires a real git repo and tmux.
func newTestModel(workspaces ...WorkspaceItem) Model {
	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.CharLimit = 50
	ti.Width = 30
	return Model{
		cfg:        config.Default(),
		input:      ti,
		help:       help.New(),
		keys:       keys,
		workspaces: workspaces,
		width:      120,
		height:     40,
	}
}

func testWS(name string) WorkspaceItem {
	return WorkspaceItem{
		Workspace: &state.Workspace{
			Name:       name,
			Branch:     "feature/" + name,
			BaseBranch: "main",
		},
		DiffStat: "2 files changed",
	}
}

func testWSWithPR(name, prURL string) WorkspaceItem {
	ws := testWS(name)
	ws.PRURL = prURL
	ws.PRStatus = "open"
	return ws
}

func testWSWithWindow(name string) WorkspaceItem {
	ws := testWS(name)
	ws.WindowID = "@1"
	return ws
}

// applyUpdate calls m.Update and casts the result back to Model.
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
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// ---------------------------------------------------------------------------
// 1. Delete confirmation dialog
// ---------------------------------------------------------------------------

func TestDeleteConfirmation_XEntersConfirmMode(t *testing.T) {
	m := newTestModel(testWS("alpha"))
	m, _ = applyUpdate(m, keyMsg("x"))

	if !m.deleting {
		t.Error("expected deleting=true after pressing x")
	}
	if m.deleteTarget != "alpha" {
		t.Errorf("deleteTarget = %q, want %q", m.deleteTarget, "alpha")
	}
}

func TestDeleteConfirmation_XNoOpWhenNoWorkspaces(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, keyMsg("x"))

	if m.deleting {
		t.Error("expected deleting=false when workspace list is empty")
	}
}

func TestDeleteConfirmation_YConfirmsAndResets(t *testing.T) {
	m := newTestModel(testWS("beta"))
	m.deleting = true
	m.deleteTarget = "beta"

	m, cmd := applyUpdate(m, keyMsg("y"))

	if m.deleting {
		t.Error("expected deleting=false after confirming with y")
	}
	if m.deleteTarget != "" {
		t.Errorf("deleteTarget = %q, want empty string", m.deleteTarget)
	}
	// cmd should be non-nil (deleteWorkspaceCmd returned)
	if cmd == nil {
		t.Error("expected non-nil cmd after confirming delete")
	}
}

func TestDeleteConfirmation_UpperYConfirms(t *testing.T) {
	m := newTestModel(testWS("beta"))
	m.deleting = true
	m.deleteTarget = "beta"

	m, cmd := applyUpdate(m, keyMsg("Y"))

	if m.deleting {
		t.Error("expected deleting=false after Y")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd after Y confirmation")
	}
}

func TestDeleteConfirmation_NAbortsDelete(t *testing.T) {
	m := newTestModel(testWS("gamma"))
	m.deleting = true
	m.deleteTarget = "gamma"

	m, cmd := applyUpdate(m, keyMsg("n"))

	if m.deleting {
		t.Error("expected deleting=false after n")
	}
	if m.deleteTarget != "" {
		t.Errorf("deleteTarget = %q, want empty string after abort", m.deleteTarget)
	}
	if cmd != nil {
		t.Error("expected nil cmd after aborting delete")
	}
}

func TestDeleteConfirmation_EscAbortsDelete(t *testing.T) {
	m := newTestModel(testWS("delta"))
	m.deleting = true
	m.deleteTarget = "delta"

	m, cmd := applyUpdate(m, keyMsg("esc"))

	if m.deleting {
		t.Error("expected deleting=false after esc")
	}
	if m.deleteTarget != "" {
		t.Errorf("deleteTarget = %q, want empty after esc", m.deleteTarget)
	}
	if cmd != nil {
		t.Error("expected nil cmd after esc")
	}
}

func TestDeleteConfirmation_UnrelatedKeysIgnored(t *testing.T) {
	m := newTestModel(testWS("epsilon"))
	m.deleting = true
	m.deleteTarget = "epsilon"

	m, cmd := applyUpdate(m, keyMsg("q"))

	// q should not quit while in confirm mode
	if !m.deleting {
		t.Error("expected deleting to remain true when pressing q in confirm mode")
	}
	if cmd != nil {
		t.Error("expected nil cmd for unrelated key in confirm mode")
	}
}

func TestDeleteConfirmation_ViewContainsWorkspaceName(t *testing.T) {
	m := newTestModel(testWS("myfeature"))
	m.deleting = true
	m.deleteTarget = "myfeature"

	view := m.View()

	if !strings.Contains(view, "myfeature") {
		t.Errorf("View() does not contain workspace name %q\ngot: %s", "myfeature", view)
	}
	if !strings.Contains(view, "Delete Workspace") {
		t.Errorf("View() does not contain 'Delete Workspace' header\ngot: %s", view)
	}
}

func TestDeleteConfirmation_ViewContainsConfirmHints(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.deleting = true
	m.deleteTarget = "ws"

	view := m.View()

	if !strings.Contains(view, "confirm") {
		t.Errorf("View() does not contain 'confirm'\ngot: %s", view)
	}
	if !strings.Contains(view, "cancel") {
		t.Errorf("View() does not contain 'cancel'\ngot: %s", view)
	}
}

// ---------------------------------------------------------------------------
// 2. Live agent output preview
// ---------------------------------------------------------------------------

func TestCleanPreview_StripsANSIEscapes(t *testing.T) {
	input := "\x1b[32mHello\x1b[0m World"
	got := cleanPreview(input)
	if strings.Contains(got, "\x1b") {
		t.Errorf("cleanPreview() left ANSI codes: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Errorf("cleanPreview() removed real content: %q", got)
	}
}

func TestCleanPreview_KeepsLast5NonEmptyLines(t *testing.T) {
	lines := []string{"line1", "line2", "line3", "line4", "line5", "line6", "line7"}
	input := strings.Join(lines, "\n")

	got := cleanPreview(input)
	gotLines := strings.Split(got, "\n")

	if len(gotLines) != 5 {
		t.Errorf("cleanPreview() returned %d lines, want 5", len(gotLines))
	}
	if gotLines[0] != "line3" {
		t.Errorf("first line = %q, want %q", gotLines[0], "line3")
	}
	if gotLines[4] != "line7" {
		t.Errorf("last line = %q, want %q", gotLines[4], "line7")
	}
}

func TestCleanPreview_FiltersBlankLines(t *testing.T) {
	input := "real\n   \n\noutput\n\t\n"
	got := cleanPreview(input)
	gotLines := strings.Split(got, "\n")

	for _, l := range gotLines {
		if strings.TrimSpace(l) == "" {
			t.Errorf("cleanPreview() kept a blank line: %q", l)
		}
	}
}

func TestCleanPreview_EmptyInputReturnsEmpty(t *testing.T) {
	if got := cleanPreview(""); got != "" {
		t.Errorf("cleanPreview(\"\") = %q, want empty string", got)
	}
}

func TestCleanPreview_FewerThan5LinesReturnedAsIs(t *testing.T) {
	input := "a\nb\nc"
	got := cleanPreview(input)
	if got != "a\nb\nc" {
		t.Errorf("cleanPreview() = %q, want %q", got, "a\nb\nc")
	}
}

func TestCleanPreview_TrailingSpacesTrimmed(t *testing.T) {
	input := "line1   \nline2\t\t"
	got := cleanPreview(input)
	for _, l := range strings.Split(got, "\n") {
		if l != strings.TrimRight(l, " \t") {
			t.Errorf("line has trailing whitespace: %q", l)
		}
	}
}

func TestCapturePreviewCmd_NilWhenNoWorkspaces(t *testing.T) {
	m := newTestModel()
	cmd := m.capturePreviewCmd()
	if cmd != nil {
		t.Error("capturePreviewCmd() should return nil when workspace list is empty")
	}
}

func TestCapturePreviewCmd_ReturnsEmptyPreviewWhenNoWindow(t *testing.T) {
	ws := testWS("no-window") // WindowID is ""
	m := newTestModel(ws)

	cmd := m.capturePreviewCmd()
	if cmd == nil {
		t.Fatal("capturePreviewCmd() returned nil, want a cmd")
	}
	msg := cmd()
	preview, ok := msg.(capturePreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want capturePreviewMsg", msg)
	}
	if preview.lines != "" {
		t.Errorf("lines = %q, want empty for workspace with no window", preview.lines)
	}
}

func TestCapturePreviewCmd_ReturnsNonNilCmdWhenWindowExists(t *testing.T) {
	// When a window exists, capturePreviewCmd returns a cmd that will call
	// tmuxCtrl.CapturePane. We only verify the cmd is non-nil here since
	// executing it requires a real tmux session.
	ws := testWSWithWindow("active-ws")
	m := newTestModel(ws)

	cmd := m.capturePreviewCmd()
	if cmd == nil {
		t.Error("capturePreviewCmd() returned nil for workspace with a window")
	}
}

func TestAgentPreview_MessageUpdatesModel(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m, _ = applyUpdate(m, capturePreviewMsg{lines: "doing something..."})

	if m.agentPreview != "doing something..." {
		t.Errorf("agentPreview = %q, want %q", m.agentPreview, "doing something...")
	}
}

func TestAgentPreview_ViewShowsPanelWhenNonEmpty(t *testing.T) {
	m := newTestModel(testWSWithWindow("active"))
	m.agentPreview = "Running tests..."

	view := m.View()

	if !strings.Contains(view, "Running tests...") {
		t.Errorf("View() does not contain agent preview content\ngot: %s", view)
	}
	if !strings.Contains(view, "Agent Output") {
		t.Errorf("View() does not contain 'Agent Output' panel title\ngot: %s", view)
	}
}

func TestAgentPreview_ViewHidesPanelWhenEmpty(t *testing.T) {
	m := newTestModel(testWSWithWindow("active"))
	m.agentPreview = "" // explicitly empty

	view := m.View()

	if strings.Contains(view, "Agent Output") {
		t.Errorf("View() should not show preview panel when agentPreview is empty\ngot: %s", view)
	}
}

func TestAgentPreview_CursorUpTriggersCapture(t *testing.T) {
	m := newTestModel(testWS("ws1"), testWS("ws2"))
	m.cursor = 1 // start at second item

	_, cmd := applyUpdate(m, keyMsg("k"))

	if cmd == nil {
		t.Error("expected non-nil cmd (capturePreviewCmd) after cursor move up")
	}
}

func TestAgentPreview_CursorDownTriggersCapture(t *testing.T) {
	m := newTestModel(testWS("ws1"), testWS("ws2"))
	m.cursor = 0 // start at first item

	_, cmd := applyUpdate(m, keyMsg("j"))

	if cmd == nil {
		t.Error("expected non-nil cmd (capturePreviewCmd) after cursor move down")
	}
}

func TestAgentPreview_LoadedWorkspacesTriggerCapture(t *testing.T) {
	m := newTestModel()

	workspaces := []WorkspaceItem{testWS("fresh")}
	_, cmd := applyUpdate(m, loadedWorkspacesMsg{workspaces: workspaces})

	if cmd == nil {
		t.Error("expected non-nil cmd after loadedWorkspacesMsg")
	}
}

func TestAgentPreview_PreviewTickReschedulesAndCaptures(t *testing.T) {
	m := newTestModel(testWSWithWindow("ws"))

	_, cmd := applyUpdate(m, previewTickMsg{})

	if cmd == nil {
		t.Error("expected non-nil batch cmd after previewTickMsg")
	}
}

// ---------------------------------------------------------------------------
// 3. Auto-refresh of workspace status
// ---------------------------------------------------------------------------

func TestAutoRefresh_RefreshTickReturnsNonNilCmd(t *testing.T) {
	m := newTestModel(testWS("ws"))

	_, cmd := applyUpdate(m, refreshTickMsg{})

	if cmd == nil {
		t.Error("expected non-nil cmd (batch with loadWorkspaces + next tick) for refreshTickMsg")
	}
}

func TestAutoRefresh_RefreshTickDoesNotChangeModelState(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"))
	m.cursor = 1

	newM, _ := applyUpdate(m, refreshTickMsg{})

	// State should be unchanged; only cmds are issued
	if newM.cursor != 1 {
		t.Errorf("cursor changed after refreshTickMsg: got %d, want 1", newM.cursor)
	}
	if len(newM.workspaces) != 2 {
		t.Errorf("workspaces changed after refreshTickMsg: got %d, want 2", len(newM.workspaces))
	}
}

func TestAutoRefresh_InitSchedulesBothTicks(t *testing.T) {
	// Init returns a batch that includes both the refresh and preview ticks.
	// We cannot inspect what's inside a tea.Batch, but we can verify Init
	// returns a non-nil cmd (not nil, which would mean "do nothing").
	m := newTestModel()
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() returned nil; expected batch cmd including refresh ticks")
	}
}

// ---------------------------------------------------------------------------
// 4. Two-step create dialog
// ---------------------------------------------------------------------------

func TestCreateDialog_NKeyEntersStep1(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, keyMsg("n"))

	if !m.creating {
		t.Error("expected creating=true after pressing n")
	}
	if m.createStep != 0 {
		t.Errorf("createStep = %d, want 0", m.createStep)
	}
}

func TestCreateDialog_Step1EnterAdvancesToStep2(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 0
	_ = m.input.Focus()
	m.input.SetValue("feat/new")

	m, _ = applyUpdate(m, keyMsg("enter"))

	if !m.creating {
		t.Error("expected creating=true after step 1 enter (should move to step 2)")
	}
	if m.createStep != 1 {
		t.Errorf("createStep = %d, want 1 after step 1 enter", m.createStep)
	}
	if m.newBranchName != "feat/new" {
		t.Errorf("newBranchName = %q, want %q", m.newBranchName, "feat/new")
	}
}

func TestCreateDialog_Step2PrefilledWithConfigDefault(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 0
	_ = m.input.Focus()
	m.input.SetValue("my-feature")

	m, _ = applyUpdate(m, keyMsg("enter"))

	// The input should be pre-filled with cfg.Worktree.DefaultBase ("main")
	if m.input.Value() != m.cfg.Worktree.DefaultBase {
		t.Errorf("input value after step 1 = %q, want default base %q",
			m.input.Value(), m.cfg.Worktree.DefaultBase)
	}
}

func TestCreateDialog_Step2EnterCreatesWorkspace(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 1
	m.newBranchName = "feat/thing"
	_ = m.input.Focus()
	m.input.SetValue("develop")

	m, cmd := applyUpdate(m, keyMsg("enter"))

	if m.creating {
		t.Error("expected creating=false after step 2 enter")
	}
	if m.createStep != 0 {
		t.Errorf("createStep = %d, want 0 after completion", m.createStep)
	}
	if m.newBranchName != "" {
		t.Errorf("newBranchName = %q, want empty after completion", m.newBranchName)
	}
	if m.input.Value() != "" {
		t.Errorf("input value = %q, want empty after completion", m.input.Value())
	}
	// cmd returned is createWorkspaceCmd (non-nil)
	if cmd == nil {
		t.Error("expected non-nil cmd (createWorkspaceCmd) after step 2 enter")
	}
}

func TestCreateDialog_EmptyNameInStep1IsIgnored(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 0
	m.input.SetValue("")

	m, cmd := applyUpdate(m, keyMsg("enter"))

	// Should stay in step 1 and not advance
	if !m.creating {
		t.Error("expected creating=true when submitting empty name")
	}
	if m.createStep != 0 {
		t.Errorf("createStep = %d, want 0 when name is empty", m.createStep)
	}
	if cmd != nil {
		t.Error("expected nil cmd when name is empty")
	}
}

func TestCreateDialog_EscCancelsAtStep1(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 0
	m.input.SetValue("partial-name")

	m, cmd := applyUpdate(m, keyMsg("esc"))

	if m.creating {
		t.Error("expected creating=false after esc")
	}
	if m.createStep != 0 {
		t.Errorf("createStep = %d, want 0 after esc", m.createStep)
	}
	if m.input.Value() != "" {
		t.Errorf("input value = %q, want empty after esc", m.input.Value())
	}
	if cmd != nil {
		t.Error("expected nil cmd after esc")
	}
}

func TestCreateDialog_EscCancelsAtStep2(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 1
	m.newBranchName = "feat/foo"
	m.input.SetValue("develop")

	m, cmd := applyUpdate(m, keyMsg("esc"))

	if m.creating {
		t.Error("expected creating=false after esc at step 2")
	}
	if m.createStep != 0 {
		t.Errorf("createStep = %d, want 0 after esc at step 2", m.createStep)
	}
	if m.newBranchName != "" {
		t.Errorf("newBranchName = %q, want empty after esc at step 2", m.newBranchName)
	}
	if cmd != nil {
		t.Error("expected nil cmd after esc")
	}
}

func TestCreateDialog_ViewShowsStep1Label(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 0

	view := m.View()

	if !strings.Contains(view, "Step 1/2") {
		t.Errorf("View() does not contain 'Step 1/2'\ngot: %s", view)
	}
	if !strings.Contains(view, "Branch name") {
		t.Errorf("View() does not contain 'Branch name' label\ngot: %s", view)
	}
}

func TestCreateDialog_ViewShowsStep2LabelWithBranchName(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.createStep = 1
	m.newBranchName = "feat/awesome"

	view := m.View()

	if !strings.Contains(view, "Step 2/2") {
		t.Errorf("View() does not contain 'Step 2/2'\ngot: %s", view)
	}
	if !strings.Contains(view, "feat/awesome") {
		t.Errorf("View() does not contain branch name %q in step 2 label\ngot: %s",
			"feat/awesome", view)
	}
}

func TestCreateDialog_ViewShowsCreateHeader(t *testing.T) {
	m := newTestModel()
	m.creating = true

	view := m.View()

	if !strings.Contains(view, "Create New Workspace") {
		t.Errorf("View() does not show create header\ngot: %s", view)
	}
}

// ---------------------------------------------------------------------------
// 5. Open PR in browser
// ---------------------------------------------------------------------------

func TestOpenPR_OKeyWithPRURLReturnsCmd(t *testing.T) {
	ws := testWSWithPR("my-feature", "https://github.com/example/repo/pull/42")
	m := newTestModel(ws)

	_, cmd := applyUpdate(m, keyMsg("o"))

	if cmd == nil {
		t.Error("expected non-nil cmd when pressing o on workspace with PR URL")
	}
}

func TestOpenPR_OKeyWithoutPRURLDoesNothing(t *testing.T) {
	ws := testWS("no-pr") // no PRURL
	m := newTestModel(ws)

	_, cmd := applyUpdate(m, keyMsg("o"))

	if cmd != nil {
		t.Error("expected nil cmd when pressing o on workspace without PR URL")
	}
}

func TestOpenPR_OKeyWithEmptyWorkspaceListDoesNothing(t *testing.T) {
	m := newTestModel()

	_, cmd := applyUpdate(m, keyMsg("o"))

	if cmd != nil {
		t.Error("expected nil cmd when pressing o with no workspaces")
	}
}

func TestOpenURL_ReturnsNonNilCmd(t *testing.T) {
	cmd := openURLCmd("https://github.com/example/repo/pull/1")
	if cmd == nil {
		t.Error("openURLCmd() returned nil")
	}
}

func TestOpenURL_CmdDoesNotPanicOnExec(t *testing.T) {
	// openURLCmd uses cmd.Start() (fire-and-forget, ignores errors), so even if
	// xdg-open/open is not available, the returned cmd must not panic.
	cmd := openURLCmd("https://example.com")
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("openURLCmd().exec panicked: %v", r)
		}
	}()
	_ = cmd() // execute the tea.Cmd; ignore the returned tea.Msg (nil)
}

// ---------------------------------------------------------------------------
// General model / view sanity checks
// ---------------------------------------------------------------------------

func TestView_MainScreen_NoWorkspaces(t *testing.T) {
	m := newTestModel()
	view := m.View()

	if !strings.Contains(view, "OpenTree Workspaces") {
		t.Errorf("View() missing title\ngot: %s", view)
	}
	if !strings.Contains(view, "No workspaces") {
		t.Errorf("View() missing empty-state message\ngot: %s", view)
	}
}

func TestView_MainScreen_ShowsWorkspaceName(t *testing.T) {
	m := newTestModel(testWS("feat/login"))
	view := m.View()

	if !strings.Contains(view, "feat/login") {
		t.Errorf("View() does not contain workspace name\ngot: %s", view)
	}
}

func TestView_MainScreen_ShowsMergedBadge(t *testing.T) {
	ws := testWSWithPR("feat/done", "https://github.com/example/repo/pull/1")
	ws.PRStatus = "merged"
	m := newTestModel(ws)

	view := m.View()

	if !strings.Contains(view, "merged") {
		t.Errorf("View() does not show merged badge\ngot: %s", view)
	}
}

func TestView_MainScreen_NormalModeDoesNotShowDeleteOrCreateUI(t *testing.T) {
	m := newTestModel(testWS("ws"))
	view := m.View()

	if strings.Contains(view, "Delete Workspace") {
		t.Errorf("View() shows delete UI in normal mode")
	}
	if strings.Contains(view, "Step 1/2") {
		t.Errorf("View() shows create dialog in normal mode")
	}
}

func TestCursorNavigation_WrapsAtBounds(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"), testWS("c"))

	// Up at top should stay at 0
	m, _ = applyUpdate(m, keyMsg("k"))
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (can't go above 0)", m.cursor)
	}

	// Move to bottom
	m, _ = applyUpdate(m, keyMsg("j"))
	m, _ = applyUpdate(m, keyMsg("j"))
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}

	// Down at bottom should stay at 2
	m, _ = applyUpdate(m, keyMsg("j"))
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (can't go past last item)", m.cursor)
	}
}

func TestCursorClamped_OnLoadedWorkspacesShrink(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"), testWS("c"))
	m.cursor = 2

	// Simulate list shrinking to 1 item
	m, _ = applyUpdate(m, loadedWorkspacesMsg{workspaces: []WorkspaceItem{testWS("a")}})

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after list shrinks below cursor", m.cursor)
	}
}

func TestErrorMessage_DisplayedAndCleared(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, errMsg{err: fmt.Errorf("something went wrong")})

	if m.err == nil {
		t.Error("expected m.err to be set after errMsg")
	}

	view := m.View()
	if !strings.Contains(view, "something went wrong") {
		t.Errorf("View() does not show error message\ngot: %s", view)
	}

	m, _ = applyUpdate(m, clearErrorMsg{})
	if m.err != nil {
		t.Errorf("expected m.err=nil after clearErrorMsg, got %v", m.err)
	}
}

// ---------------------------------------------------------------------------
// Issue badge tests
// ---------------------------------------------------------------------------

func testWSWithIssue(name string, issueNumber int, issueTitle string) WorkspaceItem {
	ws := testWS(name)
	ws.IssueNumber = issueNumber
	ws.IssueTitle = issueTitle
	return ws
}

func TestView_IssueBadge_Shown(t *testing.T) {
	m := newTestModel(testWSWithIssue("my-issue-branch", 42, "Add dark mode"))
	view := m.View()
	if !strings.Contains(view, "#42") {
		t.Errorf("View() should show issue badge '#42'\ngot: %s", view)
	}
}

func TestView_IssueBadge_NotShownWhenNoIssue(t *testing.T) {
	m := newTestModel(testWS("plain-branch"))
	view := m.View()
	// No issue number in state → no badge rendered
	if strings.Contains(view, "#0") {
		t.Errorf("View() should not render '#0' badge for workspaces without an issue\ngot: %s", view)
	}
}

func TestView_IssueBadge_AndPRBadge_BothShown(t *testing.T) {
	ws := testWSWithIssue("combo-branch", 7, "Fix login bug")
	ws.PRStatus = "open"
	ws.PRURL = "https://github.com/owner/repo/pull/1"
	m := newTestModel(ws)
	view := m.View()
	if !strings.Contains(view, "#7") {
		t.Errorf("View() should show issue badge '#7'\ngot: %s", view)
	}
	if !strings.Contains(view, "PR open") {
		t.Errorf("View() should still show 'PR open' badge\ngot: %s", view)
	}
}

func TestView_IssueBadge_MultipleWorkspaces(t *testing.T) {
	m := newTestModel(
		testWSWithIssue("issue-branch", 99, "Refactor auth"),
		testWS("plain-branch"),
	)
	view := m.View()
	if !strings.Contains(view, "#99") {
		t.Errorf("View() should show badge for issue workspace\ngot: %s", view)
	}
}
