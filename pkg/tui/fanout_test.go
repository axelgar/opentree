package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func testFanoutWS(name, group, agent string) WorkspaceItem {
	ws := testWS(name)
	ws.FanoutGroup = group
	ws.Agent = agent
	return ws
}

func TestSortedWorkspaces_KeepsFanoutSiblingsAdjacent(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Interleaved on purpose: each sibling's own creation time, activity and
	// PR state would scatter its group if any mode compared members directly.
	x1 := testFanoutWS("feat/x-claude", "feat/x", "claude")
	x1.CreatedAt = base.Add(4 * time.Hour)
	x1.LastActivity = base.Add(1 * time.Hour)
	x2 := testFanoutWS("feat/x-gemini", "feat/x", "gemini")
	x2.CreatedAt = base.Add(1 * time.Hour)
	x2.LastActivity = base.Add(5 * time.Hour)
	x2.PRStatus = "open"
	y1 := testFanoutWS("fix/y-opencode", "fix/y", "opencode")
	y1.CreatedAt = base.Add(3 * time.Hour)
	y1.LastActivity = base.Add(2 * time.Hour)
	loose := testWS("middle")
	loose.CreatedAt = base.Add(2 * time.Hour)
	loose.LastActivity = base.Add(3 * time.Hour)

	m := newTestModel(x1, loose, x2, y1)

	for mode := range sortModeNames {
		m.sortMode = mode
		sorted := m.sortedWorkspaces()
		positions := map[string]int{}
		for i, ws := range sorted {
			positions[ws.Name] = i
		}
		if d := positions["feat/x-claude"] - positions["feat/x-gemini"]; d != 1 && d != -1 {
			t.Errorf("sort %q: feat/x siblings not adjacent: %v", sortModeNames[mode], positions)
		}
	}

	// The group still competes in each mode as a unit, by its best member:
	// feat/x holds both the newest creation and an open PR, so it leads under
	// both of those modes even though one of its members would not.
	m.sortMode = sortByAge
	if got := m.sortedWorkspaces()[0].FanoutGroup; got != "feat/x" {
		t.Errorf("age sort leads with group %q, want feat/x (holds the newest member)", got)
	}
	m.sortMode = sortByPR
	if got := m.sortedWorkspaces()[0].FanoutGroup; got != "feat/x" {
		t.Errorf("PR sort leads with group %q, want feat/x (holds the open PR)", got)
	}
}

func TestView_ShowsFanoutBadge(t *testing.T) {
	m := newTestModel(testFanoutWS("feat/x-claude", "feat/x", "claude"))
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "⑂ feat/x") {
		t.Error("view should carry the fan-out badge with the group name")
	}
}

func TestCompareKey_RefusesUngroupedRow(t *testing.T) {
	m := newTestModel(testWS("alpha"))

	m, cmd := applyUpdate(m, keyMsg("D"))

	if m.err == nil || !strings.Contains(m.err.Error(), "not part of a fan-out group") {
		t.Errorf("err = %v, want the not-a-group refusal", m.err)
	}
	if cmd == nil {
		t.Error("expected the transient error's clear timer")
	}
	if m.diffViewing {
		t.Error("no diff view should open for an ungrouped row")
	}
}

func TestCompareKey_LoadsTheGroupDiff(t *testing.T) {
	m := newTestModel(
		testFanoutWS("feat/x-claude", "feat/x", "claude"),
		testFanoutWS("feat/x-gemini", "feat/x", "gemini"),
	)

	m, cmd := applyUpdate(m, keyMsg("D"))

	if m.err != nil {
		t.Fatalf("unexpected error: %v", m.err)
	}
	if cmd == nil {
		t.Fatal("expected a command loading the group diff")
	}
}

func TestBuildGroupDiff_SeparatesSiblingsLikeDiffSections(t *testing.T) {
	got := buildGroupDiff([]groupDiffSection{
		{name: "feat/x-claude", agent: "claude", content: "+one\n"},
		{name: "feat/x-gemini", agent: "gemini", content: "(error: worktree missing)"},
	})

	for _, want := range []string{
		"══════════ feat/x-claude (claude) ══════════\n\n+one",
		"══════════ feat/x-gemini (gemini) ══════════\n\n(error: worktree missing)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("group diff missing %q in:\n%s", want, got)
		}
	}
	// Every header line must start with ══, which is all renderDiffLine keys
	// its section styling on.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "feat/x-") && !strings.HasPrefix(line, "══") {
			t.Errorf("header %q would not be styled as a section", line)
		}
	}
}

func TestGroupDiff_OpensInTheExistingViewer(t *testing.T) {
	m := newTestModel(testFanoutWS("feat/x-claude", "feat/x", "claude"))

	m, _ = applyUpdate(m, diffLoadedMsg{content: "══════════ feat/x-claude (claude) ══════════", wsName: "feat/x · 2 siblings"})

	if !m.diffViewing {
		t.Fatal("diff view should open")
	}
	if m.diffWsName != "feat/x · 2 siblings" {
		t.Errorf("diff title = %q, want the group title", m.diffWsName)
	}
}

