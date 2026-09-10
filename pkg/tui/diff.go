package tui

// The diff view. DiffCombined and buildGroupDiff hand over one string — git's
// unified output under ══ section headers — and it is parsed here, once, into
// rows that know what they are: which file, which line numbers, added or
// removed. Everything the view does after that (the cursor, the tree, the
// jumps, the notes) is a question about rows, not a question about text.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/axelgar/opentree/pkg/ui"
)

// diffView is the state of an open diff. Zero value is closed.
type diffView struct {
	open   bool
	wsName string
	rows   []diffRow
	files  []diffFile
	// cursor is the row the reader is on; offset the first row on screen.
	// The cursor pulls the window when the keys move it past an edge, and the
	// wheel pulls the cursor when it scrolls the window away from it.
	cursor int
	offset int
	// gutter is the width of the widest line number, for a column that does
	// not jitter as the numbers grow.
	gutter int
	// tree is whether the file pane is wanted (t); showTree says whether it
	// fits. reviewed is the files ticked with space. help is the ? card.
	tree     bool
	reviewed map[int]bool
	help     bool
	// numbers is the line-number gutter (L); wrap is long lines folded
	// rather than cut (w).
	numbers bool
	wrap    bool
	// searching is the find box open in the footer; query lives on after
	// enter closes the box, and n/N step its matches until esc clears it.
	searching bool
	query     string
	matches   []ui.Match
	current   int
}

// The tree pane's width, and the terminal width below which it is not worth
// the columns it takes from the code.
const (
	diffTreeWidth    = 28
	diffTreeMinWidth = 100
)

// Row kinds. Code rows carry the sign git gave them; the rest name the
// structure around the code.
const (
	rowSection = 's' // ══════ Committed Changes ══════
	rowFile    = 'f' // diff --git a/x b/y
	rowMeta    = 'm' // index, ---, +++, rename from, Binary files …
	rowHunk    = 'h' // @@ -a,b +c,d @@
	rowContext = ' '
	rowAdd     = '+'
	rowDel     = '-'
	rowText    = 't' // anything outside a file: "No changes.", an error, air
)

type diffRow struct {
	kind byte
	// text is the line without its sign for code rows, whole otherwise.
	text string
	// oldNo and newNo are the line's numbers in each side, 0 where it has
	// none: a removed line has no new number, a header has neither.
	oldNo, newNo int
	// file indexes diffView.files, or -1 outside any file.
	file int
}

type diffFile struct {
	// section is the ══ header the file sits under, "" when there was none.
	section string
	// path is the new path; oldPath is set only for a rename.
	path, oldPath  string
	binary         bool
	added, removed int
	// first and last are the file's row range, header included.
	first, last int
}

// newDiffView parses content and opens on its first row.
func newDiffView(content, wsName string) diffView {
	rows, files, gutter := parseDiff(content)
	return diffView{open: true, wsName: wsName, rows: rows, files: files, gutter: gutter,
		tree: true, reviewed: map[int]bool{}}
}

