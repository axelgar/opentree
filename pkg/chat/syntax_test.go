package chat

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/lipgloss"
)

func TestHighlight_KnowsGoAndDeclinesTheUnknown(t *testing.T) {
	if highlight("x := 1", "go") == nil {
		t.Error("chroma knows go; the block fell back to plain")
	}
	for _, lang := range []string{"", "notalanguage"} {
		if highlight("x := 1", lang) != nil {
			t.Errorf("lang %q should fall back to plain, not guess", lang)
		}
	}
}

// The mapping is coarse on purpose; what matters is that the five categories
// land on their five styles and everything structural lands on the base.
func TestTokenStyle_MapsTheFiveCategories(t *testing.T) {
	for _, tt := range []struct {
		tok  chroma.TokenType
		want lipgloss.Style
	}{
		{chroma.Keyword, mdSynKeywordStyle},
		{chroma.KeywordType, mdSynKeywordStyle},
		{chroma.LiteralString, mdSynStringStyle},
		{chroma.LiteralNumberInteger, mdSynNumberStyle},
		{chroma.Comment, mdSynCommentStyle},
		{chroma.CommentSingle, mdSynCommentStyle},
		{chroma.NameFunction, mdSynNameStyle},
		{chroma.Name, mdCodeBlockStyle},
		{chroma.Punctuation, mdCodeBlockStyle},
	} {
		if got := tokenStyle(tt.tok); got.GetForeground() != tt.want.GetForeground() {
			t.Errorf("tokenStyle(%v) painted with the wrong colour", tt.tok)
		}
	}
}

// Streaming hands the highlighter half-written code on every chunk. Whatever
// the lexer makes of it, the lines must come back whole and at width.
func TestRenderCodeBlock_HalfWrittenCodeKeepsItsShape(t *testing.T) {
	lines := []string{"func main() {", `	fmt.Println("unterminated`}
	got := renderCodeBlock(lines, "go", 30)
	if len(got) != len(lines) {
		t.Fatalf("%d lines in, %d lines out", len(lines), len(got))
	}
	for i, l := range got {
		if w := lipgloss.Width(l); w != 30 {
			t.Errorf("line %d is %d cells, want padded to 30", i, w)
		}
	}
	if !strings.Contains(got[0], "func main() {") {
		t.Errorf("the code itself went missing: %q", got[0])
	}
}

// A highlighted block and a plain one must agree on the text; colour is the
// only thing the language name may change.
func TestRenderCodeBlock_HighlightingChangesNothingButPaint(t *testing.T) {
	lines := []string{"if err != nil {", "	return err", "}"}
	lit := renderCodeBlock(lines, "go", 40)
	plain := renderCodeBlock(lines, "", 40)
	if len(lit) != len(plain) {
		t.Fatalf("highlighted %d lines, plain %d", len(lit), len(plain))
	}
	// The test binary has no terminal, so both render unstyled — which is the
	// equality that matters: same cells, same places.
	for i := range lit {
		if lit[i] != plain[i] {
			t.Errorf("line %d differs:\n  lit:   %q\n  plain: %q", i, lit[i], plain[i])
		}
	}
}
