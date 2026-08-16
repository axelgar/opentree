package notify

import "time"

// cooldown is how long the same event about the same thing stays swallowed. It
// is short on purpose: it is there to absorb a status that flickers, not to
// ration notifications.
const cooldown = 10 * time.Second

// Watcher turns a sequence of states into the few moments worth interrupting
// someone for.
//
// It is edge-triggered. The transition is the whole content: a level-triggered
// notifier reading the same field would re-announce a workspace that has been
// blocked for an hour, every time it looked.
//
// One per session, driven from that session's own update loop, which is why
// nothing here is locked.
type Watcher struct {
	workspace string
	on        map[Kind]bool
	send      Sender
	watched   func() bool
	now       func() time.Time

	prev  State
	since time.Time
	last  map[string]time.Time
}

// Options is what a Watcher needs to exist.
type Options struct {
	// Workspace names the session in whatever a surface renders.
	Workspace string

	// On is the events to send, by name; anything else never fires. Unknown
	// names are ignored rather than refused — the config file is not this
	// package's to validate, and `opentree notify test` is where a typo shows.
	On []string

	// Send is the surface, or surfaces.
	Send Sender

	// Watched reports whether a human is already looking at this session. A
	// notification about the window you are reading is an interruption with no
	// content. Nil is a session nobody is ever looking at.
	Watched func() bool

	// Now overrides the clock, for tests.
	Now func() time.Time
}

// New returns a Watcher, or nil when nothing could ever come of one: no
// surface to send to, or no event enabled. A nil *Watcher observes happily and
// does nothing, so the caller never has to carry the question.
func New(opts Options) *Watcher {
	on := make(map[Kind]bool, len(opts.On))
	for _, name := range opts.On {
		for _, k := range Kinds {
			if Kind(name) == k {
				on[k] = true
			}
		}
	}
	if opts.Send == nil || len(on) == 0 {
		return nil
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Watcher{
		workspace: opts.Workspace,
		on:        on,
		send:      opts.Send,
		watched:   opts.Watched,
		now:       now,
		prev:      StateOther,
		last:      map[string]time.Time{},
	}
}

// Observe takes one reading of the session.
//
// Every reading, not only the ones that changed: the caller's whole advantage
// is that it has a single funnel every state change passes through, and
// deciding which of them is an edge belongs here.
func (w *Watcher) Observe(sig Signal) {
	if w == nil || sig.State == w.prev {
		return
	}

	from, at := w.prev, w.now()
	held := time.Duration(0)
	if !w.since.IsZero() {
		held = at.Sub(w.since)
	}
	w.prev, w.since = sig.State, at

	kind, ok := transition(from, sig.State)
	if !ok || !w.on[kind] {
		return
	}

	// Asked here rather than earlier: it is one subprocess on a machine that
	// may be busy, and an event nobody enabled should not pay for it.
	if w.watched != nil && w.watched() {
		return
	}

	ev := Event{Kind: kind, Workspace: w.workspace, Detail: sig.Detail}
	if kind == Done {
		ev.Elapsed = held
	}
	if w.throttled(ev, at) {
		return
	}
	w.send.Send(ev)
}

// transition names the moment between two states, or says there is none.
//
// done fires only from working. A chat goes starting → idle when it finishes
// its handshake, which is a session opening rather than a turn ending, and
// notifying on it would ring every window at launch.
func transition(from, to State) (Kind, bool) {
	switch {
	case to == StateBlocked:
		return Blocked, true
	case to == StateStopped:
		return Stopped, true
	case to == StateIdle && from == StateWorking:
		return Done, true
	}
	return "", false
}

// throttled reports whether this exact event was already sent moments ago, and
// records it when it was not.
//
// The fingerprint is the detail, so a second escalation asking a different
// question is never swallowed — only a status that flickers back and forth
// between the same two readings.
//
// A suppressed event never gets this far, deliberately: arming the cooldown for
// a notification nobody received would swallow the one that follows it.
func (w *Watcher) throttled(ev Event, at time.Time) bool {
	key := string(ev.Kind) + "\x00" + ev.Detail
	if last, ok := w.last[key]; ok && at.Sub(last) < cooldown {
		return true
	}
	w.last[key] = at
	return false
}