// parseDiff turns git's unified output — possibly several diffs under ══
// headers — into rows and the files they belong to. It is prefix matching,
// the same tests renderDiffLine used to make, with the hunk headers read for
// their numbers so every code row can say where it is. The third result is
// the gutter width.
func parseDiff(content string) ([]diffRow, []diffFile, int) {
	lines := strings.Split(content, "\n")
	rows := make([]diffRow, 0, len(lines))
	var files []diffFile
	section := ""
	cur := -1 // index into files
	inHunk := false
	oldNo, newNo := 0, 0
	widest := 0

	closeFile := func() {
		if cur >= 0 {
			files[cur].last = len(rows) - 1
		}
		cur = -1
		inHunk = false
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "══"):
			closeFile()
			section = strings.Trim(line, "═ ")
			rows = append(rows, diffRow{kind: rowSection, text: line, file: -1})
			continue
		case strings.HasPrefix(line, "diff --git "):
			closeFile()
			oldPath, newPath := gitPaths(strings.TrimPrefix(line, "diff --git "))
			f := diffFile{section: section, path: newPath, first: len(rows)}
			if oldPath != newPath {
				f.oldPath = oldPath
			}
			files = append(files, f)
			cur = len(files) - 1
			rows = append(rows, diffRow{kind: rowFile, text: line, file: cur})
			continue
		}
		if cur < 0 {
			rows = append(rows, diffRow{kind: rowText, text: line, file: -1})
			continue
		}
		if strings.HasPrefix(line, "@@") {
			oldNo, newNo = hunkStarts(line)
			inHunk = true
			rows = append(rows, diffRow{kind: rowHunk, text: line, file: cur})
			continue
		}
		if !inHunk {
			if strings.HasPrefix(line, "Binary files ") {
				files[cur].binary = true
			}
			rows = append(rows, diffRow{kind: rowMeta, text: line, file: cur})
			continue
		}
		row := diffRow{file: cur}
		switch {
		case line == "" || line[0] == ' ':
			// A blank is an empty context line whose one space was trimmed
			// away at the end of a section.
			row.kind, row.text = rowContext, strings.TrimPrefix(line, " ")
			row.oldNo, row.newNo = oldNo, newNo
			oldNo++
			newNo++
		case line[0] == '+':
			row.kind, row.text, row.newNo = rowAdd, line[1:], newNo
			newNo++
			files[cur].added++
		case line[0] == '-':
			row.kind, row.text, row.oldNo = rowDel, line[1:], oldNo
			oldNo++
			files[cur].removed++
		case line[0] == '\\':
			row.kind, row.text = rowMeta, line
		default:
			// Something after the hunk that is not a hunk: the section's own
			// trailing air, or text a caller appended.
			closeFile()
			rows = append(rows, diffRow{kind: rowText, text: line, file: -1})
			continue
		}
		widest = max(widest, row.oldNo, row.newNo)
		rows = append(rows, row)
	}
	closeFile()
	return rows, files, len(strconv.Itoa(widest))
}

// gitPaths splits the "a/old b/new" tail of a diff --git line. Paths with a
// space in them are split on the last " b/", which is right for every path
// that does not itself contain " b/".
func gitPaths(tail string) (oldPath, newPath string) {
	i := strings.LastIndex(tail, " b/")
	if i < 0 {
		return tail, tail
	}
	return strings.TrimPrefix(tail[:i], "a/"), tail[i+3:]
}

// hunkStarts reads the two start lines out of "@@ -a,b +c,d @@ …".
func hunkStarts(header string) (oldStart, newStart int) {
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return 1, 1
	}
	return rangeStart(fields[1]), rangeStart(fields[2])
}

func rangeStart(r string) int {
	r = strings.TrimLeft(r, "-+")
	if i := strings.IndexByte(r, ','); i >= 0 {
		r = r[:i]
	}
	n, err := strconv.Atoi(r)
	if err != nil {
		return 1
	}
	return n
}

// --- keys, scroll ---------------------------------------------------------

