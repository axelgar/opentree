// Package notify carries the moment an agent starts needing a human out of the
// window it happened in.
//
// The chat process is the only thing that can do it. It is the one component
// that still exists when the dashboard does not — which is the case the whole
// feature is for — and every state change it makes funnels through a single
// method. So this package is shaped for that caller: it observes a sequence of
// states, decides which transitions are worth interrupting someone for, and
// hands the few that are to a surface.
//
// It knows nothing about agents, permissions or the protocol. A Signal is a
// state name and a line of detail, which is what keeps the whole thing testable
// without an agent to run.
package notify

import "time"

// State is what a watched session is doing, in this package's own vocabulary.
// The chat has its own names and maps onto these: the two are deliberately not
// the same type, because importing the chat here would be a cycle the moment
// the chat notified anything.
type State string

const (
	// StateOther is every state nothing is said about — starting, setting up.
	// It is a state rather than an absence so that leaving it is still an edge.
	StateOther   State = "other"
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateStopped State = "stopped"
)

// Kind is a moment worth carrying: the three things that happen to an agent
// which somebody looking elsewhere would want to know about. They are also the
// names the `[notify] on` list is written in.
type Kind string

const (
	Blocked Kind = "blocked" // the agent stopped to ask a human
	Done    Kind = "done"    // a turn finished
	Stopped Kind = "stopped" // the agent died, failed to start, or its setup failed
)

// Kinds is every event there is, in the order that reads as a lifecycle.
var Kinds = []Kind{Blocked, Done, Stopped}

// Default is what is on when nothing has said otherwise. Something that needs a
// human is worth an interruption and an agent that has stopped is worth
// knowing; a finished turn is worth seeing when you get back. Four agents
// finishing turns is a banner every ninety seconds, and a notifier you mute is
// a notifier you deleted.
func Default() []string { return []string{string(Blocked), string(Stopped)} }

// Signal is one reading of a session. Detail is whatever the state has to say
// for itself — the escalation's title when blocked, the failure when stopped —
// and doubles as the fingerprint the cooldown compares.
type Signal struct {
	State  State
	Detail string
}

// Event is a transition that earned an interruption.
type Event struct {
	Kind      Kind
	Workspace string
	Detail    string

	// Elapsed is how long the session spent in the state it just left, which is
	// how long the turn took for a done. Zero for everything else.
	Elapsed time.Duration
}

// Sender is one surface a notification reaches. Implementations are
// best-effort and report nothing: a surface that is missing, or that fails,
// must cost neither the other surfaces nor the chat driving them.
type Sender interface {
	Send(Event)
}
