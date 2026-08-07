package chat

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/axelgar/opentree/pkg/acp"
)

// Session states a chat process reports.
const (
	StateIdle      = "idle"
	StateWorking   = "working"
	StateAwaiting  = "awaiting_permission"
	StateStopped   = "stopped"
	StateStarting  = "starting"
	dialTimeout    = 300 * time.Millisecond
	commandTimeout = 2 * time.Second
)

// Status is what a chat process publishes about its session. It replaces the
// hook-written status file for ACP agents: the chat process is holding the
// protocol connection, so it knows exactly what the agent is doing rather than
// inferring it from a file's mtime.
type Status struct {
	Workspace  string      `json:"workspace"`
	State      string      `json:"state"`
	Tool       string      `json:"tool,omitempty"`
	Cost       float64     `json:"cost,omitempty"`
	ContextPct int         `json:"context_pct,omitempty"`
	Permission *Permission `json:"permission,omitempty"`
}

// Permission is an escalation waiting on a human, mirrored so the workspace
// list can answer it without attaching.
type Permission struct {
	Title   string                 `json:"title"`
	Options []acp.PermissionOption `json:"options"`
}

// Command types the list can send back.
const (
	CommandPermission = "permission"
	CommandInterrupt  = "interrupt"
	CommandPrompt     = "prompt"
)

type Command struct {
	Type     string `json:"type"`
	OptionID string `json:"option_id,omitempty"`
	Text     string `json:"text,omitempty"`
}

// socketRoot is /tmp rather than os.TempDir() because macOS resolves TMPDIR to
// a long /var/folders/... path, and unix socket paths are capped near 104
// bytes. Everything opentree runs on has /tmp; it already requires tmux.
const socketRoot = "/tmp"

// SocketPath is where a workspace's chat process listens.
//
// Sockets live outside the repo for length reasons: ".opentree/s/<branch>"
// under a worktree that is itself nested a few directories deep overruns the
// socket path limit, and the bind fails with "invalid argument". The repo is
// identified by a hash of its root so two checkouts of the same project get
// their own directory.
func SocketPath(repoRoot, workspace string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(repoRoot))

	name := strings.NewReplacer("/", "-", ":", "-").Replace(workspace)
	if len(name) > 32 {
		name = name[:32]
	}
	return filepath.Join(socketRoot, fmt.Sprintf("opentree-%08x", h.Sum32()), name)
}

// ---------------------------------------------------------------------------
// Server, run by the chat process
// ---------------------------------------------------------------------------

type server struct {
	ln   net.Listener
	mu   sync.Mutex
	last Status
}

// serve starts the control socket. Each connection is a one-shot exchange: the
// server writes the current status, then reads any commands until the peer
// hangs up. Polling beats a persistent stream here because the workspace list
// already refreshes on a tick, and one-shot means no reconnect logic on either
// side.
func serve(path string, onCommand func(Command)) (*server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// A socket left behind by a killed process would block the bind.
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &server{ln: ln, last: Status{State: StateStarting}}
	go s.accept(onCommand)
	return s, nil
}

func (s *server) accept(onCommand func(Command)) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn, onCommand)
	}
}

func (s *server) handle(conn net.Conn, onCommand func(Command)) {
	defer func() { _ = conn.Close() }()

	s.mu.Lock()
	current := s.last
	s.mu.Unlock()
	if err := json.NewEncoder(conn).Encode(current); err != nil {
		return
	}

	dec := json.NewDecoder(conn)
	for {
		var cmd Command
		if err := dec.Decode(&cmd); err != nil {
			return
		}
		if onCommand != nil {
			onCommand(cmd)
		}
	}
}

func (s *server) publish(st Status) {
	s.mu.Lock()
	s.last = st
	s.mu.Unlock()
}

func (s *server) Close() error {
	addr := s.ln.Addr().String()
	err := s.ln.Close()
	_ = os.Remove(addr)
	return err
}

// ---------------------------------------------------------------------------
// Client, used by the workspace list
// ---------------------------------------------------------------------------

// Query asks a workspace's chat process what it is doing. A missing or refused
// socket means no chat is running, which is not an error worth reporting.
func Query(path string) (Status, bool) {
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return Status{}, false
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(commandTimeout))
	var st Status
	if err := json.NewDecoder(conn).Decode(&st); err != nil {
		return Status{}, false
	}
	return st, true
}

// Send issues one command to a workspace's chat process.
func Send(path string, cmd Command) error {
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(commandTimeout))
	// The server greets with a status before listening; drain it so the
	// command is not interleaved with a write the peer has not finished.
	var st Status
	_ = json.NewDecoder(conn).Decode(&st)
	return json.NewEncoder(conn).Encode(cmd)
}
