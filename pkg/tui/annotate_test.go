package tui

import (
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/chat"
)

// notedModel is a diff of twoFileDiff open on workspace a, with the cursor on
// row 10, the added "b := 3" (new line 13).
func notedModel(ws ...WorkspaceItem) Model {
	if len(ws) == 0 {
		ws = []WorkspaceItem{testWS("a")}
	}
	m := newTestModel(ws...)
	m.diff = newDiffView(twoFileDiff, ws[0].Name)
	m.diff.cursor = 10
	return m
}

func typeNote(m Model, text string) Model {
	for _, r := range text {
		m, _ = applyUpdate(m, keyMsg(string(r)))
	}
	m, _ = applyUpdate(m, keyMsg("enter"))
	return m
}

func TestAnnotate_EnterCapturesPathLineAndCode(t *testing.T) {
	m := notedModel()
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.diff.noting == nil {
		t.Fatal("enter on a code row did not open the note box")
	}
	if !strings.Contains(m.View(), "pkg/new.go:13 (+)") {
		t.Errorf("the box does not say where the note goes:\n%s", m.View())
	}
	m = typeNote(m, "why 3?")
	if len(m.diff.notes) != 1 {
		t.Fatalf("notes = %+v", m.diff.notes)
	}
	n := m.diff.notes[0]
	if n.path != "pkg/new.go" || n.line != 13 || n.kind != rowAdd || n.code != "\tb := 3" || n.note != "why 3?" || n.row != 10 {
		t.Errorf("note = %+v", n)
	}
	view := m.View()
	if !strings.Contains(view, "●") || !strings.Contains(view, "● why 3?") {
		t.Errorf("the noted row and the footer do not show the note:\n%s", view)
	}
	if !strings.Contains(view, "●1") {
		t.Errorf("the tree does not count the note:\n%s", view)
	}

	// a again edits rather than doubles; emptied, it removes.
	m, _ = applyUpdate(m, keyMsg("a"))
	if m.input.Value() != "why 3?" {
		t.Errorf("the box did not pre-fill the note: %q", m.input.Value())
	}
	m, _ = applyUpdate(m, keyMsg("!"))
	m, _ = applyUpdate(m, keyMsg("enter"))
	if len(m.diff.notes) != 1 || m.diff.notes[0].note != "why 3?!" {
		t.Errorf("edit gave %+v", m.diff.notes)
	}
	m, _ = applyUpdate(m, keyMsg("x"))
	if len(m.diff.notes) != 0 {
		t.Error("x did not delete the note under the cursor")
	}
}

func TestAnnotate_FileLevelHasNoLine(t *testing.T) {
	m := notedModel()
	m, _ = applyUpdate(m, keyMsg("A"))
	m = typeNote(m, "split this file")
	if len(m.diff.notes) != 1 {
		t.Fatalf("notes = %+v", m.diff.notes)
	}
	n := m.diff.notes[0]
	if n.row != -1 || n.line != 0 || n.kind != 'f' || n.path != "pkg/new.go" {
		t.Errorf("file note = %+v", n)
	}
	if n.where() != "pkg/new.go (whole file)" {
		t.Errorf("where = %q", n.where())
	}
}

func TestAnnotate_RefusedOnHunkHeader(t *testing.T) {
	m := notedModel()
	m.diff.cursor = 7 // @@
	m, cmd := applyUpdate(m, keyMsg("a"))
	if m.diff.noting != nil || cmd == nil || m.err == nil || !strings.Contains(m.err.Error(), "line of code") {
		t.Errorf("a on a hunk header: noting=%v err=%v", m.diff.noting, m.err)
	}
	// Esc in the box leaves things as they were.
	m.diff.cursor = 10
	m, _ = applyUpdate(m, keyMsg("a"))
	m, _ = applyUpdate(m, keyMsg("z"))
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.diff.noting != nil || len(m.diff.notes) != 0 || !m.diff.open {
		t.Error("esc in the note box did not just cancel")
	}
}

