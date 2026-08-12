package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
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

// applyUpdate calls m.Update and casts the result back to Model.
func applyUpdate(m Model, msg tea.Msg) (Model, tea.Cmd) {
	newM, cmd := m.Update(msg)
	return newM.(Model), cmd
}

func TestEscClearsFilterInNormalMode(t *testing.T) {
	m := newTestModel(testWS("alpha"), testWS("beta"))
	m.filterQuery = "alp"

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.filterQuery != "" {
		t.Errorf("filterQuery = %q after esc, want empty", m.filterQuery)
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d after esc, want 0", m.cursor)
	}
}

func TestReviewsSentMsg_Feedback(t *testing.T) {
	m := newTestModel(testWS("alpha"))

	// count == 0 → transient error that auto-clears.
	m, cmd := applyUpdate(m, reviewsSentMsg{wsName: "alpha", count: 0})
	if m.err == nil {
		t.Error("expected error banner for zero review comments")
	}
	if cmd == nil {
		t.Error("expected auto-clear command for zero-count error")
	}

	// count > 0 → transient success notice.
	m.err = nil
	m, cmd = applyUpdate(m, reviewsSentMsg{wsName: "alpha", count: 3})
	if m.err != nil {
		t.Errorf("unexpected error banner: %v", m.err)
	}
	if !strings.Contains(m.notice, "3 review comment") {
		t.Errorf("notice = %q, want mention of 3 review comments", m.notice)
	}
	if cmd == nil {
		t.Error("expected auto-clear command for the notice")
	}

	m, _ = applyUpdate(m, clearNoticeMsg{seq: m.noticeSeq})
	if m.notice != "" {
		t.Errorf("notice = %q after clearNoticeMsg, want empty", m.notice)
	}
}

