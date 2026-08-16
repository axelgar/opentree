package chat

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/ui"
)

// The chat's pickers — settings, /resume, login — are one component, drawn
// and driven here. A titled list of rows with a cursor on one of them is the
// same thing wherever it appears, and a third copy of the keys or the layout
// would be a third thing to keep in step: the first three had already copied
// the same thirty-line switch before this file existed.

// pickerWindow caps how many rows a picker shows at once. Model lists run to
// thirty entries and the footer cannot grow to meet them.
const pickerWindow = 8

// pickerAction is what a keystroke asked of a picker.
type pickerAction int

const (
	pickerIgnored pickerAction = iota // not a picker key; the caller decides
	pickerClosed                      // esc or ctrl+c — what closing means is the caller's
	pickerMoved                       // the cursor was stepped in place
	pickerChose                       // enter or a digit; the index says which row
)

// pickerKey folds the keys every picker shares: esc closes, ↑/ctrl+p and
// ↓/ctrl+n wrap the cursor, enter takes the row under it, a digit takes a row
// by position. What choosing or closing does stays with each picker — only
// the keys are shared, so the three cannot drift apart on them.
func pickerKey(msg tea.KeyMsg, cursor *int, rows int) (pickerAction, int) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return pickerClosed, 0
	case "up", "ctrl+p":
		if rows > 0 {
			*cursor = (*cursor - 1 + rows) % rows
		}
		return pickerMoved, 0
	case "down", "ctrl+n":
		if rows > 0 {
			*cursor = (*cursor + 1) % rows
		}
		return pickerMoved, 0
	case "enter":
		return pickerChose, *cursor
	}
	if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 && n <= rows {
		return pickerChose, n - 1
	}
	return pickerIgnored, 0
}

// pickerView renders a titled list into the footer, scrolled to keep the
// cursor visible.
func pickerView(title string, rows []completionItem, cursor, width int) string {
	start := 0
	if cursor >= pickerWindow {
		start = cursor - pickerWindow + 1
	}
	end := min(start+pickerWindow, len(rows))

	lines := []string{permLabelStyle.Render(title)}
	for i := start; i < end; i++ {
		mark, style := "  ", completionItemStyle
		if i == cursor {
			mark, style = "› ", completionSelectedStyle
		}
		row := mark + rows[i].value
		if rows[i].desc != "" {
			row += "  " + rows[i].desc
		}
		lines = append(lines, style.Render(ui.Truncate(row, width-6)))
	}
	if len(rows) > pickerWindow {
		lines = append(lines, noticeStyle.Render(fmt.Sprintf("  %d of %d", cursor+1, len(rows))))
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(permBoxStyle.Render(strings.Join(lines, "\n")))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ move • enter select • esc back"))
	return b.String()
}

// pickerHeight is the footer space a picker needs.
func pickerHeight(rows int) int {
	if rows > pickerWindow {
		return pickerWindow + 6 // plus the "n of N" counter
	}
	return rows + 5
}
