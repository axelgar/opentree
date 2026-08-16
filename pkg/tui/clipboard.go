package tui

import (
	"context"
	"errors"
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

	var tools [][]string
	switch runtime.GOOS {
	case "darwin":
		tools = [][]string{{"pbcopy"}}
	case "linux":
		// Wayland first, then X11. Whichever is not in use is usually not
		// installed either, so this is one failed exec at worst.
		tools = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}}
	}

	for _, tool := range tools {
		if _, err := exec.LookPath(tool[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, tool[0], tool[1:]...) // #nosec G204 -- fixed command names
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return err
		}
		return nil
	}
	return errNoClipboardTool
}
