package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The tests here assert structure — what got stripped, what got kept, where
// the lines fold — rather than escape sequences, because the test binary runs
// without a terminal and lipgloss strips colour for it. That is also why they
// are trustworthy: the structure is the part that must not depend on theme.

func mdLines(text string, width int) []string {
	return strings.Split(renderMarkdown(text, width), "\n")
}

func TestMarkdown_StripsInlineMarkers(t *testing.T) {
	for _, tt := range []struct{ in, want, gone string }{
		{"this is **bold** text", "this is bold text", "**"},
		{"this is *leaning* text", "this is leaning text", "*"},
		{"this is _leaning_ text", "this is leaning text", "_"},
		{"this is ***both*** text", "this is both text", "*"},
		{"run `make check` now", "run  make check  now", "`"},
		{"# A Heading", "A Heading", "#"},
	} {
		got := renderMarkdown(tt.in, 80)
		if got != tt.want {
			t.Errorf("renderMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.Contains(got, tt.gone) {
			t.Errorf("renderMarkdown(%q) kept the %q marker: %q", tt.in, tt.gone, got)
		}
	}
}

func TestMarkdown_UnmatchedMarkersStayLiteral(t *testing.T) {
	for _, in := range []string{
		"2 * 3 * 4 is 24",        // spaced stars are arithmetic, not emphasis
		"an open **bold arrives", // the closer has not streamed in yet
		"a stray ` backtick",
		"#hashtag, not a heading",
		"1000 items",
	} {
		if got := renderMarkdown(in, 80); got != in {
			t.Errorf("renderMarkdown(%q) = %q, want it untouched", in, got)
		}
	}
}

func TestMarkdown_FencedBlockDropsTheFenceAndPads(t *testing.T) {
	got := mdLines("before\n```go\nx := 1\n```\nafter", 20)
	want := []string{"before", " x := 1" + strings.Repeat(" ", 13), "after"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("line %d = %q, want %q (all: %#v)", i, got[i], w, got)
		}
	}
}

// An unterminated fence stays an open code block: lines already painted as
// code must not snap back to prose when the closer has simply not arrived yet.
func TestMarkdown_UnterminatedFenceStaysCode(t *testing.T) {
	got := mdLines("```\nfor i := range 3 {", 40)
	if len(got) != 1 || !strings.HasPrefix(got[0], " for i := range 3 {") {
		t.Fatalf("lines = %#v, want the half-streamed line set as code", got)
	}
	if w := lipgloss.Width(got[0]); w != 40 {
		t.Errorf("code line is %d cells, want padded to 40", w)
	}
}

// Code is truncated, never wrapped: rewrapping code makes its indentation lie.
func TestMarkdown_CodeLinesTruncateAtTheColumn(t *testing.T) {
	long := "```\n" + strings.Repeat("abc", 40) + "\n```"
	for _, line := range mdLines(long, 30) {
		if w := lipgloss.Width(line); w > 30 {
			t.Fatalf("code line is %d cells wide at width 30", w)
		}
	}
}

func TestMarkdown_ListItemsHangTheirIndent(t *testing.T) {
	got := mdLines("- a bullet that is long enough to wrap onto another line", 30)
	if !strings.HasPrefix(got[0], "• a bullet") {
		t.Fatalf("first line = %q, want a • marker", got[0])
	}
	if len(got) < 2 || !strings.HasPrefix(got[1], "  ") {
		t.Fatalf("continuation = %#v, want it hung under the text", got)
	}

	got = mdLines("2. numbered", 30)
	if got[0] != "2. numbered" {
		t.Fatalf("ordered item = %q, want the number kept", got[0])
	}
}

func TestMarkdown_BlockquoteCarriesABar(t *testing.T) {
	got := mdLines("> quoted words", 40)
	if !strings.HasPrefix(got[0], "▏ quoted words") {
		t.Fatalf("quote = %q, want a bar before the words", got[0])
	}
}

func TestMarkdown_RuleSpansTheColumn(t *testing.T) {
	got := renderMarkdown("---", 24)
	if got != strings.Repeat("─", 24) {
		t.Fatalf("rule = %q, want 24 cells of line", got)
	}
}

func TestMarkdown_ProseWrapsToTheColumn(t *testing.T) {
	long := strings.Repeat("some words in a paragraph ", 8)
	for _, line := range mdLines(long, 36) {
		if w := lipgloss.Width(line); w > 36 {
			t.Fatalf("prose line is %d cells wide at width 36", w)
		}
	}
}

// The renderer is what streaming re-runs, so a prefix of the text must render
// a prefix of the result — this is the no-flicker property in one assertion.
func TestMarkdown_PrefixStableWhileStreaming(t *testing.T) {
	full := "some prose\n\n```go\nx := 1\ny := 2\n```\n- a list\n"
	all := renderMarkdown(full, 40)
	for cut := range full {
		if !strings.HasSuffix(full[:cut], "\n") {
			continue // mid-line prefixes may restyle that line; whole lines may not
		}
		// The last rendered line is the one still streaming — a source line
		// split at "\n" leaves a trailing "" that the next chunk will grow —
		// so stability is asserted for everything above it.
		lines := mdLines(full[:cut], 40)
		settled := strings.Join(lines[:len(lines)-1], "\n")
		if settled != "" && !strings.HasPrefix(all, settled) {
			t.Fatalf("rendering the first %d bytes is not a prefix of rendering it all:\n%q", cut, settled)
		}
	}
}
