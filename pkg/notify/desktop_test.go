package notify

import (
	"strings"
	"testing"
	"time"
)

func TestEventText(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			"blocked names the question, which is the whole reason to get up",
			Event{Kind: Blocked, Workspace: "fix-auth", Detail: "rm -rf dist"},
			"needs permission: rm -rf dist",
		},
		{
			"blocked by an agent that did not say what it wants",
			Event{Kind: Blocked, Workspace: "fix-auth"},
			"needs permission",
		},
		{
			"done says how long you were away",
			Event{Kind: Done, Workspace: "fix-auth", Elapsed: 4 * time.Minute},
			"turn finished in 4m",
		},
		{
			// A turn shorter than a second is one whose length is not news.
			"done with nothing worth reporting",
			Event{Kind: Done, Workspace: "fix-auth", Elapsed: 200 * time.Millisecond},
			"turn finished",
		},
		{
			"a setup that failed says what failed",
			Event{Kind: Stopped, Workspace: "fix-auth", Detail: "fix-auth: pnpm install exited 1"},
			"fix-auth: pnpm install exited 1",
		},
		{
			"and an agent that simply died says that",
			Event{Kind: Stopped, Workspace: "fix-auth"},
			"agent stopped",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body := tt.event.Text()
			if title != "fix-auth" {
				t.Errorf("title = %q, want the workspace", title)
			}
			if body != tt.want {
				t.Errorf("body = %q, want %q", body, tt.want)
			}
		})
	}
}

// TestAppleString is the only thing between a branch named with a quote and an
// AppleScript that will not compile. Every part of an event's text is the
// user's own — a workspace name, a tool call — and none of it is examined
// before it gets here.
func TestAppleString(t *testing.T) {
	tests := []struct{ in, want string }{
		{`rm -rf dist`, `"rm -rf dist"`},
		{`say "hello"`, `"say \"hello\""`},
		{`C:\path`, `"C:\\path"`},
		{`\"`, `"\\\""`},
	}
	for _, tt := range tests {
		if got := appleString(tt.in); got != tt.want {
			t.Errorf("appleString(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// TestSenders_EverySurfaceGetsIt covers the fan-out: one event, every surface,
// no ordering between them worth promising.
func TestSenders_EverySurfaceGetsIt(t *testing.T) {
	a, b := &recorder{}, &recorder{}
	Senders{a, b}.Send(Event{Kind: Blocked, Workspace: "fix-auth"})
	if len(a.events) != 1 || len(b.events) != 1 {
		t.Errorf("surfaces got %d and %d events, want one each", len(a.events), len(b.events))
	}
}

func TestCompact(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{40 * time.Second, "40s"},
		{12 * time.Minute, "12m"},
		{3 * time.Hour, "3h"},
	} {
		if got := compact(tt.d); got != tt.want {
			t.Errorf("compact(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestDesktopIsBestEffort: a surface that cannot deliver says nothing and
// returns. There is nobody to tell — the chat that raised it is drawing a
// frame — and a notifier that could fail loudly would be worse than one that
// quietly did not fire.
func TestDesktopIsBestEffort(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no osascript, no notify-send
	done := make(chan struct{})
	go func() {
		Desktop{}.Send(Event{Kind: Blocked, Workspace: "fix-auth", Detail: strings.Repeat("x", 500)})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * sendTimeout):
		t.Fatal("Send did not return")
	}
}
