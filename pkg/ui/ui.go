// Package ui holds what opentree's two terminal programs — the workspace list
// and the chat — have to agree on: the text primitives, and the palette.
//
// Styles stay duplicated per program on purpose (see pkg/chat/styles.go). These
// are not styles. They are behaviour and values, and copies of both drift:
// three truncators had drifted into three different answers for a narrow width
// before they were gathered here, and ninety-seven colours had been mirrored by
// hand between the two programs, agreeing by coincidence and agreeing with a
// light terminal not at all.
package ui

import "github.com/charmbracelet/lipgloss"

// Truncate shortens plain text to width, marking the cut. Never call it on
// styled text: slicing runes through an escape sequence would corrupt it.
func Truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width < 1 {
		return ""
	}
	if width == 1 {
		return "…"
	}
	return string([]rune(s)[:width-1]) + "…"
}

// SpinnerFrames is the one spinner both programs draw, so a wait looks the
// same wherever it happens.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
