package notify

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// The out-of-terminal surface, for the case the bell cannot reach: the terminal
// is behind a browser, or closed.
//
// Shelling out to the platform's own tool is the shape pkg/chat's clipboard
// already established — bounded by a context, best-effort, and with Windows
// absent because opentree needs tmux.

// sendTimeout bounds one delivery. osascript can take a moment on a cold run
// and notify-send can block on a bus that is not there; neither may hold up the
// chat that asked.
const sendTimeout = 3 * time.Second

// Desktop is the OS notification surface: a banner that outlives the terminal
// it came from.
//
// It is a signpost and not a button. Attaching an action to an osascript
// notification means bundling an app or making the user install
// terminal-notifier, so nothing here pretends to be clickable — the action is
// the dashboard's b key, one keystroke away.
type Desktop struct{}

// Send raises the banner, and says nothing about whether it worked. A machine
// without notify-send, or a macOS that has not yet been asked to allow
// notifications, is a surface that is not there — which is what the tmux bell
// and `opentree notify test` are for.
func (Desktop) Send(ev Event) {
	title, body := ev.Text()
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s",
			appleString(body), appleString("opentree · "+title))
		_ = exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		_ = exec.CommandContext(ctx, "notify-send", "opentree · "+title, body).Run()
	}
}

// appleString quotes a Go string as an AppleScript one. A workspace name or a
// tool call is the user's own text and arrives here unexamined, so the quoting
// is the only thing between a branch named `"` and a script that does not run.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Text is what a surface says about an event: the workspace it happened in, and
// one line naming the moment. The body says what happened rather than what to
// do — a notification you cannot act on from where it appears should not be
// telling you to.
func (e Event) Text() (title, body string) {
	switch e.Kind {
	case Blocked:
		body = "needs permission"
		if e.Detail != "" {
			body += ": " + e.Detail
		}
	case Done:
		body = "turn finished"
		if e.Elapsed >= time.Second {
			body += " in " + compact(e.Elapsed)
		}
	case Stopped:
		body = "agent stopped"
		if e.Detail != "" {
			body = e.Detail
		}
	}
	return e.Workspace, body
}

// compact is an elapsed time in one word: 40s, 12m, 3h.
func compact(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// Senders is every surface at once. One that fails costs the others nothing,
// because none of them report a failure — a notification is best-effort by
// nature, and there is nobody to tell.
type Senders []Sender

func (ss Senders) Send(ev Event) {
	for _, s := range ss {
		s.Send(ev)
	}
}
