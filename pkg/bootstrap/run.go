package bootstrap

import (
	"fmt"
	"hash/fnv"
	"net"
)

// The third part of preparing a worktree, and the one with a problem the other
// two do not have: five worktrees of one repository all want port 3000.
//
// opentree does not rewrite the run command to fix that. Injecting --port is
// where every tool that tries spends its caveats — it needs a table of
// framework flags that goes stale each time one ships a new CLI, and it gives
// up on compound commands, env prefixes and scripts that delegate. PORT is
// exported instead, which is the interface every stack already agreed on, and a
// stack that ignores it can be told `--port $PORT` by the person who wrote the
// command.

const (
	// portFirst and portLast bound the ports opentree hands out.
	//
	// The range is not arbitrary. Linux's ephemeral range starts at 32768 and
	// macOS's at 49152, so anything allocated above those collides with the
	// kernel's own choices intermittently — the worst kind of bug to be handed,
	// since it reproduces for nobody.
	portFirst = 20000
	portLast  = 32000
)

// AssignPort picks a port for a workspace, avoiding the ones its siblings
// already hold.
//
// Assigned once and then persisted, because a port is not only opentree's
// business: OAuth redirect URIs are registered against an exact localhost:PORT,
// and a workspace whose port moved every time its server restarted would break
// every login flow it was set up for.
//
// The first candidate is derived from the workspace's name, so the same
// workspace tends to get the same port across machines and re-creations, and
// the search walks on from there.
func AssignPort(workspace string, taken map[int]bool) (int, error) {
	span := portLast - portFirst
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspace))
	start := int(h.Sum32() % uint32(span)) // #nosec G115 -- span is a small positive constant

	for i := range span {
		port := portFirst + (start+i)%span
		if taken[port] || !free(port) {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("no free port between %d and %d", portFirst, portLast)
}

// free reports whether a port can be bound right now. Binding is the only
// honest test — something listening on it answers a dial, but something bound
// to it and not yet listening does not.
func free(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
