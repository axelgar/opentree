package tui

import (
	"context"
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

// TestCopyWith_FallsThroughToTheNextTool: the first tool exiting non-zero used
// to end the whole attempt. On a desktop that ships both, wl-copy is installed
// but refuses under an X session, so the copy key reported failure with xclip
// sitting right there unused.
func TestCopyWith_FallsThroughToTheNextTool(t *testing.T) {
	dir := prependPath(t)
	pasted := filepath.Join(t.TempDir(), "pasted")
	fakeTool(t, dir, "ot-test-wl-copy", "echo 'no compositor' >&2\nexit 1\n")
	fakeTool(t, dir, "ot-test-xclip", "/bin/cat > '"+pasted+"'\n")

	tools := [][]string{{"ot-test-wl-copy"}, {"ot-test-xclip", "-selection", "clipboard"}}
	if err := copyWith(context.Background(), tools, "one\ntwo\n"); err != nil {
		t.Fatalf("copyWith: %v", err)
	}

	got, err := os.ReadFile(pasted)
	if err != nil {
		t.Fatalf("the second tool was never reached: %v", err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("clipboard got %q, want the text handed to copyWith", got)
	}
}

// TestCopyWith_EveryToolFailedReportsTheLast: with nothing left to try, the
// reason the last tool gave is the only clue there is — and it has to arrive in
// the error rather than on os.Stderr, which would paint over the dashboard's
// alternate screen.
func TestCopyWith_EveryToolFailedReportsTheLast(t *testing.T) {
	dir := prependPath(t)
	fakeTool(t, dir, "ot-test-wl-copy", "exit 1\n")
	fakeTool(t, dir, "ot-test-xclip", "echo 'cannot open display' >&2\nexit 1\n")

	tools := [][]string{{"ot-test-wl-copy"}, {"ot-test-xclip", "-selection", "clipboard"}}
	err := copyWith(context.Background(), tools, "text")
	if err == nil {
		t.Fatal("copyWith = nil, want the last tool's failure")
	}
	if errors.Is(err, errNoClipboardTool) {
		t.Fatalf("err = %v, want the failure of a tool that is installed", err)
	}
	if !strings.Contains(err.Error(), "ot-test-xclip") {
		t.Errorf("err = %v, want it to name the tool that failed last", err)
	}
	if !strings.Contains(err.Error(), "cannot open display") {
		t.Errorf("err = %v, want the tool's own stderr folded in", err)
	}
}

// TestCopyWith_NothingInstalledNamesWhatToInstall: the wording is what the
// error log shows, and it is the only place the user is told what to install.
func TestCopyWith_NothingInstalledNamesWhatToInstall(t *testing.T) {
	prependPath(t)

	err := copyWith(context.Background(), [][]string{{"ot-test-absent-copy"}}, "text")
	if !errors.Is(err, errNoClipboardTool) {
		t.Fatalf("err = %v, want errNoClipboardTool", err)
	}
}