func TestStatusCheckErrMsg_LogsOnceWithoutBanner(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, statusCheckErrMsg{err: fmt.Errorf("gh pr view failed: auth")})
	m, _ = applyUpdate(m, statusCheckErrMsg{err: fmt.Errorf("gh pr view failed: auth")})

	if m.err != nil {
		t.Errorf("statusCheckErrMsg must not set the transient error banner, got %v", m.err)
	}
	if len(m.errLog) != 1 {
		t.Fatalf("errLog len = %d, want 1 (consecutive duplicates collapse)", len(m.errLog))
	}
	if !strings.Contains(m.errLog[0], "auth") {
		t.Errorf("errLog[0] = %q, want to contain the failure reason", m.errLog[0])
	}
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
	if !strings.Contains(view, "Delete workspace") {
		t.Errorf("View() does not contain 'Delete workspace' header\ngot: %s", view)
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

func TestOpenPR_OKeyWithoutPRURLShowsError(t *testing.T) {
	ws := testWS("no-pr") // no PRURL
	m := newTestModel(ws)

	updated, cmd := applyUpdate(m, keyMsg("o"))

	if cmd == nil {
		t.Error("expected non-nil cmd (transient error) when pressing o on workspace without PR URL")
	}
	if updated.err == nil {
		t.Error("expected transient error to be set")
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

	if !strings.Contains(view, "Workspaces") {
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

// Which agent a worktree runs decides how you talk to it, and the row used to
// be the one place that never said.
func TestView_MainScreen_NamesTheAgent(t *testing.T) {
	claude, opencode := testWS("a"), testWS("b")
	claude.Agent, opencode.Agent = "claude", "opencode"
	view := newTestModel(claude, opencode).View()

	for _, want := range []string{"Claude Code", "OpenCode"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() does not name %q\ngot: %s", want, view)
		}
	}
}

// A workspace created before opentree recorded the agent has no name to show,
// and must not leave a bullet with nothing in front of it.
func TestView_MainScreen_NoAgentRecorded(t *testing.T) {
	if view := newTestModel(testWS("legacy")).View(); strings.Contains(view, "• feature/legacy") {
		t.Errorf("View() left a separator with no agent before it:\n%s", view)
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

	m, _ = applyUpdate(m, clearErrorMsg{seq: m.errSeq})
	if m.err != nil {
		t.Errorf("expected m.err=nil after clearErrorMsg, got %v", m.err)
	}

	// Regression: a stale timer (older seq) must not clear a newer banner.
	m, _ = applyUpdate(m, errMsg{err: fmt.Errorf("newer error")})
	m, _ = applyUpdate(m, clearErrorMsg{seq: m.errSeq - 1})
	if m.err == nil {
		t.Error("stale clearErrorMsg wiped a newer error banner")
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

// ---------------------------------------------------------------------------
// renderDiffLine
// ---------------------------------------------------------------------------

func TestRenderDiffLine_SectionHeader(t *testing.T) {
	line := "══════ Committed Changes ══════"
	result := renderDiffLine(line)
	// Should be styled (non-empty and different from plain text due to ANSI codes)
	if result == "" {
		t.Error("renderDiffLine should return non-empty for section header")
	}
	if !strings.Contains(result, "Committed Changes") {
		t.Errorf("renderDiffLine should preserve section header text, got: %s", result)
	}
}

func TestRenderDiffLine_AddedLine(t *testing.T) {
	result := renderDiffLine("+added line")
	if !strings.Contains(result, "added line") {
		t.Errorf("renderDiffLine should preserve added line text, got: %s", result)
	}
}

func TestRenderDiffLine_RemovedLine(t *testing.T) {
	result := renderDiffLine("-removed line")
	if !strings.Contains(result, "removed line") {
		t.Errorf("renderDiffLine should preserve removed line text, got: %s", result)
	}
}

func TestRenderDiffLine_HunkHeader(t *testing.T) {
	result := renderDiffLine("@@ -1,3 +1,5 @@")
	if !strings.Contains(result, "@@") {
		t.Errorf("renderDiffLine should preserve hunk header, got: %s", result)
	}
}

func TestRenderDiffLine_PlainLine(t *testing.T) {
	line := " context line"
	result := renderDiffLine(line)
	if result != line {
		t.Errorf("renderDiffLine should return plain lines unchanged, got: %q", result)
	}
}

// ---------------------------------------------------------------------------
// Diff scrolling
// ---------------------------------------------------------------------------

func TestDiffScrolling_JScrollsDown(t *testing.T) {
	m := newTestModel()
	// Build diff content with more lines than availHeight (height=40, availHeight=32)
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	m.diffViewing = true
	m.diffContent = strings.Join(lines, "\n")
	m.diffScrollOffset = 0

	m, _ = applyUpdate(m, keyMsg("j"))
	if m.diffScrollOffset != 1 {
		t.Errorf("diffScrollOffset = %d, want 1", m.diffScrollOffset)
	}
}

func TestDiffScrolling_KScrollsUp(t *testing.T) {
	m := newTestModel()
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	m.diffViewing = true
	m.diffContent = strings.Join(lines, "\n")
	m.diffScrollOffset = 5

	m, _ = applyUpdate(m, keyMsg("k"))
	if m.diffScrollOffset != 4 {
		t.Errorf("diffScrollOffset = %d, want 4", m.diffScrollOffset)
	}
}

func TestDiffScrolling_JClampsAtMaxScroll(t *testing.T) {
	m := newTestModel() // height=40, availHeight=32
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	m.diffViewing = true
	m.diffContent = strings.Join(lines, "\n")
	// maxScroll = 50 - 32 = 18
	m.diffScrollOffset = 18

	m, _ = applyUpdate(m, keyMsg("j"))
	if m.diffScrollOffset != 18 {
		t.Errorf("diffScrollOffset = %d after j at maxScroll, want 18 (clamped)", m.diffScrollOffset)
	}
}

func TestDiffScrolling_KClampsAtZero(t *testing.T) {
	m := newTestModel()
	m.diffViewing = true
	m.diffContent = "line 1\nline 2\nline 3"
	m.diffScrollOffset = 0

	m, _ = applyUpdate(m, keyMsg("k"))
	if m.diffScrollOffset != 0 {
		t.Errorf("diffScrollOffset = %d after k at 0, want 0 (clamped)", m.diffScrollOffset)
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

// ---------------------------------------------------------------------------
// Agent status, read from the chat's socket
// ---------------------------------------------------------------------------

// The badge comes from the chat process, which holds the protocol connection
// and therefore knows what the agent is doing rather than inferring it from a
// file's age.
func TestView_ChatBadge_States(t *testing.T) {
	for _, tt := range []struct {
		status *chat.Status
		want   string
	}{
		{&chat.Status{State: chat.StateWorking}, "working"},
		{&chat.Status{State: chat.StateWorking, Tool: "bash"}, "bash"},
		{&chat.Status{State: chat.StateAwaiting, Permission: &chat.Permission{Title: "Run tests?"}}, "PERMISSION"},
		{&chat.Status{State: chat.StateStopped}, "agent stopped"},
		{&chat.Status{State: chat.StateStarting}, "starting"},
		{&chat.Status{State: chat.StateIdle}, "idle"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			ws := testWS("branch-a")
			ws.ChatStatus = tt.status
			view := newTestModel(ws).View()
			if !strings.Contains(view, tt.want) {
				t.Errorf("View() missing %q for state %q\ngot: %s", tt.want, tt.status.State, view)
			}
		})
	}
}

// No chat, no badge. A workspace whose chat window is closed is an ordinary
// thing, not something to invent a status for.
func TestView_NoChat_NoAgentBadge(t *testing.T) {
	view := newTestModel(testWS("branch-a")).View()
	for _, unwanted := range []string{"working", "PERMISSION", "agent stopped"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("View() shows %q for a workspace with no chat\ngot: %s", unwanted, view)
		}
	}
}

func TestView_StatusBar_WaitingCount(t *testing.T) {
	ws1 := testWS("branch-a")
	ws1.ChatStatus = &chat.Status{State: chat.StateAwaiting, Permission: &chat.Permission{Title: "Run tests?"}}
	ws2 := testWS("branch-b")
	ws2.ChatStatus = &chat.Status{State: chat.StateWorking} // working, not counted
	ws3 := testWS("branch-c")                               // no chat, not counted
	m := newTestModel(ws1, ws2, ws3)
	bar := m.statusBar()
	if !strings.Contains(bar, "1 waiting") {
		t.Errorf("statusBar() should show '1 waiting', got: %s", bar)
	}
}

// ---------------------------------------------------------------------------
// Remote branch mode (combobox suggestions)
// ---------------------------------------------------------------------------

func TestFilterBranches_EmptyQueryReturnsAll(t *testing.T) {
	branches := []string{"main", "feat/alpha", "feat/beta", "fix/bug"}
	got := filterBranches(branches, "")
	if len(got) != len(branches) {
		t.Errorf("filterBranches with empty query: got %d, want %d", len(got), len(branches))
	}
}

func TestFilterBranches_CaseInsensitive(t *testing.T) {
	branches := []string{"feat/Alpha", "feat/beta", "Fix/Bug"}
	got := filterBranches(branches, "FEAT")
	if len(got) != 2 {
		t.Errorf("filterBranches('FEAT'): got %d results, want 2; results: %v", len(got), got)
	}
}

func TestFilterBranches_SubstringMatch(t *testing.T) {
	branches := []string{"feat/login", "feat/logout", "fix/crash", "main"}
	got := filterBranches(branches, "log")
	if len(got) != 2 {
		t.Errorf("filterBranches('log'): got %d results, want 2; results: %v", len(got), got)
	}
}

func TestFilterBranches_NoMatch(t *testing.T) {
	branches := []string{"feat/alpha", "fix/bug"}
	got := filterBranches(branches, "zzz")
	if len(got) != 0 {
		t.Errorf("filterBranches('zzz'): got %d results, want 0", len(got))
	}
}

func TestRemoteBranchMode_RKeyEntersMode(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, keyMsg("r"))

	if !m.creating {
		t.Error("expected creating=true after pressing r")
	}
	if !m.remoteBranchMode {
		t.Error("expected remoteBranchMode=true after pressing r")
	}
}

func TestRemoteBranchMode_EscResets(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.remoteBranches = []string{"feat/a", "feat/b"}
	m.filteredBranches = []string{"feat/a", "feat/b"}
	m.branchSuggestionCursor = 1

	m, cmd := applyUpdate(m, keyMsg("esc"))

	if m.creating {
		t.Error("expected creating=false after esc")
	}
	if m.remoteBranchMode {
		t.Error("expected remoteBranchMode=false after esc")
	}
	if len(m.remoteBranches) != 0 {
		t.Errorf("remoteBranches should be cleared after esc, got %v", m.remoteBranches)
	}
	if m.branchSuggestionCursor != 0 {
		t.Errorf("branchSuggestionCursor = %d, want 0 after esc", m.branchSuggestionCursor)
	}
	if cmd != nil {
		t.Error("expected nil cmd after esc")
	}
}

func TestRemoteBranchMode_DownMovescursor(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.filteredBranches = []string{"feat/a", "feat/b", "feat/c"}
	m.branchSuggestionCursor = 0

	m, _ = applyUpdate(m, keyMsg("down"))

	if m.branchSuggestionCursor != 1 {
		t.Errorf("branchSuggestionCursor = %d, want 1 after down", m.branchSuggestionCursor)
	}
}

func TestRemoteBranchMode_UpMovescursor(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.filteredBranches = []string{"feat/a", "feat/b"}
	m.branchSuggestionCursor = 1

	m, _ = applyUpdate(m, keyMsg("up"))

	if m.branchSuggestionCursor != 0 {
		t.Errorf("branchSuggestionCursor = %d, want 0 after up", m.branchSuggestionCursor)
	}
}

func TestRemoteBranchMode_UpClampsAtZero(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.filteredBranches = []string{"feat/a", "feat/b"}
	m.branchSuggestionCursor = 0

	m, _ = applyUpdate(m, keyMsg("up"))

	if m.branchSuggestionCursor != 0 {
		t.Errorf("branchSuggestionCursor = %d, want 0 (clamped)", m.branchSuggestionCursor)
	}
}

func TestRemoteBranchMode_DownClampsAtEnd(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.filteredBranches = []string{"feat/a", "feat/b"}
	m.branchSuggestionCursor = 1

	m, _ = applyUpdate(m, keyMsg("down"))

	if m.branchSuggestionCursor != 1 {
		t.Errorf("branchSuggestionCursor = %d, want 1 (clamped at end)", m.branchSuggestionCursor)
	}
}

func TestRemoteBranchMode_TabSelectsSuggestion(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.remoteBranches = []string{"feat/alpha", "feat/beta"}
	m.filteredBranches = []string{"feat/alpha", "feat/beta"}
	m.branchSuggestionCursor = 1

	m, _ = applyUpdate(m, keyMsg("tab"))

	if m.input.Value() != "feat/beta" {
		t.Errorf("input value after tab = %q, want %q", m.input.Value(), "feat/beta")
	}
	if m.branchSuggestionCursor != 0 {
		t.Errorf("cursor should reset to 0 after tab, got %d", m.branchSuggestionCursor)
	}
}

func TestRemoteBranchMode_EnterWithHighlightedSuggestion(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.filteredBranches = []string{"feat/alpha", "feat/beta"}
	m.branchSuggestionCursor = 1

	m, cmd := applyUpdate(m, keyMsg("enter"))

	if m.creating {
		t.Error("expected creating=false after enter")
	}
	if m.remoteBranchMode {
		t.Error("expected remoteBranchMode=false after enter")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd (createWorkspaceFromRemoteCmd) after enter")
	}
}

func TestRemoteBranchMode_LoadedBranchesMsgSetsFields(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true

	m, _ = applyUpdate(m, remoteBranchesLoadedMsg{branches: []string{"feat/a", "feat/b"}})

	if len(m.remoteBranches) != 2 {
		t.Errorf("remoteBranches len = %d, want 2", len(m.remoteBranches))
	}
	if len(m.filteredBranches) != 2 {
		t.Errorf("filteredBranches len = %d, want 2", len(m.filteredBranches))
	}
}

func TestRemoteBranchMode_ViewShowsTitle(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true

	view := m.View()

	if !strings.Contains(view, "Create Workspace from Remote Branch") {
		t.Errorf("View() does not show remote branch title\ngot: %s", view)
	}
}

func TestRemoteBranchMode_ViewShowsSuggestions(t *testing.T) {
	m := newTestModel()
	m.creating = true
	m.remoteBranchMode = true
	m.filteredBranches = []string{"feat/alpha", "feat/beta"}
	m.branchSuggestionCursor = 0

	view := m.View()

	if !strings.Contains(view, "feat/alpha") {
		t.Errorf("View() does not show suggestion 'feat/alpha'\ngot: %s", view)
	}
	if !strings.Contains(view, "feat/beta") {
		t.Errorf("View() does not show suggestion 'feat/beta'\ngot: %s", view)
	}
	// Highlighted item should have "▶"
	if !strings.Contains(view, "▶") {
		t.Errorf("View() does not show selection indicator '▶'\ngot: %s", view)
	}
}

// ---------------------------------------------------------------------------
// Incremental refresh on create
// ---------------------------------------------------------------------------

// TestCreatedWorkspaceMsg_NoStateStore verifies that sending a createdWorkspaceMsg
// to a model with no stateStore does not panic and does not change the workspace
// list (stateStore lookup is skipped).
func TestCreatedWorkspaceMsg_NoStateStore(t *testing.T) {
	m := newTestModel(testWS("existing"))

	m, _ = applyUpdate(m, createdWorkspaceMsg{wsName: "new-ws", branch: "feature/new-ws", worktreeDir: ""})

	// stateStore is nil — no append should happen
	if len(m.workspaces) != 1 {
		t.Errorf("workspaces len = %d, want 1 (stateStore nil, no append)", len(m.workspaces))
	}
}

// TestCreatedWorkspaceMsg_ClearsCreatingState verifies that the creating spinner
// flags are cleared when a createdWorkspaceMsg is received.
func TestCreatedWorkspaceMsg_ClearsCreatingState(t *testing.T) {
	m := newTestModel()
	m.workspaceCreating = true
	m.workspaceCreatingName = "new-ws"

	m, _ = applyUpdate(m, createdWorkspaceMsg{wsName: "new-ws", branch: "feature/new-ws", worktreeDir: ""})

	if m.workspaceCreating {
		t.Error("workspaceCreating should be false after createdWorkspaceMsg")
	}
	if m.workspaceCreatingName != "" {
		t.Errorf("workspaceCreatingName = %q, want empty", m.workspaceCreatingName)
	}
}

// ---------------------------------------------------------------------------
// Agent selection overlay
// ---------------------------------------------------------------------------

func TestAgentSelection_AKeyEntersMode(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m, _ = applyUpdate(m, keyMsg("A"))

	if !m.agentSelecting {
		t.Error("expected agentSelecting=true after pressing A")
	}
}

func TestAgentSelection_CursorStartsOnActiveAgent(t *testing.T) {
	m := newTestModel(testWS("ws"))
	// Default agent is opencode (index 0)
	m, _ = applyUpdate(m, keyMsg("A"))

	if m.agentCursor != 0 {
		t.Errorf("agentCursor = %d, want 0 (OpenCode is default)", m.agentCursor)
	}
}

func TestAgentSelection_DownMovescursor(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 0

	m, _ = applyUpdate(m, keyMsg("j"))

	if m.agentCursor != 1 {
		t.Errorf("agentCursor = %d, want 1 after down", m.agentCursor)
	}
}

func TestAgentSelection_UpMovescursor(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 1

	m, _ = applyUpdate(m, keyMsg("k"))

	if m.agentCursor != 0 {
		t.Errorf("agentCursor = %d, want 0 after up", m.agentCursor)
	}
}

func TestAgentSelection_UpClampsAtZero(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 0

	m, _ = applyUpdate(m, keyMsg("k"))

	if m.agentCursor != 0 {
		t.Errorf("agentCursor = %d, want 0 (clamped)", m.agentCursor)
	}
}

func TestAgentSelection_EscCancels(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 2

	m, _ = applyUpdate(m, keyMsg("esc"))

	if m.agentSelecting {
		t.Error("expected agentSelecting=false after esc")
	}
}

func TestAgentSelection_EnterSelectsAgent(t *testing.T) {
	// Selecting an agent persists it via config.FindConfigFile(); chdir to a
	// temp dir so the write can't reformat this repo's own opentree.toml.
	t.Chdir(t.TempDir())
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 1 // Claude Code
	// Enter now depends on whether the agent can actually run, which otherwise
	// makes this test agree or disagree based on what is installed here.
	m.agentReadiness = alwaysReady

	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.agentSelecting {
		t.Error("expected agentSelecting=false after enter")
	}
	if m.cfg.Agent.Command != "claude" {
		t.Errorf("cfg.Agent.Command = %q, want %q", m.cfg.Agent.Command, "claude")
	}
}

func alwaysReady(config.PredefinedAgent) (string, bool) { return agentReady, true }

func TestAgentSelection_ViewShowsOverlay(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.agentSelecting = true
	m.agentCursor = 0

	view := m.View()

	if !strings.Contains(view, "Select Agent") {
		t.Errorf("View() does not contain 'Select Agent'\ngot: %s", view)
	}
	if !strings.Contains(view, "OpenCode") {
		t.Errorf("View() does not contain 'OpenCode'\ngot: %s", view)
	}
	if !strings.Contains(view, "Claude Code") {
		t.Errorf("View() does not contain 'Claude Code'\ngot: %s", view)
	}
	if !strings.Contains(view, "(active)") {
		t.Errorf("View() does not show '(active)' for current agent\ngot: %s", view)
	}
	if !strings.Contains(view, "▶") {
		t.Errorf("View() does not show selection indicator\ngot: %s", view)
	}
}

// ---- Async identity regressions ----

// Regression: prContentGeneratedMsg carried no workspace identity, so a slow
// generation for workspace A could open the PR dialog with A's content while
// prWsName pointed at workspace B — creating B's PR with A's title/body.
func TestPRContentGenerated_StaleWorkspaceIgnored(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"))
	m.prGenerating = true
	m.prWsName = "b"

	m, _ = applyUpdate(m, prContentGeneratedMsg{wsName: "a", title: "A title", body: "A body"})

	if m.prCreating {
		t.Error("stale prContentGeneratedMsg for another workspace must not open the PR dialog")
	}
	if !m.prGenerating {
		t.Error("still waiting for workspace b's generation")
	}
}

func TestPRContentGenerated_MatchingWorkspaceOpensDialog(t *testing.T) {
	m := newTestModel(testWS("a"))
	m.prGenerating = true
	m.prWsName = "a"

	m, _ = applyUpdate(m, prContentGeneratedMsg{wsName: "a", title: "T", body: "B"})

	if !m.prCreating || m.prGenerating {
		t.Error("matching prContentGeneratedMsg should open the PR dialog")
	}
	if m.input.Value() != "T" {
		t.Errorf("input = %q, want generated title", m.input.Value())
	}
}

// Regression: keys weren't blocked while the "Generating PR…" screen was up,
// so j/x/enter acted on the invisible list behind it.
func TestPRGenerating_BlocksListKeys(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"))
	m.prGenerating = true
	m.prWsName = "a"

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.deleting {
		t.Error("delete confirmation opened while PR generation screen was up")
	}

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.prGenerating {
		t.Error("esc should cancel PR generation")
	}
}

// Regression: the cursor was a bare index, so a refresh that reordered rows
// pointed destructive keys at whichever workspace moved under the cursor.
func TestLoadedWorkspaces_CursorFollowsWorkspaceName(t *testing.T) {
	m := newTestModel(testWS("alpha"), testWS("beta"), testWS("gamma"))
	m.cursor = 1 // beta (sortByName: alpha, beta, gamma)

	reordered := []WorkspaceItem{testWS("beta"), testWS("gamma"), testWS("alpha")}
	m, _ = applyUpdate(m, loadedWorkspacesMsg{workspaces: reordered})

	if got := m.currentWorkspaceName(); got != "beta" {
		t.Errorf("cursor followed to %q, want to stay on beta", got)
	}
}

// Regression: deletedWorkspaceMsg cleared ALL in-flight delete tracking and
// the whole selection, so overlapping deletes lost their guard and a batch
// confirmed after an unrelated delete finished deleted nothing, silently.
func TestDeletedWorkspace_OnlyClearsFinishedNames(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"), testWS("c"))
	m.markDeleting("a")
	m.markDeleting("b")
	m.selected = map[string]bool{"c": true}

	m, _ = applyUpdate(m, deletedWorkspaceMsg{names: []string{"a"}})

	if !m.isWorkspaceInFlight("b") {
		t.Error("workspace b's in-flight delete tracking was lost")
	}
	if m.isWorkspaceInFlight("a") {
		t.Error("workspace a should no longer be in flight")
	}
	if !m.selected["c"] {
		t.Error("selection of c was wiped by an unrelated delete completing")
	}
	if !m.workspaceDeleting {
		t.Error("spinner flag should stay on while b is still deleting")
	}

	m, _ = applyUpdate(m, deletedWorkspaceMsg{names: []string{"b"}})
	if m.workspaceDeleting {
		t.Error("spinner flag should clear once all deletes finish")
	}
}

// Regression: sort modes had no tie-break, so ties reshuffled randomly on
// every refresh (base order comes from map iteration).
func TestSortedWorkspaces_DeterministicOnTies(t *testing.T) {
	a, b, c := testWS("a"), testWS("b"), testWS("c")
	m := newTestModel(b, c, a)
	m.sortMode = sortByPR // all tie (no PRs)

	first := m.sortedWorkspaces()
	m.workspaces = []WorkspaceItem{c, a, b} // simulate a reshuffled reload
	second := m.sortedWorkspaces()

	for i := range first {
		if first[i].Name != second[i].Name {
			t.Fatalf("tied sort order changed across reloads: %v vs %v", first[i].Name, second[i].Name)
		}
	}
}
