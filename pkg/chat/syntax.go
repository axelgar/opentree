package chat

// The code inside a reply's fences is coloured by chroma — lexed in pkg/ui,
// which says what each span is, and painted here in the chat's own styles,
// the same way everything else in the chat is.

import (
	"strings"

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
// the language is unknown — the caller falls back to unhighlighted code.
func highlight(code, lang string) [][]codeSpan {
	if lang == "" {
		return nil
	}
	lines := ui.Highlight(code, lexers.Get(lang))
	if lines == nil {
		return nil
	}
	out := make([][]codeSpan, len(lines))
	for i, line := range lines {
		out[i] = make([]codeSpan, len(line))
		for j, sp := range line {
			out[i][j] = codeSpan{text: sp.Text, style: kindStyle(sp.Kind)}
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

// kindStyle is the style each kind of span is painted in, the block's base
// style for plain code.
func kindStyle(k ui.SynKind) lipgloss.Style {
	switch k {
	case ui.KindComment:
		return mdSynCommentStyle
	case ui.KindKeyword:
		return mdSynKeywordStyle
	case ui.KindString:
		return mdSynStringStyle
	case ui.KindNumber:
		return mdSynNumberStyle
	case ui.KindName:
		return mdSynNameStyle
	default:
		return mdCodeBlockStyle
	}
}
