package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// clipboardTimeout bounds the helper. A clipboard read that never returns would
// leave ctrl+v looking like a key that does nothing, forever.
const clipboardTimeout = 3 * time.Second

// clipboardImage returns an image from the system clipboard, or ok=false when
// the clipboard holds no image — which is the ordinary case, since ctrl+v is
// also how text is pasted.
//
// ponytail: shelling out to the platform's own tool. golang.design/x/clipboard
// does this in-process, but it wants cgo and X11 headers on Linux, and this is
// thirty lines. Windows is absent because opentree needs tmux.
func clipboardImage() ([]byte, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()

	switch runtime.GOOS {
	case "darwin":
		return pasteboardImage(ctx)
	case "linux":
		// Wayland first, then X11. Whichever is not in use is usually not
		// installed either, so this is one failed exec at worst.
		if data, ok := run(ctx, "wl-paste", "--type", "image/png"); ok {
			return data, sniffBytes(data), true
		}
		if data, ok := run(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o"); ok {
			return data, sniffBytes(data), true
		}
	}
	return nil, "", false
}

// pasteboardImage asks AppleScript for the clipboard as a PNG. pbpaste is
// text-only and image bytes do not survive osascript's stdout, so the script
// writes the file and Go reads it back.
//
// A clipboard with no image makes the coercion fail, which is the ok=false
// path rather than an error worth showing.
func pasteboardImage(ctx context.Context) ([]byte, string, bool) {
	f, err := os.CreateTemp("", "opentree-paste-*.png")
	if err != nil {
		return nil, "", false
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()

	script := fmt.Sprintf(`set f to open for access POSIX file %q with write permission
set eof f to 0
write (the clipboard as «class PNGf») to f
close access f`, name)
	if err := exec.CommandContext(ctx, "osascript", "-e", script).Run(); err != nil {
		return nil, "", false
	}

	data, err := os.ReadFile(name) // #nosec G304 -- a temp file this function just created
	if err != nil || len(data) == 0 {
		return nil, "", false
	}
	return data, sniffBytes(data), true
}

func run(ctx context.Context, name string, args ...string) ([]byte, bool) {
	out, err := exec.CommandContext(ctx, name, args...).Output() // #nosec G204 -- fixed command names
	if err != nil || len(out) == 0 {
		return nil, false
	}
	return out, true
}
