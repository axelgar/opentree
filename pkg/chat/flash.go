package chat

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A flash is a line on the status bar that says what just happened — "copied
// the last reply" — and goes away by itself. The log is for what was said;
// a notice there for every copy would bury the conversation under its own
// receipts. m.err is the other candidate, and it means "the last turn
// failed": it comes with a retry hint that would offer to resend a message
// because a clipboard tool was missing.
type flash struct {
	text   string
	failed bool

	// seq names the flash the timer was started for, so a tick from a flash
	// that has since been replaced does not clear its replacement early.
	seq int
}

// flashClearMsg is the timer running out.
type flashClearMsg struct{ seq int }

// flashFor is how long a flash stays. Long enough to read a line, short
// enough that the help is back before anyone wonders where it went.
const flashFor = 3 * time.Second

// withFlash shows text on the status line and starts the clock on it.
func (m Model) withFlash(text string, failed bool) (Model, tea.Cmd) {
	m.flash = flash{text: text, failed: failed, seq: m.flash.seq + 1}
	seq := m.flash.seq
	return m, tea.Tick(flashFor, func(time.Time) tea.Msg { return flashClearMsg{seq: seq} })
}

// clearFlash takes a flash down when its own timer, and only its own, runs
// out.
func (m Model) clearFlash(msg flashClearMsg) Model {
	if msg.seq == m.flash.seq {
		m.flash = flash{seq: m.flash.seq}
	}
	return m
}
