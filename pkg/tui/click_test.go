package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func clickAt(y int) tea.MouseMsg {
	return tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// rowY is the screen line of a visible row's first line.
func rowY(t *testing.T, m Model, index int) int {
	t.Helper()
	_, spans := m.listScreen()
	for _, span := range spans {
		if span.index == index {
			return span.top + appStyle.GetPaddingTop()
		}
	}
	t.Fatalf("row %d is not on screen", index)
	return 0
}

func freezeClock(t *testing.T) {
	t.Helper()
	prev := clock
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = prev })
}

func TestClick_SelectsTheRowUnderThePointer(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"), testWS("c"))

	// The second line of a row is still the row.
	m, cmd := applyUpdate(m, clickAt(rowY(t, m, 2)+1))
	if m.cursor != 2 {
		t.Errorf("cursor = %d after a click on the third row, want 2", m.cursor)
	}
	if cmd != nil {
		t.Error("a single click attached")
	}

	m, _ = applyUpdate(m, clickAt(rowY(t, m, 0)))
	if m.cursor != 0 {
		t.Errorf("cursor = %d after a click on the first row, want 0", m.cursor)
	}
}

func TestClick_ADoubleClickAttaches(t *testing.T) {
	freezeClock(t)
	m := newTestModel(testWS("a"), testWS("b"))
	y := rowY(t, m, 1)
	m, _ = applyUpdate(m, clickAt(y))
	_, cmd := applyUpdate(m, clickAt(y))
	if cmd == nil {
		t.Fatal("a double-click did nothing")
	}
}

func TestClick_TwoRowsAreNotADoubleClick(t *testing.T) {
	freezeClock(t)
	m := newTestModel(testWS("a"), testWS("b"))
	m, _ = applyUpdate(m, clickAt(rowY(t, m, 0)))
	m, cmd := applyUpdate(m, clickAt(rowY(t, m, 1)))
	if cmd != nil {
		t.Error("clicks on two different rows counted as a double-click")
	}
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
}

func TestClick_OutsideTheRowsAndInDialogsIsIgnored(t *testing.T) {
	m := newTestModel(testWS("a"), testWS("b"))
	m.cursor = 1
	m, _ = applyUpdate(m, clickAt(0)) // the logo
	if m.cursor != 1 {
		t.Errorf("a click on the logo moved the cursor to %d", m.cursor)
	}
	m.filtering = true
	m, _ = applyUpdate(m, clickAt(rowY(t, m, 0)))
	if m.cursor != 1 {
		t.Errorf("a click under a dialog moved the cursor to %d", m.cursor)
	}
}