// handleDiffKey is the open diff's keyboard. It owns every key: the list
// underneath is not what the reader is looking at.
func (m Model) handleDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.diff
	if d.help {
		d.help = false
		return m, nil
	}
	if d.searching {
		return m.handleDiffFindKey(msg)
	}
	page := m.diffPage()
	switch msg.String() {
	case "esc":
		// With a live query, esc clears it; the next esc closes.
		if d.query != "" {
			d.query, d.matches = "", nil
			return m, nil
		}
		m.diff = diffView{}
		return m, nil
	case "q":
		m.diff = diffView{}
		return m, nil
	case "/":
		d.searching = true
		m.input.Reset()
		m.input.Placeholder = "find"
		m.input.Focus()
		return m, textinput.Blink
	case "N":
		m.stepDiffMatch(-1)
	case "?":
		d.help = true
	case "t":
		d.tree = !d.tree
	case "L":
		d.numbers = !d.numbers
	case "w":
		d.wrap = !d.wrap
	case "]":
		m.jumpDiff(d.nextRow(d.cursor, +1, func(r diffRow) bool { return r.kind == rowHunk }))
	case "[":
		m.jumpDiff(d.nextRow(d.cursor, -1, func(r diffRow) bool { return r.kind == rowHunk }))
	case "n":
		// revdiff's rule: n steps matches while there is a query, files otherwise.
		if d.query != "" {
			m.stepDiffMatch(+1)
			break
		}
		m.jumpDiff(d.nextRow(d.cursor, +1, func(r diffRow) bool { return r.kind == rowFile }))
	case "p":
		m.jumpDiff(d.nextRow(d.cursor, -1, func(r diffRow) bool { return r.kind == rowFile }))
	case " ":
		if f := d.fileAt(d.cursor); f >= 0 {
			d.reviewed[f] = !d.reviewed[f]
		}
	case "up", "k":
		d.cursor--
	case "down", "j":
		d.cursor++
	// A page at a time, and the ends: a diff of a few hundred lines was a
	// few hundred presses of j.
	case "pgup", "ctrl+u":
		d.cursor -= page
		d.offset -= page
	case "pgdown", "ctrl+d":
		d.cursor += page
		d.offset += page
	case "g", "home":
		d.cursor, d.offset = 0, 0
	case "G", "end":
		d.cursor = len(d.rows) - 1
		d.offset = m.maxDiffScroll()
	}
	m.clampDiffScroll()
	return m, nil
}

// handleDiffFindKey types into the find box. Enter keeps the query and gives
// the keys back to the diff; esc clears it.
func (m Model) handleDiffFindKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.diff
	switch msg.String() {
	case "esc":
		d.searching, d.query, d.matches = false, "", nil
		m.input.Reset()
		return m, nil
	case "enter":
		d.searching = false
		m.input.Reset()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	d.query = m.input.Value()
	m.refindDiff()
	return m, cmd
}

// refindDiff finds the query again and lands on the first match at or below
// the cursor — searching starts from where the reader is — wrapping to the
// first when nothing is below.
func (m *Model) refindDiff() {
	d := &m.diff
	rows := make([]string, len(d.rows))
	for i, r := range d.rows {
		rows[i] = expandTabs(r.text)
	}
	d.matches = ui.FindMatches(rows, d.query)
	d.current = -1
	for i, mt := range d.matches {
		if mt.Line >= d.cursor {
			d.current = i
			break
		}
	}
	if d.current < 0 && len(d.matches) > 0 {
		d.current = 0
	}
	m.showDiffMatch()
}

// stepDiffMatch moves to the next or previous match, around the ends.
func (m *Model) stepDiffMatch(delta int) {
	d := &m.diff
	n := len(d.matches)
	if n == 0 {
		return
	}
	d.current = ((d.current+delta)%n + n) % n
	m.showDiffMatch()
}

// showDiffMatch puts the cursor on the current match, centred on screen when
// it was not already in view.
func (m *Model) showDiffMatch() {
	d := &m.diff
	if d.current < 0 || d.current >= len(d.matches) {
		return
	}
	line := d.matches[d.current].Line
	if line < d.offset || line > m.lastVisibleRow() {
		d.offset = line - m.diffPage()/2
	}
	d.cursor = line
	m.clampDiffScroll()
}

// nextRow is the first row past from, in the direction of step, that want
// accepts — or -1 when there is none.
func (d diffView) nextRow(from, step int, want func(diffRow) bool) int {
	for i := from + step; i >= 0 && i < len(d.rows); i += step {
		if want(d.rows[i]) {
			return i
		}
	}
	return -1
}

// fileAt is the file a row belongs to, or -1.
func (d diffView) fileAt(row int) int {
	if row < 0 || row >= len(d.rows) {
		return -1
	}
	return d.rows[row].file
}

// jumpDiff puts a row at the top of the window, the way a jump to a hunk or a
// file should land: what was jumped to is the first thing on screen, with
// what follows it below. A -1 (nothing to jump to) stays put.
func (m *Model) jumpDiff(row int) {
	if row < 0 {
		return
	}
	m.diff.cursor, m.diff.offset = row, row
	m.clampDiffScroll()
}

