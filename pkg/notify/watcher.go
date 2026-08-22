package notify

import "time"

// cooldown is how long the same event about the same thing stays swallowed. It
// is short on purpose: it is there to absorb a status that flickers, not to
// ration notifications.
const cooldown = 10 * time.Second

// queueDepth is how many transitions may be waiting on delivery at once.
//
// Generous for what it holds — a session produces a handful of these an hour —
// and finite because the whole point of the queue is that Observe never waits.
// A queue this full means a surface that has stopped answering, and one more
// notification on top of thirty-two undelivered ones is worth nothing.
const queueDepth = 32

// Watcher turns a sequence of states into the few moments worth interrupting
// someone for.
//
// It is edge-triggered. The transition is the whole content: a level-triggered
// notifier reading the same field would re-announce a workspace that has been
// blocked for an hour, every time it looked.
//
// One per session, driven from that session's own update loop. Everything that
// decides *whether* a transition is an edge runs on that loop and is touched by
// nothing else, which is why none of it is locked. Everything after that
// decision runs on one goroutine of this Watcher's own — see deliver.
type Watcher struct {
	workspace string
	on        map[Kind]bool
	send      Sender
	watched   func() bool
	now       func() time.Time

	prev  State
	since time.Time

	// queue hands an event that survived the edge test to the delivery
	// goroutine. last belongs to that goroutine alone.
	queue chan pending
	last  map[string]time.Time
}

// pending is one item on the delivery queue. A done channel and no event is a
// marker: the goroutine closes it in turn, which is how a test waits for
// everything queued ahead of it to have been dealt with.
type pending struct {
	ev   Event
	at   time.Time
	done chan struct{}
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
//
// A live one owns a goroutine for the rest of the process. There is no stop,
// because there is nothing a caller would do with one: a Watcher is made once
// per chat session and the session is the process. The goroutine is parked on
// an empty channel whenever there is nothing to deliver.
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
	w := &Watcher{
		workspace: opts.Workspace,
		on:        on,
		send:      opts.Send,
		watched:   opts.Watched,
		now:       now,
		prev:      StateOther,
		queue:     make(chan pending, queueDepth),
		last:      map[string]time.Time{},
	}
	go w.deliver()
	return w
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

	ev := Event{Kind: kind, Workspace: w.workspace, Detail: sig.Detail}
	if kind == Done {
		ev.Elapsed = held
	}

	// The moment travels with the event, so the cooldown compares when the
	// transition happened rather than when the queue got round to it.
	select {
	case w.queue <- pending{ev: ev, at: at}:
	default:
		// Never on the caller's behalf: Observe is called from a render loop,
		// and a loop that waits is a window that has stopped drawing. A queue
		// this full is a surface that has stopped answering, and the dropped
		// event has not armed the cooldown against whatever comes next.
	}
}

// deliver is everything that happens after a transition has been judged worth
// carrying: whether anyone is already looking, whether it was said moments ago,
// and saying it.
//
// It is a goroutine because all three of those leave the process. The
// visibility question is a tmux subprocess and each surface is another —
// osascript or notify-send, both bounded at three seconds and neither in a
// hurry — so a single blocked transition used to freeze the chat's whole update
// loop for as long as the slowest of them took. Notifying about a window is not
// worth making that window stop drawing.
//
// The two questions travel together and in this order deliberately, which is
// what makes this one goroutine rather than a command per event: the cooldown
// must not be armed by a notification nobody received (see throttled), and
// w.last is unguarded because this goroutine is the only thing that touches it.
func (w *Watcher) deliver() {
	for it := range w.queue {
		if it.done != nil {
			close(it.done)
			continue
		}
		// Asked here rather than when the reading arrived: it is one subprocess
		// on a machine that may be busy, an event nobody enabled should not pay
		// for it, and the honest moment to ask whether somebody is looking at
		// the window is the moment before interrupting them.
		if w.watched != nil && w.watched() {
			continue
		}
		if w.throttled(it.ev, it.at) {
			continue
		}
		w.send.Send(it.ev)
	}
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
// a notification nobody received would swallow the one that follows it. That is
// also why this runs on the delivery goroutine rather than beside Observe —
// splitting the two would put the cooldown ahead of the visibility question and
// invert the rule. The map is unguarded because that goroutine is the only
// thing that reaches it.
func (w *Watcher) throttled(ev Event, at time.Time) bool {
	key := string(ev.Kind) + "\x00" + ev.Detail
	if last, ok := w.last[key]; ok && at.Sub(last) < cooldown {
		return true
	}
	w.last[key] = at
	return false
}
