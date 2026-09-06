package tui

import (
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/workspace"
	"github.com/axelgar/opentree/pkg/worktree"
)

func TestSync_ConflictsOpenADialogThatHandsThemToTheAgent(t *testing.T) {
	m := newTestModel(testWS("a"))
	res := worktree.SyncResult{Ref: "origin/main", Conflicts: []string{"auth.go", "login.go"}}
	m, _ = applyUpdate(m, syncedMsg{wsName: "a", branch: "feature/a", res: res})
	if m.syncConflict == nil {
		t.Fatal("conflicts did not open the dialog")
	}
	view := m.View()
	for _, want := range []string{"Conflicts in a", "auth.go", "login.go", "2 conflicts", "ask the agent"} {
		if !strings.Contains(view, want) {
			t.Errorf("dialog is missing %q:\n%s", want, view)
		}
	}
	if !m.busyWithDialog() {
		t.Error("the dialog does not count as one")
	}

	m, cmd := applyUpdate(m, keyMsg("y"))
	if m.syncConflict != nil {
		t.Error("y left the dialog open")
	}
	if cmd == nil {
		t.Error("y did not hand the conflicts to the agent")
	}
}

func TestSync_TheDialogCanBeLeft(t *testing.T) {
	m := newTestModel(testWS("a"))
	res := worktree.SyncResult{Ref: "main", Conflicts: []string{"auth.go"}}
	m, _ = applyUpdate(m, syncedMsg{wsName: "a", branch: "feature/a", res: res})
	m, cmd := applyUpdate(m, keyMsg("n"))
	if m.syncConflict != nil || cmd != nil {
		t.Error("n did not simply close the dialog")
	}
}

func TestSync_AMergeWithoutConflictsIsANotice(t *testing.T) {
	m := newTestModel(testWS("a"))
	_, cmd := applyUpdate(m, syncedMsg{wsName: "a", branch: "feature/a", res: worktree.SyncResult{Ref: "origin/main", Updated: true}})
	if cmd == nil {
		t.Error("a merge that moved the branch said nothing")
	}
	if m.syncConflict != nil {
		t.Error("a clean merge opened the conflicts dialog")
	}
}

func TestSyncConflictPrompt(t *testing.T) {
	// The prompt lives beside the service; checked here because this is
	// where the dialog that sends it is.
	res := worktree.SyncResult{Ref: "origin/main", Conflicts: []string{"a.go"}}
	got := workspace.SyncConflictPrompt("feature/a", res)
	for _, want := range []string{"origin/main", "feature/a", "- a.go", "git commit"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}
}
