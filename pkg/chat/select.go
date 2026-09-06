package chat

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Selecting text with the mouse. The chat holds the mouse so the wheel scrolls
// the conversation rather than the terminal's scrollback, and with the mouse
// goes the terminal's own drag-to-select. This gives the drag back: press on
// the log, drag, and what is under the highlight goes to the clipboard when
// the button comes up — the rows as drawn, colours stripped, which is what the
// terminal would have copied. A double-click takes the word under the pointer
// and a triple-click the row, the way every terminal does.
//
// The selection lives in cells of the rendered log rather than in entries:
// what is on screen is what a reader points at, and a reply's markdown, a tool
// row's diff and the log's own notices all become rows in one place, which is
// the viewport's content. ctrl+y is the other half, for the things worth
// copying whole — a code block without its wrap — and for a chat with no
// mouse at all.

// cell is a position in the rendered log: which row, and which column of it.
type cell struct{ line, col int }

// selMode is what a press selects by: cells, words or whole rows.
type selMode int

const (
	selCells selMode = iota
	selWords
	selLines
)

// selection is the state of the drag, and of the highlight it leaves behind.
type selection struct {
	// active is whether the button is still down; on is whether there is a
	// highlight to draw. The highlight outlives the drag by a moment, so the
	// reader can see what was copied.
	active bool
	on     bool
	mode   selMode

	// anchor is where the press landed and head where the pointer is now;
	// either may come first.
	anchor, head cell

	// clicks counts presses on one cell in quick succession, which is how a
	// double- and a triple-click are told from two presses.
	clicks    int
	lastPress time.Time
	lastCell  cell
}

// multiClick is how quickly a second press has to follow the first to count
// as the same gesture.
const multiClick = 400 * time.Millisecond

// colEnd is a column past the end of any row: where a drag below the log
// lands, and where a selected row ends. Large rather than the largest int,
// which is one addition away from wrapping negative.
const colEnd = 1 << 30

// now is the clock the click counter reads, a variable so a test can press
// twice in no time at all.
var now = time.Now

// handleMouse routes the mouse: the wheel to the viewport, which scrolls
// itself, and the left button to the selection.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if tea.MouseEvent(msg).IsWheel() {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m.relayout(), cmd
	}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		return m.pressAt(msg.X, msg.Y).relayout(), nil
	case tea.MouseActionMotion:
		if !m.sel.active {
			return m, nil
		}
		m.sel.head = m.cellAt(msg.X, msg.Y)
		return m.relayout(), nil
	case tea.MouseActionRelease:
		if !m.sel.active {
			return m, nil
		}
		return m.release()
	}
	return m, nil
}

// pressAt starts a selection where the button went down, or counts the press
// toward a double- or triple-click on the same cell. A press outside the log
// — on the header, the input, a dialog — selects nothing and ends whatever
// highlight was showing.
func (m Model) pressAt(x, y int) Model {
	if !m.inLog(y) || len(m.logLines) == 0 {
		m.sel = selection{}
		return m
	}
	at := m.cellAt(x, y)
	t := now()
	clicks := 1
	if at == m.sel.lastCell && t.Sub(m.sel.lastPress) < multiClick {
		clicks = m.sel.clicks + 1
	}
	sel := selection{active: true, on: true, anchor: at, head: at,
		clicks: clicks, lastPress: t, lastCell: at}
	switch {
	case clicks >= 3:
		sel.mode = selLines
	case clicks == 2:
		sel.mode = selWords
	}
	m.sel = sel
	return m
}

// release ends the drag and copies what it covered. A press that never moved
// covered nothing, and copying nothing would replace whatever the clipboard
// held with an empty string.
func (m Model) release() (tea.Model, tea.Cmd) {
	m.sel.active = false
	text := m.selectedText()
	if text == "" {
		m.sel.on = false
		return m.relayout(), nil
	}
	return m.relayout(), copyCmd(countLines(text), text)
}

// clearSelection takes the highlight down. Called for a key press, and when
// the copy's receipt leaves the status line: a highlight with no drag behind
// it is a stain.
func (m Model) clearSelection() Model {
	if !m.sel.on || m.sel.active {
		return m
	}
	m.sel.on = false
	return m.relayout()
}

// inLog reports whether a screen row belongs to the viewport.
func (m Model) inLog(y int) bool {
	return y >= headerHeight && y < headerHeight+m.viewport.Height
}

