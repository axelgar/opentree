package notify

import (
	"testing"
	"time"
)

// recorder is the surface the watcher's tests send to. Nothing here shells out
// — that is the point of the Sender interface, and it is why every rule below
// can be tested without a tmux session or a notification daemon.
type recorder struct{ events []Event }

func (r *recorder) Send(ev Event) { r.events = append(r.events, ev) }

func (r *recorder) kinds() []Kind {
	out := make([]Kind, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.Kind)
	}
	return out
}

// clock is a hand-wound time.Now, so the cooldown can be walked past without
// a test that sleeps.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

// watch returns a watcher with every event on, its surface and its clock.
func watch(t *testing.T, on ...string) (*Watcher, *recorder, *clock) {
	t.Helper()
	if len(on) == 0 {
		on = []string{"blocked", "done", "stopped"}
	}
	rec := &recorder{}
	c := &clock{t: time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)}
	w := New(Options{Workspace: "fix-auth", On: on, Send: rec, Now: c.now})
	if w == nil {
		t.Fatal("New returned nil for a watcher with a surface and events")
	}
	return w, rec, c
}

func observe(w *Watcher, states ...State) {
	for _, s := range states {
		w.Observe(Signal{State: s})
	}
}

func TestWatcher_TheThreeEvents(t *testing.T) {
	tests := []struct {
		name   string
		states []State
		want   []Kind
	}{
		{
			"a turn that needs a human, answered, then finished",
			[]State{StateWorking, StateBlocked, StateWorking, StateIdle},
			[]Kind{Blocked, Done},
		},
		{
			"an agent that dies mid-turn",
			[]State{StateWorking, StateStopped},
			[]Kind{Stopped},
		},
		{
			// The handshake, which is not a turn ending.
			"a chat opening",
			[]State{StateOther, StateIdle},
			nil,
		},
		{
			"a session that only ever sits idle",
			[]State{StateIdle, StateIdle, StateIdle},
			nil,
		},
		{
			"blocked from anywhere",
			[]State{StateIdle, StateBlocked},
			[]Kind{Blocked},
		},
		{
			"stopped before anything else happened",
			[]State{StateStopped},
			[]Kind{Stopped},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, rec, _ := watch(t)
			observe(w, tt.states...)
			if got := rec.kinds(); !sameKinds(got, tt.want) {
				t.Errorf("events = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWatcher_EdgeNotLevel is the difference from recordChatErrors, which reads
// the same field on every refresh on purpose. An error persists and wants to be
// seen once; a transition happens once and is the whole content.
func TestWatcher_EdgeNotLevel(t *testing.T) {
	w, rec, c := watch(t)
	w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	for range 20 {
		c.add(time.Minute)
		w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	}
	if len(rec.events) != 1 {
		t.Fatalf("sent %d notifications for one escalation, want 1", len(rec.events))
	}
}

func TestWatcher_OnlyTheEnabledEvents(t *testing.T) {
	w, rec, _ := watch(t, "blocked")
	observe(w, StateWorking, StateIdle, StateStopped, StateBlocked)
	if got := rec.kinds(); !sameKinds(got, []Kind{Blocked}) {
		t.Errorf("events = %v, want only the one that was switched on", got)
	}
}

func TestWatcher_NothingToDoIsNoWatcher(t *testing.T) {
	if w := New(Options{On: Default(), Send: nil}); w != nil {
		t.Error("a watcher with no surface should be nil")
	}
	if w := New(Options{On: nil, Send: &recorder{}}); w != nil {
		t.Error("a watcher with every event off should be nil")
	}
	if w := New(Options{On: []string{"blokced"}, Send: &recorder{}}); w != nil {
		t.Error("a misspelled event is no event")
	}
	// And the nil one is safe to drive, so no caller carries the question.
	var w *Watcher
	w.Observe(Signal{State: StateBlocked})
}

// TestWatcher_CooldownSwallowsAFlicker covers a status that bounces between two
// readings — the same question, twice, seconds apart.
func TestWatcher_CooldownSwallowsAFlicker(t *testing.T) {
	w, rec, c := watch(t)
	for range 5 {
		w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
		c.add(time.Second)
		w.Observe(Signal{State: StateWorking})
		c.add(time.Second)
	}
	if len(rec.events) != 1 {
		t.Fatalf("sent %d notifications for one flickering escalation, want 1", len(rec.events))
	}

	// Past the cooldown it is news again.
	c.add(cooldown)
	w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	if len(rec.events) != 2 {
		t.Fatalf("sent %d, want the same question again once the cooldown passed", len(rec.events))
	}
}

// TestWatcher_CooldownIsPerQuestion is why the fingerprint exists: a second
// escalation seconds after the first is a different question, and swallowing it
// would leave an agent blocked with nothing said about it.
func TestWatcher_CooldownIsPerQuestion(t *testing.T) {
	w, rec, c := watch(t)
	w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	c.add(time.Second)
	w.Observe(Signal{State: StateWorking})
	c.add(time.Second)
	w.Observe(Signal{State: StateBlocked, Detail: "git push --force"})

	if len(rec.events) != 2 {
		t.Fatalf("sent %d notifications for two different questions, want 2", len(rec.events))
	}
	if rec.events[1].Detail != "git push --force" {
		t.Errorf("second notification was about %q", rec.events[1].Detail)
	}
}

// TestWatcher_SuppressedWhenWatched is decision 6: a notification about the
// window you are reading is an interruption with no content.
func TestWatcher_SuppressedWhenWatched(t *testing.T) {
	rec := &recorder{}
	looking := true
	w := New(Options{Workspace: "fix-auth", On: Default(), Send: rec,
		Watched: func() bool { return looking }})

	w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	if len(rec.events) != 0 {
		t.Fatalf("sent %d notifications about the window being looked at", len(rec.events))
	}

	// Look away, and the next escalation lands — the suppressed one must not
	// have armed the cooldown against it.
	looking = false
	w.Observe(Signal{State: StateWorking})
	w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	if len(rec.events) != 1 {
		t.Fatalf("sent %d, want the escalation once nobody was looking", len(rec.events))
	}
}

// TestWatcher_DoneCarriesTheTurnLength is the second thing the state's start
// time pays for: the notification can say how long you were away.
func TestWatcher_DoneCarriesTheTurnLength(t *testing.T) {
	w, rec, c := watch(t)
	w.Observe(Signal{State: StateWorking})
	c.add(4 * time.Minute)
	w.Observe(Signal{State: StateIdle})

	if len(rec.events) != 1 {
		t.Fatalf("sent %d notifications, want the one done", len(rec.events))
	}
	if got := rec.events[0].Elapsed; got != 4*time.Minute {
		t.Errorf("Elapsed = %v, want the length of the turn", got)
	}
}

func TestWatcher_EventsNameTheirWorkspace(t *testing.T) {
	w, rec, _ := watch(t)
	w.Observe(Signal{State: StateBlocked, Detail: "rm -rf dist"})
	if got := rec.events[0].Workspace; got != "fix-auth" {
		t.Errorf("Workspace = %q, want the one the watcher was made for", got)
	}
}

func sameKinds(got, want []Kind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
