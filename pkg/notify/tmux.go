package notify

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The in-tmux surface, and why it is one byte rather than a marked window name.
//
// tmux already has a marker for "this window wants you": monitor-bell is on by
// default and window-status-bell-style is reverse, so a bell delivered into a
// pane renders its window inverted in the status bar until the window is
// selected. No configuration, no new state, and it clears itself — in a status
// bar the user configured themselves.
//
// Renaming the window instead was the original idea and is not available: every
// tmux operation opentree performs resolves a window by exact name — attach,
// select, kill, activity, pane command, and the dashboard's own row-to-window
// join — so a marked name is a window opentree can no longer find, silently.

// Pane is the tmux pane this process is running in, or "" when it is not
// running in one.
//
// Outside tmux nothing is notified at all: no TMUX_PANE means somebody ran
// `opentree chat` by hand, in a terminal they are sitting in front of, and
// guessing loudly is worse than staying quiet.
func Pane() string { return os.Getenv("TMUX_PANE") }

// queryTimeout bounds the visibility question. It is asked once per transition
// rather than per frame, but a tmux server that has wedged must not take the
// chat down with it.
const queryTimeout = 2 * time.Second

// Watched reports whether somebody is looking at this pane's window right now:
// the window is the current one *and* its session has a client attached.
//
// Both halves are needed. An unattached session still has a current window, so
// the first question alone would suppress every notification from a workspace
// nobody has looked at all day.
func Watched(pane string) bool {
	if pane == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane,
		"#{window_active}|#{session_attached}").Output()
	if err != nil {
		return false
	}
	return watchedFrom(out)
}

// watchedFrom reads tmux's answer.
//
// Split from the call so it can be tested without a tmux, real or faked. Both
// ways of misreading this are silent — too loose and every workspace nobody has
// opened all day counts as watched, so no notification ever arrives; too strict
// and the chat rings the window you are sitting in front of — and neither shows
// up as an error, so the parse is worth pinning on its own.
func watchedFrom(out []byte) bool {
	active, attached, ok := strings.Cut(strings.TrimSpace(string(out)), "|")
	// session_attached is a count of clients rather than a flag.
	return ok && active == "1" && attached != "" && attached != "0"
}

// Bell is the tmux surface: one byte, into the pane's own terminal.
//
// It goes to /dev/tty rather than the chat's stdout because Bubble Tea owns
// stdout and renders whole frames from its own loop — a notifier writing into
// that stream is a race for no gain. /dev/tty is the same device opened
// separately, and the pane's process is the chat itself, so it is the pane's
// terminal by construction.
type Bell struct{}

// Send rings the window. Every event rings the same way: which one it was is a
// question for the dashboard, and a bell has no room to answer it.
func (Bell) Send(Event) {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString("\a")
}
