package tui

import (
	"strings"
	"testing"
)

func diffModel(lines int) Model {
	m := newTestModel(testWS("a"))
	m.diffViewing = true
	m.diffWsName = "a"
	m.diffContent = strings.TrimSuffix(strings.Repeat("+line\n", lines), "\n")
	return m
}

func TestDiffView_PagesAndJumpsToTheEnds(t *testing.T) {
	m := diffModel(200)
	page := m.diffPage()
	if page < minDiffHeight {
		t.Fatalf("page = %d; the test model is too short to page", page)
	}

	m, _ = applyUpdate(m, keyMsg("pgdown"))
	if m.diffScrollOffset != page {
		t.Errorf("pgdown moved to %d, want one page, %d", m.diffScrollOffset, page)
	}
	m, _ = applyUpdate(m, keyMsg("G"))
	if m.diffScrollOffset != m.maxDiffScroll() {
		t.Errorf("G moved to %d, want the end, %d", m.diffScrollOffset, m.maxDiffScroll())
	}
	m, _ = applyUpdate(m, keyMsg("pgup"))
	if m.diffScrollOffset != m.maxDiffScroll()-page {
		t.Errorf("pgup from the end moved to %d, want %d", m.diffScrollOffset, m.maxDiffScroll()-page)
	}
	m, _ = applyUpdate(m, keyMsg("g"))
	if m.diffScrollOffset != 0 {
		t.Errorf("g moved to %d, want the top", m.diffScrollOffset)
	}
	m, _ = applyUpdate(m, keyMsg("pgup"))
	if m.diffScrollOffset != 0 {
		t.Errorf("pgup at the top moved to %d", m.diffScrollOffset)
	}
	if !strings.Contains(m.View(), "pgup/pgdn") {
		t.Error("the footer does not name the page keys")
	}
}
