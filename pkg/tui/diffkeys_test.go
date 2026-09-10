package tui

import (
	"strings"
	"testing"
)

func diffModel(lines int) Model {
	m := newTestModel(testWS("a"))
	m.diff = newDiffView(strings.TrimSuffix(strings.Repeat("+line\n", lines), "\n"), "a")
	return m
}

func TestDiffView_PagesAndJumpsToTheEnds(t *testing.T) {
	m := diffModel(200)
	page := m.diffPage()
	if page < minDiffHeight {
		t.Fatalf("page = %d; the test model is too short to page", page)
	}

	m, _ = applyUpdate(m, keyMsg("pgdown"))
	if m.diff.offset != page {
		t.Errorf("pgdown moved to %d, want one page, %d", m.diff.offset, page)
	}
	m, _ = applyUpdate(m, keyMsg("G"))
	if m.diff.offset != m.maxDiffScroll() {
		t.Errorf("G moved to %d, want the end, %d", m.diff.offset, m.maxDiffScroll())
	}
	m, _ = applyUpdate(m, keyMsg("pgup"))
	if m.diff.offset != m.maxDiffScroll()-page {
		t.Errorf("pgup from the end moved to %d, want %d", m.diff.offset, m.maxDiffScroll()-page)
	}
	m, _ = applyUpdate(m, keyMsg("g"))
	if m.diff.offset != 0 {
		t.Errorf("g moved to %d, want the top", m.diff.offset)
	}
	m, _ = applyUpdate(m, keyMsg("pgup"))
	if m.diff.offset != 0 {
		t.Errorf("pgup at the top moved to %d", m.diff.offset)
	}
	if !strings.Contains(m.View(), "? keys") {
		t.Error("the footer does not point at the key card")
	}
}
