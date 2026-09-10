package tui

// Notes on the diff. Reading a diff and having something to say about a line
// used to mean leaving the view, opening the chat and describing the line
// from memory. A note is made where the cursor is, on the line or on the
// file, and s sends every note to the workspace's agent as one prompt: the
// path, the line, the code quoted, and what was said about it — phrased as a
// question when it was one.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
)

// annotation is one note: where it was made, what was under it, what it says.
type annotation struct {
	// row is the row the note is on, -1 for a note on the whole file.
	row  int
	file int
	path string
	// line is the new line number, or the old one for a removed line; 0
	// for a file note.
	line int
	kind byte // rowAdd, rowDel, rowContext, or 'f' for the file
	code string
	note string
}

// noteAt is the index of the note on a row, or -1.
func (d diffView) noteAt(row int) int {
	for i, n := range d.notes {
		if n.row == row {
			return i
		}
	}
	return -1
}

// fileNoteAt is the index of the note on a whole file, or -1.
func (d diffView) fileNoteAt(file int) int {
	for i, n := range d.notes {
		if n.row == -1 && n.file == file {
			return i
		}
	}
	return -1
}

// noteFor is a fresh note on a code row.
func (d diffView) noteFor(row int) annotation {
	r := d.rows[row]
	line := r.newNo
	if r.kind == rowDel {
		line = r.oldNo
	}
	return annotation{row: row, file: r.file, path: d.files[r.file].path, line: line, kind: r.kind, code: r.text}
}

// startNote opens the footer box on a note, with its text when it already
// has some: a second a on a noted line edits rather than doubles.
func (m Model) startNote(a annotation) (tea.Model, tea.Cmd) {
	if i := m.diff.noteIndex(a); i >= 0 {
		a.note = m.diff.notes[i].note
	}
	m.diff.noting = &a
	m.input.Reset()
	m.input.SetValue(a.note)
	m.input.Placeholder = "what about this line? end with ? to ask"
	m.input.Focus()
	return m, textinput.Blink
}

// noteIndex is where a note on the same spot already sits, or -1.
func (d diffView) noteIndex(a annotation) int {
	if a.row < 0 {
		return d.fileNoteAt(a.file)
	}
	return d.noteAt(a.row)
}

// handleNoteKey types into the note box. Enter keeps the note — or, emptied,
// removes it; esc leaves it as it was.
func (m Model) handleNoteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.diff
	switch msg.String() {
	case "esc":
		d.noting = nil
		m.input.Reset()
		return m, nil
	case "enter":
		a := *d.noting
		a.note = strings.TrimSpace(m.input.Value())
		d.noting = nil
		m.input.Reset()
		if i := d.noteIndex(a); i >= 0 {
			if a.note == "" {
				d.notes = append(d.notes[:i], d.notes[i+1:]...)
			} else {
				d.notes[i] = a
			}
		} else if a.note != "" {
			d.notes = append(d.notes, a)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleNoteListKey drives the @ card: j/k walk the notes, enter goes to one,
// x removes one, esc closes.
func (m Model) handleNoteListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.diff
	switch msg.String() {
	case "esc", "@", "q":
		d.listing = false
	case "down", "j":
		d.listCursor = min(d.listCursor+1, len(d.notes)-1)
	case "up", "k":
		d.listCursor = max(d.listCursor-1, 0)
	case "enter":
		d.listing = false
		if d.listCursor < len(d.notes) {
			n := d.notes[d.listCursor]
			row := n.row
			if row < 0 {
				row = d.files[n.file].first
			}
			m.jumpDiff(row)
		}
	case "x":
		if d.listCursor < len(d.notes) {
			d.notes = append(d.notes[:d.listCursor], d.notes[d.listCursor+1:]...)
			d.listCursor = min(d.listCursor, max(len(d.notes)-1, 0))
			d.listing = len(d.notes) > 0
		}
	}
	return m, nil
}

// noteListCard is the @ card: every note, where it is, what it says.
func (m Model) noteListCard() string {
	d := m.diff
	var sb strings.Builder
	for i, n := range d.notes {
		mark := "  "
		if i == d.listCursor {
			mark = diffCursorStyle.Render("▸ ")
		}
		fmt.Fprintf(&sb, "%s%s  %s\n", mark, diffFileStyle.Render(n.where()), n.note)
	}
	return m.dialogCard(fmt.Sprintf("Notes (%d)", len(d.notes)), strings.TrimRight(sb.String(), "\n"),
		dialogHintStyle.Render("↑/↓ move  •  enter jump  •  x delete  •  esc close"), dialogAccent)
}

// where is a note's place as the prompt names it: path:line and the sign,
// or the path alone for a file note.
func (a annotation) where() string {
	switch a.kind {
	case 'f':
		return a.path + " (whole file)"
	case rowAdd:
		return fmt.Sprintf("%s:%d (+)", a.path, a.line)
	case rowDel:
		return fmt.Sprintf("%s:%d (-)", a.path, a.line)
	default:
		return fmt.Sprintf("%s:%d", a.path, a.line)
	}
}

// isQuestion tells a note that asks from one that instructs: it ends in a
// question mark, or carries the ?? that marks a question anywhere.
func isQuestion(note string) bool {
	return strings.HasSuffix(note, "?") || strings.Contains(note, "??")
}

// formatAnnotationsPrompt is the notes as one message to the agent. Not the
// PR-review prompt: that one says "review comments to address", which these
// are not, and it has no line to quote.
func formatAnnotationsPrompt(branch string, notes []annotation) string {
	questions := 0
	for _, n := range notes {
		if isQuestion(n.note) {
			questions++
		}
	}
	var ask string
	switch {
	case questions == 0:
		ask = "Please make these changes."
	case questions == len(notes):
		ask = "Please answer these questions."
	default:
		ask = "Answer the questions; make the other changes."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "I reviewed the diff on %s and left %s. %s\n", branch, plural(len(notes), "note"), ask)
	for i, n := range notes {
		fmt.Fprintf(&sb, "\n%d. %s\n", i+1, n.where())
		if n.kind != 'f' {
			fmt.Fprintf(&sb, "   > %s\n", n.code)
		}
		if isQuestion(n.note) {
			fmt.Fprintf(&sb, "   Question: %s\n", n.note)
		} else {
			fmt.Fprintf(&sb, "   %s\n", n.note)
		}
	}
	return sb.String()
}

// sendNotes hands every note to the workspace's agent, the way R hands it the
// PR's review comments, and clears them on the way. A group comparison has
// three agents and one prompt, and no right answer about who gets it.
func (m Model) sendNotes() (tea.Model, tea.Cmd) {
	d := &m.diff
	if len(d.notes) == 0 {
		return m, m.transientErrCmd("no notes to send — a or enter makes one")
	}
	i := m.workspaceIndex(d.wsName)
	if i < 0 {
		return m, m.transientErrCmd("notes can only be sent from one workspace's diff")
	}
	ws := m.workspaces[i]
	if reason := ws.chatUnavailable(); reason != "" {
		return m, m.transientErrCmd(reason)
	}
	prompt := formatAnnotationsPrompt(ws.Branch, d.notes)
	action := "sent " + plural(len(d.notes), "note")
	d.notes = nil
	d.closeArmed = false
	return m, m.sendAgentCommand(ws.Name, action, chat.Command{Type: chat.CommandPrompt, Text: prompt})
}
