// Package clipboard puts text on the system clipboard, from wherever opentree
// happens to be running.
//
// Two routes, tried in order. The platform's own tool — pbcopy, wl-copy,
// xclip — is the one that works on the machine in front of you, and the one
// that works without asking the terminal anything. OSC 52 is the escape
// sequence a terminal accepts to set the clipboard of the machine it is
// drawing on, which is the answer over ssh, where the clipboard that matters
// is on a different computer from the one running the tool, and on a Linux
// box with no display for xclip to reach. It is the fallback rather than the
// default because iTerm2 asks for permission the first time it arrives, and
// on a laptop with pbcopy sitting right there that question has no reason to
// be put.
//
// Windows is absent for the same reason it is everywhere else: opentree needs
// tmux.
package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// timeout bounds the write. A clipboard tool that blocks forever would leave
// the copy key looking like a key that does nothing.
const timeout = 3 * time.Second

// osc52Max caps what goes out as an escape sequence. Terminals put their own
// ceiling on the payload — xterm's is a hundred thousand bytes — and past it
// the sequence is dropped whole, which reads as a copy that worked and pasted
// nothing. Anything larger is what the platform tool and the export command
// are for.
const osc52Max = 100_000

// ErrNoTool is the honest answer on a machine with nothing to copy with and
// no terminal to hand the text to, rather than a silent success that leaves
// the user pasting whatever was on the clipboard before.
var ErrNoTool = errors.New("no clipboard tool found (install wl-clipboard or xclip)")

// terminal is where OSC 52 goes. A variable so a test can read what was sent
// without a pty; everything else writes to the real one.
var terminal io.Writer = os.Stdout

// Write puts text on the system clipboard.
func Write(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := writeWith(ctx, tools(), text)
	if err == nil {
		return nil
	}
	if writeOSC52(text) {
		return nil
	}
	return err
}

// tools is the ordered list of candidate tools for this platform. Anything not
// listed here has none, and says so.
var tools = func() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "linux":
		// Wayland first, then X11. Both are commonly installed together, and
		// wl-copy under an X session with WAYLAND_DISPLAY unset exits non-zero
		// rather than declining to exist — which is why a tool that fails is a
		// reason to try the next one rather than to give up.
		return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}}
	}
	return nil
}

// writeWith feeds text to the first tool that is installed and succeeds. The
// whole list shares one deadline rather than one each: the point of the budget
// is that the copy key answers quickly, and two tools hanging for three seconds
// apiece is not quicker than one.
func writeWith(ctx context.Context, tools [][]string, text string) error {
	var lastErr error
	for _, tool := range tools {
		if _, err := exec.LookPath(tool[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, tool[0], tool[1:]...) // #nosec G204 -- fixed command names
		cmd.Stdin = strings.NewReader(text)
		// The tool's own complaint is the only clue about why it refused, and
		// it must be captured rather than inherited: both callers own the
		// alternate screen, so a stray line from wl-copy paints over whatever
		// row it lands on and stays there until the next full redraw.
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("%s: %w", tool[0], err)
			if reason, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); reason != "" {
				lastErr = fmt.Errorf("%s: %w: %s", tool[0], err, reason)
			}
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return ErrNoTool
}

// writeOSC52 hands the text to the terminal, and reports whether there was one
// to hand it to. The sequence is OSC 52 with the "c" selection — the
// clipboard, as opposed to the primary selection — and the text base64-encoded,
// which is the only shape every terminal that supports it agrees on. tmux
// forwards it to the terminal outside unless its set-clipboard option is off,
// so a chat in a tmux window reaches the same clipboard a shell would.
//
// Whether the terminal honoured it is unknowable from here: the sequence has
// no reply. Not a terminal at all — output redirected into a file — is the
// one case that can be told apart, and a sequence written into a file is
// litter, so that one is refused.
func writeOSC52(text string) bool {
	if text == "" || !terminalWritable() {
		return false
	}
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	if len(payload) > osc52Max {
		return false
	}
	_, err := io.WriteString(terminal, "\x1b]52;c;"+payload+"\x07")
	return err == nil
}

// terminalWritable reports whether the OSC 52 route leads anywhere: a real
// terminal, or a test's buffer standing in for one.
func terminalWritable() bool {
	f, ok := terminal.(*os.File)
	if !ok {
		return terminal != nil
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