// clickDiff routes a press inside the open diff: a file in the tree jumps to
// it, a row in the body takes the cursor.
func (m Model) clickDiff(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.diff.help {
		m.diff.help = false
		return m, nil
	}
	line := msg.Y - appStyle.GetPaddingTop() - 2 // header and divider
	if line < 0 || line >= m.diffPage() {
		return m, nil
	}
	if m.showTree() && msg.X-appStyle.GetPaddingLeft() < diffTreeWidth {
		lines, top := m.treeWindow()
		if i := top + line; i < len(lines) && lines[i].file >= 0 {
			m.jumpDiff(m.diff.files[lines[i].file].first)
		}
		return m, nil
	}
	if row := m.diff.offset + line; row < len(m.diff.rows) {
		m.diff.cursor = row
	}
	return m, nil
}

// scrollDiff moves the window, as the wheel does, and brings the cursor
// along if it would otherwise be left off screen.
func (m *Model) scrollDiff(delta int) {
	d := &m.diff
	d.offset = min(max(d.offset+delta, 0), m.maxDiffScroll())
	d.cursor = min(max(d.cursor, d.offset), d.offset+m.diffPage()-1)
	m.clampDiffScroll()
}

// clampDiffScroll keeps the cursor on a row and the window on the cursor.
// Called after every key, and on resize, so no two paths can disagree.
func (m *Model) clampDiffScroll() {
	d := &m.diff
	d.cursor = min(max(d.cursor, 0), max(len(d.rows)-1, 0))
	d.offset = min(max(d.offset, 0), m.maxDiffScroll())
	switch {
	case d.cursor < d.offset:
		d.offset = d.cursor
	case d.wrap:
		// Wrapped rows take more than one line each, so the window's last row
		// is found by filling it rather than by arithmetic.
		for d.offset < d.cursor && d.cursor > m.lastVisibleRow() {
			d.offset++
		}
	case d.cursor >= d.offset+m.diffPage():
		d.offset = d.cursor - m.diffPage() + 1
	}
}

// lastVisibleRow is the last row the window shows whole, at the current
// offset and width. Without wrap it is arithmetic; with it, a fill.
func (m Model) lastVisibleRow() int {
	d := m.diff
	if !d.wrap {
		return min(d.offset+m.diffPage(), len(d.rows)) - 1
	}
	lines := 0
	last := d.offset
	for i := d.offset; i < len(d.rows); i++ {
		lines += len(m.rowLines(i))
		if lines > m.diffPage() {
			break
		}
		last = i
	}
	return last
}

// maxDiffScroll is the furthest the window can go before the last row is on
// screen.
func (m Model) maxDiffScroll() int {
	return max(len(m.diff.rows)-m.diffPage(), 0)
}

// diffPage is how many rows are on screen, which is what one page key moves by.
func (m Model) diffPage() int {
	return max(m.height-headerFooterHeight, minDiffHeight)
}

// --- view -----------------------------------------------------------------

// diffScreen is the open diff: a reader, not a card — but with the same two
// bars as the dialogs, so the keys sit on one line with the position. The
// tree, when it fits, stands to the left of the code.
func (m Model) diffScreen() string {
	if m.diff.help {
		return m.diffHelpCard()
	}
	d := m.diff
	page := m.diffPage()

	body := make([]string, 0, page)
	for i := d.offset; i < len(d.rows) && len(body) < page; i++ {
		body = append(body, m.rowLines(i)...)
	}
	body = body[:min(len(body), page)]
	for len(body) < page {
		body = append(body, "")
	}
	pane := strings.Join(body, "\n")
	if m.showTree() {
		pane = lipgloss.JoinHorizontal(lipgloss.Top, m.treePane(page), pane)
	}

	header := m.bar(titleStyle.Render("Diff: "+d.wsName), m.diffSummary())
	footer := m.bar(
		dialogHintStyle.Render("j/k [ ] n/p move  •  / find  •  space reviewed  •  ? keys  •  esc close"),
		dialogHintStyle.Render(m.diffPosition()),
	)
	if d.searching {
		footer = m.bar(
			titleStyle.Render("find")+" › "+m.input.View()+"   "+dialogHintStyle.Render(m.diffMatchCount()),
			dialogHintStyle.Render("enter keep  •  esc clear"),
		)
	}
	return appStyle.Render(strings.Join([]string{
		header, m.divider(), pane + "\n" + m.divider(), footer,
	}, "\n"))
}

