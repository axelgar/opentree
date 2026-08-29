package chat

// The agent's prose is markdown whether or not anyone renders it: every agent
// this chat runs writes fences, emphasis and lists into its replies. This file
// paints them, by hand, because the alternative was a rendering library that
// picks its own light-or-dark theme against the adaptive palette and re-lays
// out a whole document on every streamed chunk.
//
// The renderer is line-oriented and pure: the same text at the same width
// always paints the same string, and a prefix of the text paints a prefix of
// the result — which is what streaming needs. Two rules keep a half-arrived
// reply from flickering:
//
//   - An unterminated fence stays an open code block. Lines already painted as
//     code never snap back to prose when more of the block arrives.
//   - An unmatched inline marker renders literally. A lone ** stays two
//     asterisks until its closer arrives, then the pair snaps to bold; the
//     window is one line, because emphasis never crosses a newline here.
//
// Tables, footnotes, setext headings and link styling are deliberately absent:
// they render as the text they are, which is readable, and each can be added
// behind renderMarkdown without touching a call site.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/ui"
)

// renderMarkdown paints one reply at the given width. Every line of the result
// fits the width; the caller hangs the brand mark and indents, exactly as it
// would for plain text.
func renderMarkdown(text string, width int) string {
	if width < 4 {
		width = 4
	}
	var out []string
	var fence *fenceState
	for _, line := range strings.Split(text, "\n") {
		if fence != nil {
			if fence.closes(line) {
				fence = nil
				continue
			}
			out = append(out, codeLine(line, width))
			continue
		}
		if f, ok := fenceOpen(line); ok {
			fence = &f
			continue
		}
		out = append(out, blockLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

// fenceState is an open code block: the delimiter that opened it, which alone
// can close it.
type fenceState struct {
	char rune
	n    int
}

// fenceOpen reports whether a line opens a fenced code block: up to three
// spaces of indent, then a run of at least three backticks or tildes. The info
// string after a backtick fence may not itself contain a backtick — that is
// inline code, not a fence.
func fenceOpen(line string) (fenceState, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" {
		return fenceState{}, false
	}
	char := rune(trimmed[0])
	if char != '`' && char != '~' {
		return fenceState{}, false
	}
	rest := strings.TrimLeft(trimmed, string(char))
	n := len(trimmed) - len(rest)
	if n < 3 || (char == '`' && strings.ContainsRune(rest, '`')) {
		return fenceState{}, false
	}
	return fenceState{char: char, n: n}, true
}

// closes reports whether a line closes this fence: a run of the same character
// at least as long as the opener, and nothing after it but spaces.
func (f fenceState) closes(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	rest := strings.TrimLeft(trimmed, string(f.char))
	return len(trimmed)-len(rest) >= f.n && strings.TrimSpace(rest) == ""
}

// codeLine sets one line of a code block: truncated plain — never wrapped, so
// indentation keeps meaning — then padded to the column and painted, so the
// block's background runs edge to edge. The padding happens before the paint
// because ui.Truncate must never see styled text.
func codeLine(line string, width int) string {
	line = " " + strings.ReplaceAll(line, "\t", "    ")
	line = ui.Truncate(line, width)
	if pad := width - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return mdCodeBlockStyle.Render(line)
}

// blockLine renders one line of prose — a heading, a rule, a quote, a list
// item or a paragraph — into as many wrapped lines as it needs.
func blockLine(line string, width int) []string {
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	if isRule(line) {
		return []string{mdRuleStyle.Render(strings.Repeat("─", width))}
	}
	if rest, ok := headingLine(line); ok {
		// The marks are dropped and the weight kept: bold is what a heading
		// means, and six gradations of it would be noise at chat width.
		return wrapStyled(inlineStyle(rest, mdHeadingStyle), width, "")
	}
	if quoted, ok := strings.CutPrefix(strings.TrimLeft(line, " "), ">"); ok {
		bar := mdQuoteStyle.Render("▏") + " "
		body := inlineStyle(strings.TrimPrefix(quoted, " "), mdQuoteStyle)
		return wrapPrefixed(body, width, bar, bar)
	}
	if marker, rest, ok := listItem(line); ok {
		hang := strings.Repeat(" ", lipgloss.Width(marker))
		return wrapPrefixed(inlineStyle(rest, agentTextStyle), width, mdBulletStyle.Render(marker), hang)
	}
	return wrapStyled(inlineStyle(line, agentTextStyle), width, "")
}

// isRule reports a thematic break: three or more of the same of -, _ or *,
// alone on their line.
func isRule(line string) bool {
	t := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
	if len(t) < 3 {
		return false
	}
	for _, ch := range []string{"-", "_", "*"} {
		if strings.Count(t, ch) == len(t) {
			return true
		}
	}
	return false
}

// headingLine matches an ATX heading: one to six #, then a space.
func headingLine(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	rest := strings.TrimLeft(trimmed, "#")
	level := len(trimmed) - len(rest)
	if level < 1 || level > 6 || !strings.HasPrefix(rest, " ") {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// listItem matches a bullet or an ordered item and returns the marker as it
// should be drawn, indentation included.
func listItem(line string) (marker, rest string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	indent := strings.Repeat(" ", len(line)-len(trimmed))
	if body, found := cutAnyPrefix(trimmed, "- ", "* ", "+ "); found {
		return indent + "• ", body, true
	}
	digits := trimmed
	for len(digits) > 0 && digits[0] >= '0' && digits[0] <= '9' {
		digits = digits[1:]
	}
	n := len(trimmed) - len(digits)
	if n > 0 && n <= 3 && len(digits) > 1 && (digits[0] == '.' || digits[0] == ')') && digits[1] == ' ' {
		return indent + trimmed[:n+1] + " ", digits[2:], true
	}
	return "", "", false
}

func cutAnyPrefix(s string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if rest, ok := strings.CutPrefix(s, p); ok {
			return rest, true
		}
	}
	return "", false
}

// wrapStyled wraps already-painted text at width, indenting continuation
// lines. lipgloss's wrap is ANSI-aware, so the inline styles survive the fold.
// The padding lipgloss adds to reach the width is trimmed back off: prose has
// no background, so the pad would be dead bytes on every line of every reply.
func wrapStyled(painted string, width int, hang string) []string {
	lines := wrappedLines(painted, width)
	for i := 1; i < len(lines) && hang != ""; i++ {
		lines[i] = hang + lines[i]
	}
	return lines
}

// wrapPrefixed wraps painted text into a column that leaves room for a first-line
// prefix, hanging the continuation lines under it.
func wrapPrefixed(painted string, width int, first, hang string) []string {
	inner := width - lipgloss.Width(hang)
	if inner < 4 {
		inner = 4
	}
	lines := wrappedLines(painted, inner)
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = first + l
		} else {
			out[i] = hang + l
		}
	}
	return out
}

func wrappedLines(painted string, width int) []string {
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(painted), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// inlineStyle paints one line's spans: `code`, ***both***, **bold** and
// *italic* (with _ as *'s synonym). Every plain span carries base explicitly,
// because a styled span's trailing reset would otherwise unpaint the rest of
// the line. Delimiters are matched within the line or not at all — an
// unmatched one is text, which is also what makes a half-streamed line safe.
func inlineStyle(s string, base lipgloss.Style) string {
	var out strings.Builder
	plain := func(t string) {
		if t != "" {
			out.WriteString(base.Render(t))
		}
	}
	runes := []rune(s)
	from := 0
	for i := 0; i < len(runes); {
		switch runes[i] {
		case '`':
			n := runLen(runes[i:], '`')
			if end := findRun(runes[i+n:], '`', n); end >= 0 {
				plain(string(runes[from:i]))
				out.WriteString(mdInlineCodeStyle.Render(" " + string(runes[i+n:i+n+end]) + " "))
				i += n + end + n
				from = i
				continue
			}
			i += n
		case '*', '_':
			ch := runes[i]
			n := min(runLen(runes[i:], ch), 3)
			if end, ok := findEmphasisClose(runes[i+n:], ch, n); ok {
				plain(string(runes[from:i]))
				out.WriteString(emphasisStyle(n).Render(string(runes[i+n : i+n+end])))
				i += n + end + n
				from = i
				continue
			}
			i += n
		default:
			i++
		}
	}
	plain(string(runes[from:]))
	return out.String()
}

// runLen counts how many of ch open the slice.
func runLen(runes []rune, ch rune) int {
	n := 0
	for n < len(runes) && runes[n] == ch {
		n++
	}
	return n
}

// findRun returns the offset of the next run of exactly want ch, or -1.
func findRun(runes []rune, ch rune, want int) int {
	for i := 0; i < len(runes); i++ {
		if runes[i] != ch {
			continue
		}
		n := runLen(runes[i:], ch)
		if n == want {
			return i
		}
		i += n - 1
	}
	return -1
}

// findEmphasisClose finds the closing run for an emphasis opener: the content
// may not begin or end with a space, which is what keeps "2 * 3 * 4" prose.
func findEmphasisClose(runes []rune, ch rune, want int) (int, bool) {
	if len(runes) == 0 || runes[0] == ' ' {
		return 0, false
	}
	i := findRun(runes, ch, want)
	if i <= 0 || runes[i-1] == ' ' {
		return 0, false
	}
	return i, true
}

func emphasisStyle(n int) lipgloss.Style {
	switch n {
	case 1:
		return mdItalicStyle
	case 2:
		return mdBoldStyle
	default:
		return mdBoldItalicStyle
	}
}
