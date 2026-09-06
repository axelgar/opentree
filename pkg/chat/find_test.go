package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func ctrlF() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlF} }
func ctrlN() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlN} }
func ctrlP() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlP} }

func typed(text string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)} }

// longLog is a chat with a reply of eighty lines, "needle" on the tenth and
// the fortieth — both above what the last screen of the log shows, and far
// enough apart that they cannot share one.
func longLog() Model {
	m := newTestModel()
	var lines []string
	for i := range 80 {
		line := "line"
		if i == 10 || i == 40 {
			line = "the Needle here"
		}
		lines = append(lines, line)
	}
	m, _ = applyUpdate(m, textUpdate("agent_message_chunk", strings.Join(lines, "\n")))
	return m
}

func lineOf(m Model, text string, nth int) int {
	seen := 0
	for i, row := range m.logLines {
		if strings.Contains(row, text) {
			if seen == nth {
				return i
			}
			seen++
		}
	}
	return -1
}

func onScreen(m Model, line int) bool {
	return line >= m.viewport.YOffset && line < m.viewport.YOffset+m.viewport.Height
}

func TestFind_TypingScrollsToTheFirstMatchBelowAndStepsThrough(t *testing.T) {
	m := longLog()
	if !m.viewport.AtBottom() {
		t.Fatal("the log should open at its end")
	}
	first, second := lineOf(m, "Needle", 0), lineOf(m, "Needle", 1)

	m, _ = applyUpdate(m, ctrlF())
	m, _ = applyUpdate(m, typed("needle"))
	if !strings.Contains(m.View(), "1 of 2") {
		t.Fatalf("the box does not count the matches:\n%s", m.View())
	}
	// Case-folded, and from where the reader stood — the end, with nothing
	// below it — wrapping to the first.
	if !onScreen(m, first) {
		t.Errorf("the first match (row %d) is not on screen; top is %d", first, m.viewport.YOffset)
	}

	m, _ = applyUpdate(m, ctrlN())
	if !strings.Contains(m.View(), "2 of 2") || !onScreen(m, second) {
		t.Errorf("ctrl+n did not step to the second match:\n%s", m.View())
	}
	m, _ = applyUpdate(m, ctrlN())
	if !strings.Contains(m.View(), "1 of 2") {
		t.Error("ctrl+n past the last did not wrap")
	}
	m, _ = applyUpdate(m, ctrlP())
	if !strings.Contains(m.View(), "2 of 2") {
		t.Error("ctrl+p did not step back")
	}
}

func TestFind_EscLeavesTheLogWhereItStands(t *testing.T) {
	m := longLog()
	m, _ = applyUpdate(m, ctrlF())
	m, _ = applyUpdate(m, typed("needle"))
	top := m.viewport.YOffset
	if m.viewport.AtBottom() {
		t.Fatal("the match did not move the log; nothing to hold")
	}

	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.finding.open {
		t.Fatal("esc left the box open")
	}
	if m.viewport.YOffset != top {
		t.Errorf("esc moved the log from %d to %d", top, m.viewport.YOffset)
	}
	if strings.Contains(m.View(), "find ›") {
		t.Error("the box is still drawn")
	}
}

func TestFind_NoMatchesSaysSo(t *testing.T) {
	m := longLog()
	m, _ = applyUpdate(m, ctrlF())
	m, _ = applyUpdate(m, typed("haystack"))
	if !strings.Contains(m.View(), "no matches") {
		t.Errorf("the box did not say there is nothing:\n%s", m.View())
	}
	// Backspace narrows the query and finds again.
	for range len("haystack") {
		m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m, _ = applyUpdate(m, typed("nee"))
	if !strings.Contains(m.View(), "of 2") {
		t.Errorf("a shorter query did not find again:\n%s", m.View())
	}
}

func TestFind_MatchesAreMarked(t *testing.T) {
	withColour(t)
	m := longLog()
	m, _ = applyUpdate(m, ctrlF())
	m, _ = applyUpdate(m, typed("needle"))
	if !strings.Contains(m.viewport.View(), "\x1b[7m") {
		t.Error("the current match is not painted in inverse")
	}
}

func TestFind_DoesNotDisturbTheDraft(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate("agent_message_chunk", "hello there"))
	m, _ = applyUpdate(m, typed("half a message"))
	m, _ = applyUpdate(m, ctrlF())
	m, _ = applyUpdate(m, typed("hello"))
	m, _ = applyUpdate(m, keyMsg("esc"))
	if got := m.input.Value(); got != "half a message" {
		t.Errorf("the draft became %q", got)
	}
}

func TestFindMatches_MeasuresColumnsPastColourAndWidth(t *testing.T) {
	rows := []string{"\x1b[31m日本\x1b[0m needle and NEEDLE"}
	got := findMatches(rows, "needle")
	if len(got) != 2 {
		t.Fatalf("matches = %+v, want 2", got)
	}
	if got[0].col != 5 || got[0].width != 6 || got[1].col != 16 {
		t.Errorf("matches = %+v, want cols 5 and 16 of width 6", got)
	}
}
