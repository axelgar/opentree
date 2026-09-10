package ui

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestHighlight_KnowsGoAndDeclinesTheUnknown(t *testing.T) {
	if Highlight("x := 1", lexers.Get("go")) == nil {
		t.Error("chroma knows go; the block fell back to plain")
	}
	if Highlight("x := 1", nil) != nil {
		t.Error("no lexer should mean plain, not a guess")
	}
}

// A string opened on one line is still a string on the next: the lines come
// back split, but they were lexed together.
func TestHighlight_StateCrossesLines(t *testing.T) {
	lines := Highlight("s := `one\ntwo\nthree`", lexers.Get("go"))
	if len(lines) < 3 {
		t.Fatalf("%d lines back, want 3", len(lines))
	}
	if len(lines[1]) != 1 || lines[1][0].Kind != KindString || lines[1][0].Text != "two" {
		t.Errorf("the middle of a raw string came back as %+v", lines[1])
	}
}

// The mapping is coarse on purpose; what matters is that the five categories
// land on their five kinds and everything structural lands on plain.
func TestKindOf_MapsTheFiveCategories(t *testing.T) {
	for _, tt := range []struct {
		tok  chroma.TokenType
		want SynKind
	}{
		{chroma.Keyword, KindKeyword},
		{chroma.KeywordType, KindKeyword},
		{chroma.LiteralString, KindString},
		{chroma.LiteralNumberInteger, KindNumber},
		{chroma.Comment, KindComment},
		{chroma.CommentSingle, KindComment},
		{chroma.NameFunction, KindName},
		{chroma.Name, KindPlain},
		{chroma.Punctuation, KindPlain},
	} {
		if got := KindOf(tt.tok); got != tt.want {
			t.Errorf("KindOf(%v) = %d, want %d", tt.tok, got, tt.want)
		}
	}
}
