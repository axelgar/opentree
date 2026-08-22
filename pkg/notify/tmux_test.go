package notify

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeTmux drops a shell script named tmux at the front of PATH, so Watched
// asks it instead of the real thing.
//
// It takes a whole script body rather than a line of canned output because half
// the cases here are about how tmux exits rather than what it prints — a pane
// that has been closed is a non-zero exit, not an empty answer. PATH is
// prepended rather than replaced so the fake only shadows tmux; a test binary
// with nothing else on PATH is a different experiment.
func fakeTmux(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestWatchedFrom pins how tmux's answer is read. Both ways of getting it
// wrong are silent: read too loosely and every workspace nobody has looked at
// all day is treated as watched, so no notification ever arrives; read too
// strictly and the chat rings the window you are sitting in front of. Neither
// shows up as an error, which is why the parse is nailed down here.
//
// Against the bytes rather than against a fake tmux on PATH: nine of these
// used to spawn a shell script apiece, each racing the two-second timeout
// Watched imposes on the real thing, and a loaded machine lost that race. The
// exec path keeps the two tests below, which are about how tmux exits rather
// than what it prints.
func TestWatchedFrom(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{
			"the current window of a session somebody is attached to",
			"1|1\n",
			true,
		},
		{
			// session_attached is a count, so two terminals on the same
			// session is still somebody looking.
			"several clients attached count the same as one",
			"1|3\n",
			true,
		},
		{
			"the pane is there but its window is not the one on screen",
			"0|1\n",
			false,
		},
		{
			// The half that matters most: an unattached session still has a
			// current window, so window_active alone would suppress every
			// notification from a workspace nobody has opened.
			"the current window of a session with no client attached",
			"1|0\n",
			false,
		},
		{
			"an empty attachment count is not an attachment",
			"1|\n",
			false,
		},
		{
			"an answer with no separator in it",
			"1\n",
			false,
		},
		{
			// A tmux too old to know a format leaves it unexpanded rather than
			// failing, so the answer arrives looking entirely plausible.
			"a format tmux did not expand",
			"#{window_active}|#{session_attached}\n",
			false,
		},
		{
			"tmux said nothing at all",
			"",
			false,
		},
		{
			"whitespace around an otherwise good answer",
			"  1|1  \n",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := watchedFrom([]byte(tt.out)); got != tt.want {
				t.Errorf("watchedFrom(%q) = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}

// A pane tmux cannot describe is a closed window, and nobody is looking at it.
// This one keeps the fake on PATH because a non-zero exit is the behaviour
// under test, not the output.
func TestWatched_APaneTmuxCannotFind(t *testing.T) {
	fakeTmux(t, `echo "can't find pane: %99" >&2; exit 1`)
	if Watched("%1") {
		t.Error("Watched = true for a pane tmux could not find")
	}
}

// TestWatched_WithoutAPaneNothingIsAsked covers the case the chat is in when
// somebody has run it by hand: no TMUX_PANE, nothing to be looking at, and no
// reason to shell out once per transition to be told so.
func TestWatched_WithoutAPaneNothingIsAsked(t *testing.T) {
	asked := filepath.Join(t.TempDir(), "asked")
	fakeTmux(t, "echo yes > "+asked+"\necho '1|1'")

	if Watched("") {
		t.Error("Watched(\"\") = true, want false — outside tmux nobody is looking at anything")
	}
	if _, err := os.Stat(asked); err == nil {
		t.Error("Watched asked tmux about a pane it had already been told does not exist")
	}
}

// TestWatched_WithoutTmuxOnPath is the machine where the chat was started
// outside tmux but TMUX_PANE was inherited from somewhere, or where tmux has
// been removed under a running session. Nobody can be looking at a pane no
// tmux can describe, and the notification goes out.
func TestWatched_WithoutTmuxOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if Watched("%1") {
		t.Error("Watched = true with no tmux to have asked")
	}
}

// TestWatched_AWedgedTmuxDoesNotHoldUpTheChat is what queryTimeout is for. The
// question is asked from the chat's own update loop, so a tmux server that has
// stopped answering must cost one bounded pause rather than the session. The
// two seconds this test spends are its whole point: drop the context from the
// exec and it stops returning instead of failing.
func TestWatched_AWedgedTmuxDoesNotHoldUpTheChat(t *testing.T) {
	// exec, so that killing the process replaces the sleep rather than
	// forking one: a forked child would inherit the stdout pipe and keep it
	// open long after the context had killed its parent.
	fakeTmux(t, "exec sleep 30")

	answered := make(chan bool, 1)
	go func() { answered <- Watched("%1") }()
	select {
	case watched := <-answered:
		if watched {
			t.Error("a tmux that never answered reported the pane as watched")
		}
	case <-time.After(4 * queryTimeout):
		t.Fatal("Watched did not return: the query is not bounded by queryTimeout")
	}
}

// TestPane is one line of production code and a whole feature: the wrong
// variable name here switches every tmux bell off, in every session, without
// anything failing.
func TestPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "%7")
	if got := Pane(); got != "%7" {
		t.Errorf("Pane() = %q, want the pane tmux put in the environment", got)
	}
	t.Setenv("TMUX_PANE", "")
	if got := Pane(); got != "" {
		t.Errorf("Pane() = %q outside tmux, want the empty string that switches the bell off", got)
	}
}
