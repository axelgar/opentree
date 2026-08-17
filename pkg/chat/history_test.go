package chat

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// The history itself
// ---------------------------------------------------------------------------

func TestHistory_RecordSkipsARepeat(t *testing.T) {
	h := history{}.record("one").record("one").record("two")
	if got, want := len(h.sent), 2; got != want {
		t.Fatalf("sent = %q, want %d messages", h.sent, want)
	}
	if h.walking() {
		t.Error("recording a message should end the walk")
	}
}

func TestHistory_RecordIgnoresNothing(t *testing.T) {
	if h := (history{}.record("")); len(h.sent) != 0 {
		t.Errorf("sent = %q, want an empty history", h.sent)
	}
}

func TestHistory_WalkStopsAtTheOldest(t *testing.T) {
	h := history{}.record("one").record("two")

	h, text, ok := h.walk(-1, "")
	if !ok || text != "two" {
		t.Fatalf("first up = %q, %v; want the newest message", text, ok)
	}
	h, text, ok = h.walk(-1, "")
	if !ok || text != "one" {
		t.Fatalf("second up = %q, %v; want the oldest message", text, ok)
	}
	if _, _, ok = h.walk(-1, ""); ok {
		t.Error("a third up should have nowhere to go")
	}
}

func TestHistory_WalkReturnsTheDraft(t *testing.T) {
	h := history{}.record("one")

	h, _, ok := h.walk(-1, "half typed")
	if !ok {
		t.Fatal("up should recall the one message sent")
	}
	h, text, ok := h.walk(+1, "one")
	if !ok || text != "half typed" {
		t.Fatalf("down = %q, %v; want the draft back", text, ok)
	}
	if h.walking() {
		t.Error("coming back past the newest message should end the walk")
	}
	if _, _, ok := h.walk(+1, "half typed"); ok {
		t.Error("down past the draft should have nowhere to go")
	}
}

func TestHistory_WalkOnAnEmptyHistory(t *testing.T) {
	if _, _, ok := (history{}).walk(-1, "typing"); ok {
		t.Error("up should do nothing before anything has been sent")
	}
}

// ---------------------------------------------------------------------------
// The arrows in the chat
// ---------------------------------------------------------------------------

var (
	up   = tea.KeyMsg{Type: tea.KeyUp}
	down = tea.KeyMsg{Type: tea.KeyDown}
)

// sendMessage types a message and presses enter, which is the only way a
// message reaches the history. The turn it starts is cleared straight away:
// these tests are about the box, not about what the agent said back, and a
// chat with a turn in flight refuses the next enter.
func sendMessage(m Model, text string) Model {
	m = typeInto(m, text)
	m, _ = applyUpdate(m, keyMsg("enter"))
	m.turn = false
	return m
}

func TestRecall_WalksBackAndForward(t *testing.T) {
	m := sendMessage(sendMessage(newTestModel(), "first"), "second")

	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "second" {
		t.Fatalf("after one up the box holds %q, want the last message sent", got)
	}
	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "first" {
		t.Fatalf("after two ups the box holds %q, want the older message", got)
	}
	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "first" {
		t.Fatalf("past the oldest the box holds %q, want it to stay put", got)
	}
	m, _ = applyUpdate(m, down)
	if got := m.input.Value(); got != "second" {
		t.Fatalf("after coming back down the box holds %q, want the newer message", got)
	}
	m, _ = applyUpdate(m, down)
	if got := m.input.Value(); got != "" {
		t.Fatalf("back past the newest the box holds %q, want it empty again", got)
	}
}

func TestRecall_KeepsWhatWasBeingWritten(t *testing.T) {
	m := typeInto(sendMessage(newTestModel(), "sent"), "half typed")

	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "sent" {
		t.Fatalf("up = %q, want the message already sent", got)
	}
	m, _ = applyUpdate(m, down)
	if got := m.input.Value(); got != "half typed" {
		t.Fatalf("down = %q, want the half-typed message back", got)
	}
}

func TestRecall_SendingAgainStartsFromTheNewest(t *testing.T) {
	m := sendMessage(sendMessage(newTestModel(), "first"), "second")

	m, _ = applyUpdate(m, up)
	m, _ = applyUpdate(m, up) // walked back to "first"
	m, _ = applyUpdate(m, keyMsg("enter"))
	m.turn = false
	if got := m.input.Value(); got != "" {
		t.Fatalf("the box holds %q after sending, want it empty", got)
	}
	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "first" {
		t.Fatalf("up = %q, want the message just sent", got)
	}
}

// A message resent unchanged is one press away again rather than two, because
// the history does not file the same message twice in a row.
func TestRecall_ResendingDoesNotDoubleUp(t *testing.T) {
	m := sendMessage(newTestModel(), "again")
	m, _ = applyUpdate(m, up)
	m, _ = applyUpdate(m, keyMsg("enter"))

	m, _ = applyUpdate(m, up)
	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "again" {
		t.Fatalf("two ups = %q, want the one message the history holds", got)
	}
	if got := len(m.history.sent); got != 1 {
		t.Errorf("history holds %d messages, want 1", got)
	}
}

// The arrows only recall from the edges of the message: inside one they move
// the cursor, which is what they are for while a paragraph is being written.
func TestRecall_LeavesTheArrowsToAMultiLineMessage(t *testing.T) {
	m := typeInto(sendMessage(newTestModel(), "sent"), "top")
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = typeInto(m, "bottom")

	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "top\nbottom" {
		t.Fatalf("up from the last row = %q, want the message untouched", got)
	}
	if m.input.Line() != 0 {
		t.Fatalf("up from the last row left the cursor on row %d, want the first", m.input.Line())
	}
	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "sent" {
		t.Fatalf("up from the first row = %q, want the message already sent", got)
	}
}

// Nothing sent yet means nothing to recall, and the arrow is the textarea's as
// it always was.
func TestRecall_DoesNothingWithAnEmptyHistory(t *testing.T) {
	m := typeInto(newTestModel(), "only message")

	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "only message" {
		t.Fatalf("up = %q, want the message untouched", got)
	}
}

// The palette owns the arrows while it is open: they pick a completion, which
// is the nearer of the two things up could mean.
func TestRecall_YieldsToTheCompletionPalette(t *testing.T) {
	m := sendMessage(newPaletteModel(t), "sent")
	m = typeInto(m, "/")
	if !m.completion.active() {
		t.Fatal("expected the command palette to open")
	}

	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "/" {
		t.Fatalf("up with the palette open = %q, want the message untouched", got)
	}
	if m.completion.cursor == 0 {
		t.Error("up with the palette open should have moved its cursor")
	}
}

// A recalled message comes back as it was sent rather than reopening the
// palette on whatever word it ended with.
func TestRecall_DoesNotReopenThePalette(t *testing.T) {
	m := typeInto(newPaletteModel(t), "look at @pkg/auth/session.go")
	m, _ = applyUpdate(m, keyMsg("enter")) // the open palette takes this one
	m, _ = applyUpdate(m, keyMsg("enter"))

	m, _ = applyUpdate(m, up)
	if got := m.input.Value(); got != "look at @pkg/auth/session.go" {
		t.Fatalf("up = %q, want the message as it was sent", got)
	}
	if m.completion.active() {
		t.Error("recalling a message should not open the completion palette")
	}
}
