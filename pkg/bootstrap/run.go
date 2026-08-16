package bootstrap

import (
	"fmt"
	"hash/fnv"
	"net"
	"os/exec"
	"strings"
	"time"
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

	// dialTimeout bounds the liveness check. This is a loopback connection to a
	// port on this machine: it either answers at once or there is nothing
	// there, and the list refreshes on a tick that cannot afford to wait.
	dialTimeout = 50 * time.Millisecond
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

// Listening reports whether something is serving on a port.
//
// It is what tells a server that has finished starting from one that is still
// compiling: the window's process is alive either way, and only the socket
// knows the difference. That three-state distinction — stopped, starting, up —
// is most of what a list of servers is worth, and it costs one loopback dial.
func Listening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), dialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Portless is what portless can do for this machine right now.
//
// opentree does not reimplement any of it. Serving https://name.localhost takes
// a certificate authority, a sudo prompt, an /etc/hosts writer and a root-owned
// service — a larger and more privileged program than this one, whose worst
// effect on a machine today is a symlink. What portless buys is a stable name
// instead of a port, and that is very much worth using once somebody has
// decided to install it.
type Portless struct {
	// Installed is portless on PATH.
	Installed bool
	// Ready is installed, with its proxy actually answering.
	Ready bool
	// Reason is why an installed portless is not being used.
	Reason string
}

// CheckPortless reports whether a server can be started behind portless.
//
// Readiness is a dial at the proxy rather than a question put to portless,
// because the answer has to be certain. An installed portless whose proxy is
// not up needs a CA, a hosts file and a root service to get there, and it asks
// for them with a sudo prompt — which in a detached tmux window nobody is
// looking at is a server that silently never starts. A proxy that answers has
// already been through all of that.
func CheckPortless() Portless {
	if _, err := exec.LookPath("portless"); err != nil {
		return Portless{}
	}
	// 443 with TLS, 80 with --no-tls: the two ports its proxy can be on.
	if Listening(443) || Listening(80) {
		return Portless{Installed: true, Ready: true}
	}
	return Portless{
		Installed: true,
		Reason:    "portless is installed but its proxy is not running — `portless proxy start`",
	}
}

// PortlessHost is the name portless serves a workspace under:
// <branch>.<repo>.localhost, which reads as "this branch of this project".
//
// Named explicitly rather than left to portless's own inference, which reads
// package.json, the git root or the directory — all three of which are the same
// for every worktree of one repository, so every worktree would infer the same
// name and collide.
func PortlessHost(workspace, repo string) string {
	labels := []string{}
	for _, part := range []string{workspace, repo} {
		if label := hostLabel(part); label != "" {
			labels = append(labels, label)
		}
	}
	return strings.Join(append(labels, "localhost"), ".")
}

// hostLabel reduces a branch or repository name to one DNS label. Dots go too:
// they would silently deepen the subdomain, and a wildcard certificate covers
// one level.
func hostLabel(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
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
