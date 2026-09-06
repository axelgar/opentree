package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prependPath puts a fresh directory at the front of PATH and returns it, so a
// stub is found where the real tool would be. Prepended rather than replaced:
// the stubs below still shell out to /bin/sh.
func prependPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// fakeTool writes an executable stub named after a clipboard tool. The names
// used here are deliberately not the real ones, so a machine that happens to
// have wl-copy or xclip installed cannot change the answer.
func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
}

// captureTerminal swaps the OSC 52 sink for a buffer and returns it.
func captureTerminal(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := terminal
	terminal = &buf
	t.Cleanup(func() { terminal = prev })
	return &buf
}

// TestWriteWith_FallsThroughToTheNextTool: the first tool exiting non-zero used
// to end the whole attempt. On a desktop that ships both, wl-copy is installed
// but refuses under an X session, so the copy key reported failure with xclip
// sitting right there unused.
func TestWriteWith_FallsThroughToTheNextTool(t *testing.T) {
	dir := prependPath(t)
	pasted := filepath.Join(t.TempDir(), "pasted")
	fakeTool(t, dir, "ot-test-wl-copy", "echo 'no compositor' >&2\nexit 1\n")
	fakeTool(t, dir, "ot-test-xclip", "/bin/cat > '"+pasted+"'\n")

	tools := [][]string{{"ot-test-wl-copy"}, {"ot-test-xclip", "-selection", "clipboard"}}
	if err := writeWith(context.Background(), tools, "one\ntwo\n"); err != nil {
		t.Fatalf("writeWith: %v", err)
	}

	got, err := os.ReadFile(pasted)
	if err != nil {
		t.Fatalf("the second tool was never reached: %v", err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("clipboard got %q, want the text handed to writeWith", got)
	}
}

// TestWriteWith_EveryToolFailedReportsTheLast: with nothing left to try, the
// reason the last tool gave is the only clue there is — and it has to arrive in
// the error rather than on os.Stderr, which would paint over the alternate
// screen.
func TestWriteWith_EveryToolFailedReportsTheLast(t *testing.T) {
	dir := prependPath(t)
	fakeTool(t, dir, "ot-test-wl-copy", "exit 1\n")
	fakeTool(t, dir, "ot-test-xclip", "echo 'cannot open display' >&2\nexit 1\n")

	tools := [][]string{{"ot-test-wl-copy"}, {"ot-test-xclip", "-selection", "clipboard"}}
	err := writeWith(context.Background(), tools, "text")
	if err == nil {
		t.Fatal("writeWith = nil, want the last tool's failure")
	}
	if errors.Is(err, ErrNoTool) {
		t.Fatalf("err = %v, want the failure of a tool that is installed", err)
	}
	if !strings.Contains(err.Error(), "ot-test-xclip") {
		t.Errorf("err = %v, want it to name the tool that failed last", err)
	}
	if !strings.Contains(err.Error(), "cannot open display") {
		t.Errorf("err = %v, want the tool's own stderr folded in", err)
	}
}

// TestWriteWith_NothingInstalledNamesWhatToInstall: the wording is what the
// error log shows, and it is the only place the user is told what to install.
func TestWriteWith_NothingInstalledNamesWhatToInstall(t *testing.T) {
	prependPath(t)

	err := writeWith(context.Background(), [][]string{{"ot-test-absent-copy"}}, "text")
	if !errors.Is(err, ErrNoTool) {
		t.Fatalf("err = %v, want ErrNoTool", err)
	}
}

// TestWriteOSC52_EncodesForTheTerminal: the sequence is the whole contract with
// the terminal, so its shape is pinned — OSC, the clipboard selection, base64,
// BEL — rather than only "something was written".
func TestWriteOSC52_EncodesForTheTerminal(t *testing.T) {
	out := captureTerminal(t)

	if !writeOSC52("hello, clipboard\n") {
		t.Fatal("writeOSC52 = false with a terminal to write to")
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello, clipboard\n")) + "\x07"
	if out.String() != want {
		t.Errorf("terminal got %q, want %q", out.String(), want)
	}
}

// TestWriteOSC52_RefusesWhatTheTerminalWouldDrop: a payload past the terminal's
// ceiling is discarded silently on the other end, which would read as a copy
// that worked. Refusing here lets the caller report the tool's failure instead.
func TestWriteOSC52_RefusesWhatTheTerminalWouldDrop(t *testing.T) {
	out := captureTerminal(t)

	if writeOSC52(strings.Repeat("x", osc52Max)) {
		t.Fatal("writeOSC52 = true for a payload the terminal would drop")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %d bytes to the terminal, want nothing", out.Len())
	}
	if writeOSC52("") {
		t.Error("writeOSC52 = true for nothing at all")
	}
}

// TestWriteOSC52_NeedsATerminal: output redirected into a file is not a
// terminal, and an escape sequence written into it is litter.
func TestWriteOSC52_NeedsATerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	prev := terminal
	terminal = f
	t.Cleanup(func() { terminal = prev })

	if writeOSC52("text") {
		t.Fatal("writeOSC52 = true with a plain file as the terminal")
	}
}

// TestWrite_FallsBackToTheTerminal is the ssh case: no tool can reach the
// clipboard that matters, and the terminal is the only road there.
func TestWrite_FallsBackToTheTerminal(t *testing.T) {
	prependPath(t)
	out := captureTerminal(t)
	prevTools := tools
	tools = func() [][]string { return [][]string{{"ot-test-absent-copy"}} }
	t.Cleanup(func() { tools = prevTools })

	if err := Write("over ssh"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(out.String(), base64.StdEncoding.EncodeToString([]byte("over ssh"))) {
		t.Errorf("terminal got %q, want the text as OSC 52", out.String())
	}
}