// diffPosition is the footer's right end: the cursor line, and while a query
// lives, where the reader stands among its matches.
func (m Model) diffPosition() string {
	pos := fmt.Sprintf("line %d/%d", m.diff.cursor+1, len(m.diff.rows))
	if m.diff.query != "" {
		return "≋ " + m.diff.query + " · " + m.diffMatchCount() + " · " + pos
	}
	return pos
}

func (m Model) diffMatchCount() string {
	switch {
	case m.diff.query == "":
		return "type to search the diff"
	case len(m.diff.matches) == 0:
		return "no matches"
	default:
		return fmt.Sprintf("%d of %d", m.diff.current+1, len(m.diff.matches))
	}
}

// showTree is whether the tree pane is drawn: wanted, worth it, and fits.
func (m Model) showTree() bool {
	return m.diff.tree && len(m.diff.files) > 1 && m.width >= diffTreeMinWidth
}

// diffBodyWidth is the columns the code has, beside the tree or without it.
func (m Model) diffBodyWidth() int {
	if m.showTree() {
		return m.chromeWidth() - diffTreeWidth - 2
	}
	return m.chromeWidth()
}

// rowLines is a row as it goes on screen: one line cut to the width with an
// ellipsis, or, under wrap, as many as the width needs.
func (m Model) rowLines(i int) []string {
	line := m.paintRow(i)
	width := m.diffBodyWidth()
	if !m.diff.wrap {
		return []string{ansi.Truncate(line, width, "…")}
	}
	return strings.Split(ansi.Hardwrap(line, width, true), "\n")
}

// treeLine is one row of the tree pane: a file, or a section heading (-1).
type treeLine struct {
	text string
	file int
}

// treeLines lays the files out under their sections: a heading whenever the
// section changes, which is how a fan-out comparison reads as sibling › files
// and a plain diff as Committed › files › Uncommitted › files, without the
// tree knowing which it is drawing.
func (m Model) treeLines() []treeLine {
	d := m.diff
	current := d.fileAt(d.cursor)
	var out []treeLine
	section := ""
	for i, f := range d.files {
		if f.section != section {
			section = f.section
			out = append(out, treeLine{text: diffSectionStyle.Render(ui.Truncate(section, diffTreeWidth-1)), file: -1})
		}
		var counts string
		switch {
		case f.binary:
			counts = diffStyle.Render("bin")
		default:
			counts = fileAddedStyle.Render(fmt.Sprintf("+%d", f.added)) + " " +
				fileRemovedStyle.Render(fmt.Sprintf("-%d", f.removed))
		}
		nameWidth := diffTreeWidth - 3 - lipgloss.Width(counts)
		name := shortenPath(f.path, nameWidth)
		mark := " "
		switch {
		case i == current:
			mark, name = diffCursorStyle.Render("▎"), diffCursorStyle.Render(name)
		case d.reviewed[i]:
			mark, name = "✓", diffStyle.Render(name)
		}
		pad := strings.Repeat(" ", max(nameWidth-lipgloss.Width(name), 0))
		out = append(out, treeLine{text: mark + " " + name + pad + " " + counts, file: i})
	}
	return out
}

// treeWindow is the tree's lines and the first one on screen, chosen so the
// current file is in view — centred when the tree is taller than the screen.
func (m Model) treeWindow() ([]treeLine, int) {
	lines := m.treeLines()
	page := m.diffPage()
	current := m.diff.fileAt(m.diff.cursor)
	at := 0
	for i, l := range lines {
		if l.file == current {
			at = i
		}
	}
	top := min(max(at-page/2, 0), max(len(lines)-page, 0))
	return lines, top
}

