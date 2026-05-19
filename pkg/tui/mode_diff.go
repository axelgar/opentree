package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	headerFooterHeight = 8
	minDiffHeight      = 5
)

func updateDiff(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = ModeList
		m.diff = diffState{}
	case "up", "k":
		if m.diff.scrollOffset > 0 {
			m.diff.scrollOffset--
		}
	case "down", "j":
		availHeight := m.height - 8
		if availHeight < 5 {
			availHeight = 5
		}
		maxScroll := len(strings.Split(m.diff.content, "\n")) - availHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.diff.scrollOffset < maxScroll {
			m.diff.scrollOffset++
		}
	}
	return m, nil
}

func viewDiff(m Model) string {
	lines := strings.Split(m.diff.content, "\n")
	availHeight := m.height - headerFooterHeight
	if availHeight < minDiffHeight {
		availHeight = minDiffHeight
	}
	maxScroll := len(lines) - availHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	// Clamp is authoritative in Update; this is a read-only safety for rendering.
	offset := m.diff.scrollOffset
	if offset > maxScroll {
		offset = maxScroll
	}
	end := offset + availHeight
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[offset:end]

	var sb strings.Builder
	for _, line := range visible {
		sb.WriteString(renderDiffLine(line))
		sb.WriteString("\n")
	}

	scrollInfo := fmt.Sprintf("line %d/%d", offset+1, len(lines))
	footer := fmt.Sprintf("%s  •  %s  •  %s",
		helpStyle.Render("↑/k ↓/j scroll"),
		helpStyle.Render("esc to close"),
		helpStyle.Render(scrollInfo),
	)
	content := fmt.Sprintf("%s\n\n%s\n%s",
		titleStyle.Render("Diff: "+m.diff.wsName),
		sb.String(),
		footer,
	)
	return appStyle.Render(content)
}
