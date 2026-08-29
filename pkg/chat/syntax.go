package chat

// The code inside a reply's fences is coloured by chroma — but only lexed by
// it. Chroma's own formatters and themes pick colours with no idea whether the
// terminal is light or dark, which is the exact mistake the adaptive palette
// exists to prevent; so its lexers hand over tokens, and the painting is done
// here with palette colours, the same way everything else in the chat is.
//
// The mapping is deliberately coarse: keywords, strings, numbers, comments and
// the names of things. Five colours read as highlighted code; twenty read as a
// ransom note, doubly so at chat width.

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/ui"
)

// renderCodeBlock sets a whole fenced block: highlighted when the fence named
// a language chroma knows, plain otherwise. The block is lexed together — a
// string or comment opened on one line reaches into the next — then painted
// line by line under the same truncate-plain-then-pad rule as codeLine.
func renderCodeBlock(lines []string, lang string, width int) []string {
	spans := highlight(strings.Join(lines, "\n"), lang)
	// Most lexers quietly append the newline they insist a file ends with;
	// rendered, that would be one empty band line the author never wrote.
	for len(spans) > len(lines) && len(spans[len(spans)-1]) == 0 {
		spans = spans[:len(spans)-1]
	}
	if spans == nil {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = codeLine(l, width)
		}
		return out
	}
	out := make([]string, len(spans))
	for i, line := range spans {
		out[i] = paintSpans(line, width)
	}
	return out
}

// codeSpan is a run of code that one style paints.
type codeSpan struct {
	text  string
	style lipgloss.Style
}

// highlight lexes code and returns it as styled spans per line, or nil when
// the language is unknown — the caller falls back to unhighlighted code, which
// is also what a lexing error degrades to. Chroma is given the block whole and
// its tokens are split back into lines here, because token values carry
// newlines wherever the grammar likes.
func highlight(code, lang string) [][]codeSpan {
	if lang == "" {
		return nil
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return nil
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return nil
	}
	out := [][]codeSpan{{}}
	for tok := it(); tok != chroma.EOF; tok = it() {
		style := tokenStyle(tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, []codeSpan{})
			}
			if part != "" {
				out[len(out)-1] = append(out[len(out)-1], codeSpan{text: part, style: style})
			}
		}
	}
	return out
}

// paintSpans sets one highlighted line at the column: a space of air, the
// spans truncated on their plain text, and the tail padded so the block's
// background runs edge to edge.
func paintSpans(line []codeSpan, width int) string {
	var out strings.Builder
	used := 0
	out.WriteString(mdCodeBlockStyle.Render(" "))
	used++
	for _, s := range line {
		text := strings.ReplaceAll(s.text, "\t", "    ")
		if used+lipgloss.Width(text) > width {
			text = ui.Truncate(text, width-used)
		}
		out.WriteString(s.style.Render(text))
		if used += lipgloss.Width(text); used >= width {
			break
		}
	}
	if used < width {
		out.WriteString(mdCodeBlockStyle.Render(strings.Repeat(" ", width-used)))
	}
	return out.String()
}

// tokenStyle maps a chroma token to one of the five syntax styles, or to the
// block's base style for everything structural.
func tokenStyle(t chroma.TokenType) lipgloss.Style {
	switch {
	case t.InCategory(chroma.Comment):
		return mdSynCommentStyle
	case t.InCategory(chroma.Keyword):
		return mdSynKeywordStyle
	case t.InSubCategory(chroma.LiteralString):
		return mdSynStringStyle
	case t.InSubCategory(chroma.LiteralNumber):
		return mdSynNumberStyle
	case t == chroma.NameFunction || t == chroma.NameClass ||
		t == chroma.NameBuiltin || t == chroma.NameDecorator:
		return mdSynNameStyle
	default:
		return mdCodeBlockStyle
	}
}
