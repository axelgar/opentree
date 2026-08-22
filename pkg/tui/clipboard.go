package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// clipboardTimeout bounds the write. A clipboard tool that blocks forever would
// leave the copy key looking like a key that does nothing.
const clipboardTimeout = 3 * time.Second

// errNoClipboardTool is the honest answer on a machine with nothing to copy
// with, rather than a silent success that leaves the user pasting whatever was
// on the clipboard before.
var errNoClipboardTool = errors.New("no clipboard tool found (install wl-clipboard or xclip)")

// copyToClipboard puts text on the system clipboard.
//
// ponytail: shelling out to the platform's own tool, the same choice
// pkg/chat/clipboard.go makes for reading images back off it. An in-process
// library wants cgo and X11 headers on Linux, and this is twenty lines.
// Windows is absent for the same reason it is there: opentree needs tmux.
func copyToClipboard(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	return copyWith(ctx, clipboardTools(), text)
}

// clipboardTools is the ordered list of candidate tools for this platform.
// Anything not listed here has none, and says so.
func clipboardTools() [][]string {
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

// copyWith feeds text to the first tool that is installed and succeeds. The
// whole list shares one deadline rather than one each: the point of the budget
// is that the copy key answers quickly, and two tools hanging for three seconds
// apiece is not quicker than one.
func copyWith(ctx context.Context, tools [][]string, text string) error {
	var lastErr error
	for _, tool := range tools {
		if _, err := exec.LookPath(tool[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, tool[0], tool[1:]...) // #nosec G204 -- fixed command names
		cmd.Stdin = strings.NewReader(text)
		// The tool's own complaint is the only clue about why it refused, and
		// it must be captured rather than inherited: the dashboard owns the
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
	return errNoClipboardTool
}