func TestAnnotate_ListJumpsAndDeletes(t *testing.T) {
	m := notedModel()
	m, _ = applyUpdate(m, keyMsg("k")) // row 9, the removed line
	m, _ = applyUpdate(m, keyMsg("a"))
	m = typeNote(m, "first")
	m, _ = applyUpdate(m, keyMsg("j"))
	m, _ = applyUpdate(m, keyMsg("a"))
	m = typeNote(m, "second")
	m, _ = applyUpdate(m, keyMsg("g"))

	m, _ = applyUpdate(m, keyMsg("@"))
	view := m.View()
	if !m.diff.listing || !strings.Contains(view, "Notes (2)") || !strings.Contains(view, "pkg/new.go:11 (-)") {
		t.Fatalf("the list did not open:\n%s", view)
	}
	m, _ = applyUpdate(m, keyMsg("j"))
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.diff.listing || m.diff.cursor != 10 {
		t.Errorf("enter on the second note: listing=%v cursor=%d, want closed on row 10", m.diff.listing, m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("@"))
	m, _ = applyUpdate(m, keyMsg("x"))
	if len(m.diff.notes) != 1 || m.diff.notes[0].note != "second" {
		t.Errorf("x in the list left %+v", m.diff.notes)
	}
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.diff.listing || !m.diff.open {
		t.Error("esc should close the list, not the diff")
	}
}

func TestFormatAnnotationsPrompt_QuestionsAreAsked(t *testing.T) {
	notes := []annotation{
		{path: "pkg/new.go", line: 13, kind: rowAdd, code: "\tb := 3", note: "why 3?"},
		{path: "pkg/new.go", line: 11, kind: rowDel, code: "\tb := 2", note: "keep this ?? it was right"},
		{path: "README.md", kind: 'f', note: "Mention the flag."},
		{path: "pkg/new.go", line: 12, kind: rowContext, code: "\ta := 1", note: "rename to alpha"},
	}
	got := formatAnnotationsPrompt("feature/a", notes)
	for _, want := range []string{
		"I reviewed the diff on feature/a and left 4 notes. Answer the questions; make the other changes.",
		"1. pkg/new.go:13 (+)\n   > \tb := 3\n   Question: why 3?",
		"2. pkg/new.go:11 (-)\n   > \tb := 2\n   Question: keep this ?? it was right",
		"3. README.md (whole file)\n   Mention the flag.",
		"4. pkg/new.go:12\n   > \ta := 1\n   rename to alpha",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if only := formatAnnotationsPrompt("b", notes[2:3]); !strings.Contains(only, "left 1 note. Please make these changes.") {
		t.Errorf("one statement: %q", only)
	}
	if only := formatAnnotationsPrompt("b", notes[:1]); !strings.Contains(only, "Please answer these questions.") {
		t.Errorf("one question: %q", only)
	}
}

func TestAnnotate_SendUsesChatPromptAndClears(t *testing.T) {
	m := notedModel(wsWithChat("a", &chat.Status{State: chat.StateIdle}))
	m, _ = applyUpdate(m, keyMsg("a"))
	m = typeNote(m, "why?")
	m, cmd := applyUpdate(m, keyMsg("s"))
	if cmd == nil || m.err != nil {
		t.Fatalf("s did not produce the send: cmd=%v err=%v", cmd, m.err)
	}
	if len(m.diff.notes) != 0 || !m.diff.open {
		t.Error("sending should clear the notes and keep the diff open")
	}

	// A stopped agent cannot take the prompt; the notes stay.
	m = notedModel(wsWithChat("a", &chat.Status{State: chat.StateStopped}))
	m, _ = applyUpdate(m, keyMsg("a"))
	m = typeNote(m, "why?")
	m, _ = applyUpdate(m, keyMsg("s"))
	if m.err == nil || !strings.Contains(m.err.Error(), "stopped") || len(m.diff.notes) != 1 {
		t.Errorf("s with a stopped agent: err=%v notes=%d", m.err, len(m.diff.notes))
	}
	if !strings.Contains(m.View(), "stopped") {
		t.Error("the diff's footer does not show the error")
	}

	m = notedModel()
	m, _ = applyUpdate(m, keyMsg("s"))
	if m.err == nil || !strings.Contains(m.err.Error(), "no notes") {
		t.Errorf("s with nothing to send: %v", m.err)
	}
}

func TestAnnotate_EscArmsThenDiscards(t *testing.T) {
	m := notedModel()
	m, _ = applyUpdate(m, keyMsg("a"))
	m = typeNote(m, "note")
	m, _ = applyUpdate(m, keyMsg("esc"))
	if !m.diff.open || !m.diff.closeArmed || !strings.Contains(m.View(), "1 note unsent") {
		t.Fatalf("first esc: open=%v armed=%v\n%s", m.diff.open, m.diff.closeArmed, m.View())
	}
	m, _ = applyUpdate(m, keyMsg("j"))
	if m.diff.closeArmed {
		t.Error("another key should disarm")
	}
	m, _ = applyUpdate(m, keyMsg("esc"))
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.diff.open {
		t.Error("the second esc in a row should close")
	}
	// Without notes, one esc closes as before.
	m = notedModel()
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.diff.open {
		t.Error("esc with no notes should close at once")
	}
}

func TestAnnotate_GroupCompareRefusesSend(t *testing.T) {
	m := newTestModel(testFanoutWS("feat/x-claude", "feat/x", "claude"))
	m.diff = newDiffView(buildGroupDiff([]groupDiffSection{{name: "feat/x-claude", agent: "claude", content: twoFileDiff}}),
		"feat/x · 1 siblings")
	m.diff.cursor = 11
	m, _ = applyUpdate(m, keyMsg("a"))
	m = typeNote(m, "hm")
	m, _ = applyUpdate(m, keyMsg("s"))
	if m.err == nil || !strings.Contains(m.err.Error(), "one workspace") || len(m.diff.notes) != 1 {
		t.Errorf("s from a group compare: err=%v notes=%d", m.err, len(m.diff.notes))
	}
}
