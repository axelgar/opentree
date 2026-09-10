package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestFindMatches_MeasuresColumnsPastColourAndWidth(t *testing.T) {
	rows := []string{"\x1b[31m日本\x1b[0m needle and NEEDLE"}
	got := FindMatches(rows, "needle")
	if len(got) != 2 {
		t.Fatalf("matches = %+v, want 2", got)
	}
	if got[0].Col != 5 || got[0].Width != 6 || got[1].Col != 16 {
		t.Errorf("matches = %+v, want cols 5 and 16 of width 6", got)
	}
	if FindMatches(rows, "") != nil {
		t.Error("an empty query matched")
	}
}

func TestPaint_KeepsTheTextAndRestylesTheStretch(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	row := "\x1b[31mred\x1b[0m plain tail"
	got := Paint(row, 4, 5, lipgloss.NewStyle().Reverse(true))
	if ansi.Strip(got) != "red plain tail" {
		t.Errorf("text changed: %q", ansi.Strip(got))
	}
	if open, _, _ := cutAround(got, "plain"); open == "" || !containsInverse(open) {
		t.Errorf("the stretch is not in inverse: %q", got)
	}
	if Paint(row, 40, 5, lipgloss.NewStyle()) != row {
		t.Error("a stretch past the end changed the row")
	}

	rows := PaintMatches([]string{"a b a", "b"}, []Match{{0, 0, 1}, {0, 4, 1}}, 1, 0,
		lipgloss.NewStyle().Reverse(true), lipgloss.NewStyle().Underline(true))
	if ansi.Strip(rows[0]) != "a b a" || rows[1] != "b" {
		t.Errorf("rows = %q", rows)
	}
}

func cutAround(s, mid string) (before, at, after string) {
	i := indexOf(s, mid)
	if i < 0 {
		return "", "", ""
	}
	return s[:i], mid, s[i+len(mid):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func containsInverse(s string) bool { return indexOf(s, "\x1b[7m") >= 0 }
