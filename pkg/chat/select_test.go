package chat

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func mouse(action tea.MouseAction, button tea.MouseButton, x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: action, Button: button}
}

func press(x, y int) tea.MouseMsg   { return mouse(tea.MouseActionPress, tea.MouseButtonLeft, x, y) }
func drag(x, y int) tea.MouseMsg    { return mouse(tea.MouseActionMotion, tea.MouseButtonLeft, x, y) }
func release(x, y int) tea.MouseMsg { return mouse(tea.MouseActionRelease, tea.MouseButtonLeft, x, y) }

// replyModel is a chat showing one two-line reply. Rendered, its rows are
// "◆ alpha beta gamma" and "  delta": the agent's mark, a space, the text.
func replyModel() Model {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate("agent_message_chunk", "alpha beta gamma\ndelta"))
	return m
}

// freezeClock pins the click counter's clock so two presses land in no time.
func freezeClock(t *testing.T) {
	t.Helper()
	prev := now
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return at }
	t.Cleanup(func() { now = prev })
}

func TestSelect_DragCopiesTheRowsUnderTheHighlight(t *testing.T) {
	got := captureClipboard(t)
	m := replyModel()
	if !strings.HasPrefix(m.logLines[0], "◆ alpha") || m.logLines[1] != "  delta" {
		t.Fatalf("the log is not laid out the way the test assumes: %q", m.logLines[:2])
	}

	// From the a of alpha on the first row to the a of delta on the second.
	m, _ = applyUpdate(m, press(2, headerHeight))
	m, _ = applyUpdate(m, drag(6, headerHeight+1))
	m, cmd := applyUpdate(m, release(6, headerHeight+1))
	if cmd == nil {
		t.Fatal("releasing a drag copied nothing")
	}
	msg := cmd()
	if want := "alpha beta gamma\n  delta"; *got != want {
		t.Errorf("clipboard got %q, want %q", *got, want)
	}
	m, _ = applyUpdate(m, msg)
	if !strings.Contains(m.View(), "copied 2 lines") {
		t.Errorf("no receipt on the status line:\n%s", m.View())
	}
}

func TestSelect_ABackwardsDragIsTheSameSelection(t *testing.T) {
	got := captureClipboard(t)
	m := replyModel()
	m, _ = applyUpdate(m, press(6, headerHeight+1))
	m, _ = applyUpdate(m, drag(2, headerHeight))
	_, cmd := applyUpdate(m, release(2, headerHeight))
	cmd()
	if want := "alpha beta gamma\n  delta"; *got != want {
		t.Errorf("clipboard got %q, want %q", *got, want)
	}
}

func TestSelect_TheHighlightIsDrawnWhileDragging(t *testing.T) {
	withColour(t)

	m := replyModel()
	m, _ = applyUpdate(m, press(2, headerHeight))
	m, _ = applyUpdate(m, drag(6, headerHeight))
	view := m.viewport.View()
	if !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("no inverse video in the log while dragging:\n%q", view)
	}
	// The rows still read the same: painting the highlight must not lose or
	// move a character.
	if plain := stripView(view); !strings.Contains(plain, "◆ alpha beta gamma") {
		t.Errorf("the painted row changed its text:\n%q", plain)
	}

	m, _ = applyUpdate(m, release(6, headerHeight))
	m, _ = applyUpdate(m, keyMsg("x"))
	if strings.Contains(m.viewport.View(), "\x1b[7m") {
		t.Error("a key did not clear the highlight")
	}
}

func TestSelect_AClickCopiesNothing(t *testing.T) {
	got := captureClipboard(t)
	*got = "untouched"
	m := replyModel()
	m, _ = applyUpdate(m, press(4, headerHeight))
	m, cmd := applyUpdate(m, release(4, headerHeight))
	if cmd != nil {
		t.Error("a click without a drag produced a copy command")
	}
	if *got != "untouched" {
		t.Errorf("clipboard got %q, want it left alone", *got)
	}
	if m.sel.on {
		t.Error("a click left a highlight behind")
	}
}

func TestSelect_DoubleClickTakesTheWord(t *testing.T) {
	freezeClock(t)
	got := captureClipboard(t)
	m := replyModel()
	// The e of beta.
	m, _ = applyUpdate(m, press(9, headerHeight))
	m, _ = applyUpdate(m, release(9, headerHeight))
	m, _ = applyUpdate(m, press(9, headerHeight))
	_, cmd := applyUpdate(m, release(9, headerHeight))
	if cmd == nil {
		t.Fatal("a double-click copied nothing")
	}
	cmd()
	if *got != "beta" {
		t.Errorf("clipboard got %q, want the word under the pointer", *got)
	}
}

func TestSelect_TripleClickTakesTheRow(t *testing.T) {
	freezeClock(t)
	got := captureClipboard(t)
	m := replyModel()
	for range 2 {
		m, _ = applyUpdate(m, press(9, headerHeight))
		m, _ = applyUpdate(m, release(9, headerHeight))
	}
	m, _ = applyUpdate(m, press(9, headerHeight))
	_, cmd := applyUpdate(m, release(9, headerHeight))
	cmd()
	if *got != "◆ alpha beta gamma" {
		t.Errorf("clipboard got %q, want the whole row", *got)
	}
}

func TestSelect_APressOutsideTheLogSelectsNothing(t *testing.T) {
	m := replyModel()
	m, _ = applyUpdate(m, press(2, 0)) // the header
	m, _ = applyUpdate(m, drag(6, headerHeight+1))
	_, cmd := applyUpdate(m, release(6, headerHeight+1))
	if cmd != nil || m.sel.on {
		t.Error("a drag that started on the header selected text")
	}
}

func TestSelect_TheWheelStillScrolls(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate("agent_message_chunk", strings.Repeat("line\n", 80)))
	if m.viewport.YOffset == 0 {
		t.Fatal("the log did not scroll; nothing to test")
	}
	before := m.viewport.YOffset
	m, _ = applyUpdate(m, mouse(tea.MouseActionPress, tea.MouseButtonWheelUp, 5, headerHeight+3))
	if m.viewport.YOffset >= before {
		t.Errorf("YOffset = %d after the wheel, want less than %d", m.viewport.YOffset, before)
	}
	if m.sel.on || m.sel.active {
		t.Error("the wheel started a selection")
	}
}

func TestWordBounds(t *testing.T) {
	row := "\x1b[31m✓\x1b[0m pkg/auth/login.go:14: \"quoted\" 日本語 end"
	cases := []struct {
		col         int
		first, last int
	}{
		{0, 0, 0},    // the mark, alone
		{1, 1, 1},    // a space, alone
		{5, 2, 22},   // inside the path: whole path, colon included
		{25, 25, 30}, // inside "quoted": without its quotes
		{33, 33, 38}, // a wide word: three runes, six cells
		{99, 99, 99}, // past the end
	}
	for _, c := range cases {
		first, last := wordBounds(row, c.col)
		if first != c.first || last != c.last {
			t.Errorf("wordBounds(col %d) = %d..%d, want %d..%d", c.col, first, last, c.first, c.last)
		}
	}
}

// stripView is a rendered view without its escape codes.
func stripView(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && (r == 'm' || r == 'K'):
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