// cellAt maps a screen position onto the log. A pointer dragged above the
// viewport selects from the top of what is visible, and one dragged below it
// to the end of the last visible row — the drag is clamped to the screen
// rather than scrolling it.
func (m Model) cellAt(x, y int) cell {
	row := y - headerHeight
	col := max(x, 0)
	switch {
	case row < 0:
		row, col = 0, 0
	case row >= m.viewport.Height:
		row, col = m.viewport.Height-1, colEnd
	}
	line := min(max(m.viewport.YOffset+row, 0), len(m.logLines)-1)
	return cell{line: line, col: col}
}

// span is the selection as a range of cells: where it starts and where it
// ends, in reading order, widened to words or rows when the press asked for
// them. ok is false for a selection that covers nothing.
func (m Model) span() (from, to cell, ok bool) {
	from, to = m.sel.anchor, m.sel.head
	if to.line < from.line || (to.line == from.line && to.col < from.col) {
		from, to = to, from
	}
	if from.line < 0 || to.line >= len(m.logLines) {
		return from, to, false
	}
	switch m.sel.mode {
	case selLines:
		from.col, to.col = 0, colEnd
	case selWords:
		from.col, _ = wordBounds(m.logLines[from.line], from.col)
		_, to.col = wordBounds(m.logLines[to.line], to.col)
	default:
		if from == to {
			return from, to, false
		}
	}
	return from, to, true
}

// selectedText is what the highlight covers, colours stripped, one line per
// row, trailing spaces trimmed — the padding a row was drawn with is not part
// of what it says.
func (m Model) selectedText() string {
	from, to, ok := m.span()
	if !ok {
		return ""
	}
	lines := make([]string, 0, to.line-from.line+1)
	for i := from.line; i <= to.line; i++ {
		start, end := 0, colEnd
		if i == from.line {
			start = from.col
		}
		if i == to.line {
			end = to.col + 1
		}
		lines = append(lines, strings.TrimRight(ansi.Strip(ansi.Cut(m.logLines[i], start, end)), " "))
	}
	text := strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

// paintSelection draws the highlight over the rendered rows. The rows are
// painted after rendering, not inside it: the render cache keys on an entry's
// revision, and a drag that invalidated cache lines on every motion event
// would re-render the log at pointer speed.
//
// Cutting a styled row with x/ansi keeps the escape state on both sides of
// the cut, so the text before the highlight keeps its colours and the text
// after it gets them back; the highlighted stretch itself is stripped and
// repainted in inverse, which is what a selection looks like everywhere.
func (m Model) paintSelection(lines []string) []string {
	from, to, ok := m.span()
	if !ok || !m.sel.on {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)
	for i := from.line; i <= to.line && i < len(out); i++ {
		start, end := 0, colEnd
		if i == from.line {
			start = from.col
		}
		if i == to.line {
			end = to.col + 1
		}
		row := out[i]
		mid := ansi.Strip(ansi.Cut(row, start, end))
		if mid == "" {
			continue
		}
		out[i] = ansi.Cut(row, 0, start) + selectStyle.Render(mid) + ansi.Cut(row, end, colEnd)
	}
	return out
}

// wordBounds is the first and last column of the word at col in a rendered
// row, colours ignored. A word is a run of anything but spaces and the
// punctuation that brackets one — so a path comes out whole and a quoted
// string comes out without its quotes. On a space, the space alone.
func wordBounds(row string, col int) (first, last int) {
	plain := []rune(ansi.Strip(row))
	// Columns and runes differ where a rune is two cells wide, so the row is
	// walked with its widths rather than indexed.
	starts := make([]int, len(plain)+1)
	for i, r := range plain {
		starts[i+1] = starts[i] + max(ansi.StringWidth(string(r)), 1)
	}
	if len(plain) == 0 || col >= starts[len(plain)] {
		return col, col
	}
	at := 0
	for at+1 < len(plain) && starts[at+1] <= col {
		at++
	}
	if !wordRune(plain[at]) {
		return starts[at], starts[at+1] - 1
	}
	lo, hi := at, at
	for lo > 0 && wordRune(plain[lo-1]) {
		lo--
	}
	for hi+1 < len(plain) && wordRune(plain[hi+1]) {
		hi++
	}
	return starts[lo], starts[hi+1] - 1
}

func wordRune(r rune) bool {
	if unicode.IsSpace(r) {
		return false
	}
	return !strings.ContainsRune("\"'`()[]{}<>,;", r)
}
