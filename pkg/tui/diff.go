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

	tea "github.com/charmbracelet/bubbletea"
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
}

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
	return diffView{open: true, wsName: wsName, rows: rows, files: files, gutter: gutter}
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
	page := m.diffPage()
	switch msg.String() {
	case "esc", "q":
		m.diff = diffView{}
		return m, nil
	case "up", "k":
		d.cursor--
	case "down", "j":
		d.cursor++
	// A page at a time, and the ends: a diff of a few hundred lines was a
	// few hundred presses of j.
	case "pgup", "ctrl+u":
		d.cursor -= page
		d.offset -= page
	case "pgdown", "ctrl+d", " ":
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
	if d.cursor < d.offset {
		d.offset = d.cursor
	} else if page := m.diffPage(); d.cursor >= d.offset+page {
		d.offset = d.cursor - page + 1
	}
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
// bars as the dialogs, so the keys sit on one line with the position.
func (m Model) diffScreen() string {
	d := m.diff
	page := m.diffPage()
	end := min(d.offset+page, len(d.rows))

	var sb strings.Builder
	for i := d.offset; i < end; i++ {
		sb.WriteString(m.paintRow(i))
		sb.WriteString("\n")
	}

	header := m.bar(titleStyle.Render("Diff: "+d.wsName), m.diffSummary())
	footer := m.bar(
		dialogHintStyle.Render("↑/↓ move  •  pgup/pgdn page  •  g/G ends  •  esc close"),
		dialogHintStyle.Render(fmt.Sprintf("line %d/%d", d.cursor+1, len(d.rows))),
	)
	return appStyle.Render(strings.Join([]string{
		header, m.divider(), sb.String() + m.divider(), footer,
	}, "\n"))
}

// paintRow sets one row: the cursor mark, then the line in the colour its
// kind has always had.
func (m Model) paintRow(i int) string {
	r := m.diff.rows[i]
	mark := " "
	if i == m.diff.cursor {
		mark = diffCursorStyle.Render("▎")
	}
	return mark + styleRow(r)
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
		return diffAddStyle.Render("+" + r.text)
	case rowDel:
		return diffRemoveStyle.Render("-" + r.text)
	case rowContext:
		return " " + r.text
	default:
		return r.text
	}
}
