package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	// dialogMinWidth stops a one-line question from rendering as a tooltip.
	dialogMinWidth = 50

	// dialogPadding is the card's interior breathing room, per side.
	dialogPadding = 2
)

var (
	dialogAccent = lipgloss.Color("#F4A261")
	dialogDanger = lipgloss.Color("196")
)

// dialogCard is the one shape every takeover dialog wears: a rounded card,
// centred in the terminal, with the title chip on top and the keys that answer
// it on the bottom. Each of these used to be left-aligned text in an otherwise
// black screen, which reads as the program having lost its window rather than
// as the program asking a question — and answering "install this?" looked
// nothing like answering "delete that?".
//
// The delete dialog keeps its red border; everything else is the accent.
func (m Model) dialogCard(title, body, footer string, border lipgloss.Color) string {
	// A destructive card wears its border colour in the chip too: the border is
	// two lines away from the sentence that says what will be removed.
	chip := titleStyle
	if border == dialogDanger {
		chip = dangerTitleStyle
	}

	parts := []string{chip.Render(title), "", body}
	if footer != "" {
		parts = append(parts, "", footer)
	}
	content := strings.Join(parts, "\n")

	// Width() is the padded interior, not the text column, so the padding is
	// added back here — sizing to the bare text width would wrap every line.
	width := min(max(lipgloss.Width(content)+2*dialogPadding, dialogMinWidth), m.dialogMaxWidth())

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(1, dialogPadding).
		Width(width).
		Render(content)

	// Before the first WindowSizeMsg there is no screen to centre inside.
	if m.width <= 0 || m.height <= 0 {
		return appStyle.Render(card)
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}

// dialogMaxWidth leaves room for the border and a margin either side, so a
// long agent description widens the card instead of overflowing the terminal.
func (m Model) dialogMaxWidth() int {
	if m.width <= 0 {
		return defaultPanelWidth
	}
	return max(m.width-8, minPanelWidth)
}

// bar spreads a chrome line's two ends across the screen. A header or footer
// reads as a bar when it has ends; the diff view's key hints used to be one
// long sentence that wrapped onto a second line at ordinary widths.
func (m Model) bar(left, right string) string {
	gap := m.chromeWidth() - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// chromeWidth is the width the bars and rules span: the screen less appStyle's
// horizontal padding.
func (m Model) chromeWidth() int {
	return max(m.width-4, minPanelWidth)
}
