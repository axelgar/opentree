// Package ui holds the text primitives opentree's two terminal programs — the
// workspace list and the chat — have to agree on. Styles stay duplicated per
// program on purpose (see pkg/chat/styles.go), but these are behaviour, and
// copies of behaviour drift: three truncators had drifted into three different
// answers for a narrow width before they were gathered here.
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
