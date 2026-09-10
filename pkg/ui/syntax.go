package ui

// Syntax colour, as far as both programs agree on it: chroma lexes, and the
// tokens come back as spans of five kinds. Chroma's own formatters and themes
// pick colours with no idea whether the terminal is light or dark, which is
// the exact mistake the palette exists to prevent; so the painting is the
// caller's, in its own styles, and only the lexing is shared.
//
// Five kinds is deliberate: keywords, strings, numbers, comments and the names
// of things read as highlighted code. Twenty read as a ransom note.

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// SynKind is what a span of code is, coarsely.
type SynKind uint8

const (
	KindPlain SynKind = iota
	KindKeyword
	KindString
	KindComment
	KindNumber
	KindName
)

// Span is a run of code of one kind.
type Span struct {
	Text string
	Kind SynKind
}

// Highlight lexes code and returns it as spans per line, or nil when there
// is no lexer — the caller falls back to plain code, which is also what a
// lexing error degrades to. The code is given to chroma whole and its tokens
// are split back into lines here, because token values carry newlines
// wherever the grammar likes; that is also why a string opened on one line
// is still a string on the next.
func Highlight(code string, lexer chroma.Lexer) [][]Span {
	if lexer == nil {
		return nil
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return nil
	}
	out := [][]Span{{}}
	for tok := it(); tok != chroma.EOF; tok = it() {
		kind := KindOf(tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, []Span{})
			}
			if part != "" {
				out[len(out)-1] = append(out[len(out)-1], Span{Text: part, Kind: kind})
			}
		}
	}
	return out
}

// KindOf maps a chroma token to one of the five kinds, or to plain for
// everything structural.
func KindOf(t chroma.TokenType) SynKind {
	switch {
	case t.InCategory(chroma.Comment):
		return KindComment
	case t.InCategory(chroma.Keyword):
		return KindKeyword
	case t.InSubCategory(chroma.LiteralString):
		return KindString
	case t.InSubCategory(chroma.LiteralNumber):
		return KindNumber
	case t == chroma.NameFunction || t == chroma.NameClass ||
		t == chroma.NameBuiltin || t == chroma.NameDecorator:
		return KindName
	default:
		return KindPlain
	}
}
