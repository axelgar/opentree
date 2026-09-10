package chat

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/ui"
)

// Find is how a long conversation is read back through for the one thing in
// it — the file the agent mentioned, the error it quoted. ctrl+f opens a box
// where the input was; typing narrows to the rows that contain the text,
// case-insensitively, with the log scrolled to the current match and every
// match marked; ctrl+n and ctrl+p step through them; esc closes the box and
// leaves the log where it stands, which is the point of having looked.
//
// The rows are searched rather than the entries: what is on screen is what
// the reader is looking for, and a match's position on screen is what the
// highlight and the scroll need. Colours are stripped first, so a word an
// escape sequence runs through is still one word.

// finding is the state of the box.
type finding struct {
	open    bool
	query   string
	matches []ui.Match
	// current is the match the log is scrolled to, or -1 with none.
	current int
	// lines is how many rows the log had when the matches were found, so a
	// log that grew underneath the box can be searched again.
	lines int
}

func (m Model) openFind() (tea.Model, tea.Cmd) {
	m.finding = finding{open: true, current: -1}
	return m.relayout(), nil
}

// handleFindKey types into the box, steps through the matches, or closes it.
func (m Model) handleFindKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		return m, leave
	case msg.String() == "esc":
		m.finding = finding{}
		return m.relayout(), nil
	case msg.String() == "enter" || msg.String() == "ctrl+n" || msg.String() == "down":
		return m.stepMatch(+1).relayout(), nil
	case msg.String() == "ctrl+p" || msg.String() == "up":
		return m.stepMatch(-1).relayout(), nil
	case msg.String() == "backspace":
		runes := []rune(m.finding.query)
		if len(runes) > 0 {
			m.finding.query = string(runes[:len(runes)-1])
		}
		return m.refind(), nil
	case msg.String() == "ctrl+u":
		m.finding.query = ""
		return m.refind(), nil
	case msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace:
		m.finding.query += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.finding.query += " "
		}
		return m.refind(), nil
	}
	return m, nil
}

// refind finds the query again from the top and lands on the first match at
// or below what is on screen — searching starts from where the reader is,
// not from the top of a conversation they have already read — and wraps to
// the first when nothing is below.
func (m Model) refind() Model {
	m.finding.matches = ui.FindMatches(m.logLines, m.finding.query)
	m.finding.lines = len(m.logLines)
	m.finding.current = -1
	for i, mt := range m.finding.matches {
		if mt.Line >= m.viewport.YOffset {
			m.finding.current = i
			break
		}
	}
	if m.finding.current < 0 && len(m.finding.matches) > 0 {
		m.finding.current = 0
	}
	return m.showMatch().relayout()
}

// stepMatch moves to the next or previous match, around the ends.
func (m Model) stepMatch(delta int) Model {
	n := len(m.finding.matches)
	if n == 0 {
		return m
	}
	m.finding.current = ((m.finding.current+delta)%n + n) % n
	return m.showMatch()
}

// showMatch scrolls the log so the current match is on screen, near the
// middle where the eye lands, unless it already is.
func (m Model) showMatch() Model {
	if m.finding.current < 0 || m.finding.current >= len(m.finding.matches) {
		return m
	}
	line := m.finding.matches[m.finding.current].Line
	top, height := m.viewport.YOffset, m.viewport.Height
	if line >= top && line < top+height {
		return m
	}
	m.viewport.SetYOffset(line - height/2)
	return m
}

// paintMatches marks every match on its row, the current one in inverse and
// the rest underlined, the same way paintSelection marks a selection.
func (m Model) paintMatches(lines []string) []string {
	if !m.finding.open {
		return lines
	}
	return ui.PaintMatches(lines, m.finding.matches, m.finding.current, 0, selectStyle, findMatchStyle)
}

// findView is the box: the query with a cursor, where the reader stands among
// the matches, and the keys.
func (m Model) findView() string {
	var where string
	switch {
	case m.finding.query == "":
		where = helpStyle.Render("type to search the conversation")
	case len(m.finding.matches) == 0:
		where = errorStyle.Render("no matches")
	default:
		where = flagStyle.Render(fmt.Sprintf("%d of %d", m.finding.current+1, len(m.finding.matches)))
	}
	line := permKeyStyle.Render("find") + " › " + m.finding.query + lipgloss.NewStyle().Reverse(true).Render(" ") +
		"   " + where + helpStyle.Render("   ctrl+n next · ctrl+p previous · esc close")
	return "\n" + line
}

// findHeight is the footer space the box takes: the blank above it and the
// line itself.
func (m Model) findHeight() int { return 2 }