func TestActionHint_LeadsWithGroupActionsOnSiblings(t *testing.T) {
	ws := testFanoutWS("feat/x-claude", "feat/x", "claude")
	if hint := ws.actionHint(); !strings.HasPrefix(hint, "D compare group • W promote") {
		t.Errorf("hint = %q, want it to lead with the group actions", hint)
	}
	if hint := testWS("alpha").actionHint(); strings.Contains(hint, "compare group") {
		t.Errorf("ungrouped hint = %q, should not mention the group actions", hint)
	}
}

// ---- promote key + dialog ----

func TestPromoteKey_RefusesUngroupedRow(t *testing.T) {
	m := newTestModel(testWS("alpha"))

	m, _ = applyUpdate(m, keyMsg("W"))

	if m.promoting {
		t.Error("no promote dialog should open for an ungrouped row")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "not part of a fan-out group") {
		t.Errorf("err = %v, want the not-a-group refusal", m.err)
	}
}

func TestPromoteKey_OpensConfirmWithLosses(t *testing.T) {
	winner := testFanoutWS("feat/x-claude", "feat/x", "claude")
	dirty := testFanoutWS("feat/x-gemini", "feat/x", "gemini")
	dirty.UncommittedCount = 3
	m := newTestModel(winner, dirty)

	m, _ = applyUpdate(m, keyMsg("W"))

	if !m.promoting || m.promoteWinner != "feat/x-claude" {
		t.Fatalf("promoting = %v winner = %q, want the dialog on feat/x-claude", m.promoting, m.promoteWinner)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Promote") || !strings.Contains(view, "feat/x-gemini") {
		t.Errorf("dialog should name the loser:\n%s", view)
	}
	if !strings.Contains(view, "3 uncommitted files") {
		t.Errorf("dialog should show what the dirty loser loses:\n%s", view)
	}
}

func TestPromoteConfirm_MarksLosersAndCancels(t *testing.T) {
	m := newTestModel(
		testFanoutWS("feat/x-claude", "feat/x", "claude"),
		testFanoutWS("feat/x-gemini", "feat/x", "gemini"),
	)
	m.promoting = true
	m.promoteWinner = "feat/x-claude"

	// esc walks away clean.
	cancelled, _ := applyUpdate(m, keyMsg("esc"))
	if cancelled.promoting || cancelled.promoteWinner != "" {
		t.Error("esc should close the dialog and clear the winner")
	}
	if cancelled.workspaceDeletingNames["feat/x-gemini"] {
		t.Error("esc must not mark anything for deletion")
	}

	// y marks the losers — and only the losers — and fires the command.
	confirmed, cmd := applyUpdate(m, keyMsg("y"))
	if confirmed.promoting {
		t.Error("y should close the dialog")
	}
	if !confirmed.workspaceDeletingNames["feat/x-gemini"] {
		t.Error("the loser should be marked deleting")
	}
	if confirmed.workspaceDeletingNames["feat/x-claude"] {
		t.Error("the winner must not be marked deleting")
	}
	if cmd == nil {
		t.Error("expected the promote command")
	}
}

func TestPromotedMsg_ClearsMarksEvenOnFailure(t *testing.T) {
	m := newTestModel(
		testFanoutWS("feat/x-claude", "feat/x", "claude"),
		testFanoutWS("feat/x-gemini", "feat/x", "gemini"),
		testFanoutWS("feat/x-opencode", "feat/x", "opencode"),
	)
	losers := []string{"feat/x-gemini", "feat/x-opencode"}
	m.markDeleting(losers...)

	// One deleted, one straggler: every mark must clear either way, or the
	// failed row would wear its spinner forever.
	m, _ = applyUpdate(m, promotedWorkspaceMsg{
		winner:  "feat/x-claude",
		deleted: []string{"feat/x-gemini"},
		losers:  losers,
		err:     errTest,
	})

	for _, name := range losers {
		if m.workspaceDeletingNames[name] {
			t.Errorf("%s still marked deleting after the batch ended", name)
		}
	}
	if m.err == nil {
		t.Error("the failure should surface as the banner error")
	}
}

func TestPromotedMsg_Notices(t *testing.T) {
	m := newTestModel(testFanoutWS("feat/x-claude", "feat/x", "claude"))

	m, _ = applyUpdate(m, promotedWorkspaceMsg{winner: "feat/x-claude", deleted: []string{"a", "b"}, losers: []string{"a", "b"}})

	if !strings.Contains(m.notice, "promoted feat/x-claude") {
		t.Errorf("notice = %q, want the promotion named", m.notice)
	}
}

var errTest = errors.New("boom")