// treePane is the tree, page lines tall, with its rule down the right.
func (m Model) treePane(page int) string {
	lines, top := m.treeWindow()
	rule := dividerStyle.Render("│")
	out := make([]string, 0, page)
	for i := top; i < top+page; i++ {
		text := ""
		if i < len(lines) {
			text = ansi.Truncate(lines[i].text, diffTreeWidth, "…")
		}
		pad := strings.Repeat(" ", max(diffTreeWidth-lipgloss.Width(text), 0))
		out = append(out, text+pad+rule+" ")
	}
	return strings.Join(out, "\n")
}

// diffKeys is what the ? card says. The diff swallows every key, so the
// list's help cannot describe it; this is the one place they are written.
var diffKeys = [][2]string{
	{"↑/↓ j/k", "move"},
	{"pgup/pgdn", "page"},
	{"g/G", "first / last line"},
	{"[ / ]", "previous / next hunk"},
	{"n / p", "next / previous file"},
	{"/", "find; n / N step the matches, esc clears"},
	{"space", "mark file reviewed"},
	{"t", "show / hide the tree"},
	{"L", "line numbers"},
	{"w", "wrap long lines"},
	{"esc q", "close"},
}

func (m Model) diffHelpCard() string {
	var sb strings.Builder
	for _, k := range diffKeys {
		fmt.Fprintf(&sb, "%-12s %s\n", k[0], k[1])
	}
	return m.dialogCard("Diff keys", strings.TrimRight(sb.String(), "\n"),
		dialogHintStyle.Render("any key closes"), dialogAccent)
}

// paintRow sets one row: the cursor mark, then the line in the colour its
// kind has always had.
func (m Model) paintRow(i int) string {
	r := m.diff.rows[i]
	mark := " "
	if i == m.diff.cursor {
		mark = diffCursorStyle.Render("▎")
	}
	line := styleRow(r)
	// Matches were measured on the bare text; a code row has its sign in
	// front of that. Painted from the right so the columns hold.
	shift := 0
	if r.kind == rowContext || r.kind == rowAdd || r.kind == rowDel {
		shift = 1
	}
	for j := len(m.diff.matches) - 1; j >= 0; j-- {
		mt := m.diff.matches[j]
		if mt.Line != i {
			continue
		}
		style := diffMatchStyle
		if j == m.diff.current {
			style = diffCurrentMatchStyle
		}
		line = ui.Paint(line, mt.Col+shift, mt.Width, style)
	}
	return mark + m.gutter(r) + line
}

// gutter is the two line-number columns, old and new, blank where a row has
// no number on that side — and blank altogether when L is off.
func (m Model) gutter(r diffRow) string {
	if !m.diff.numbers {
		return ""
	}
	w := m.diff.gutter
	no := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", w)
		}
		return fmt.Sprintf("%*d", w, n)
	}
	return diffStyle.Render(no(r.oldNo)+" "+no(r.newNo)) + " "
}

func styleRow(r diffRow) string {
	switch r.kind {
	case rowSection:
		return diffSectionStyle.Render(r.text)
	case rowFile:
		return diffFileStyle.Render(r.text)
	case rowMeta:
		if strings.HasPrefix(r.text, "--- ") || strings.HasPrefix(r.text, "+++ ") {
			return diffFileStyle.Render(r.text)
		}
		return r.text
	case rowHunk:
		return diffHunkStyle.Render(r.text)
	case rowAdd:
		return diffAddStyle.Render("+" + expandTabs(r.text))
	case rowDel:
		return diffRemoveStyle.Render("-" + expandTabs(r.text))
	case rowContext:
		return " " + expandTabs(r.text)
	default:
		return r.text
	}
}

// expandTabs is what lipgloss does to a tab when it styles a line, done to
// the unstyled lines too, so a context line sits under the changed one.
func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }
