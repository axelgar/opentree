package chat

import (
	"encoding/json"
	"errors"
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
	StateSettingUp = "setting_up"
	StateChecking  = "checking"
	dialTimeout    = 300 * time.Millisecond
	commandTimeout = 2 * time.Second
)

// ProtocolVersion is the version of this socket's wire format, stamped on
// everything that crosses it.
//
// It exists because the two ends of this socket are routinely different
// binaries. A chat window is only ever relaunched when it has stopped serving
// one — workspace.EnsureWindow starts one where there is no window, or a bare
// shell — so after an in-place upgrade the dashboard is the new opentree and
// every chat still open is the one it replaced, for as long as those windows
// live. Without a number on the wire neither side can tell a peer that predates
// a field from a peer that is broken, and the two want opposite reactions.
//
// Nothing refuses a peer over it, and nothing may start. A version older than
// this one — zero included, which is what every chat built before this constant
// publishes — is precisely the case it exists to describe; refusing those would
// break the upgrade it is here to survive. A newer one is not refused either:
// encoding/json drops fields it has no home for, so a status from the future
// still parses into everything this binary knows how to read.
//
// Bump it when a change needs the other side to know it happened: a field one
// end must not assume the other honours, or a command whose refusal would
// otherwise read as a fault.
//
// 2: the autopilot command and the Autopilot status field. A dashboard that
// sends "autopilot" to a version-1 window gets a refusal that names the
// upgrade rather than a bug.
const ProtocolVersion = 2

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
	Queued     string      `json:"queued,omitempty"`
	Permission *Permission `json:"permission,omitempty"`

	// Since is when the session last entered this state, stamped by the chat
	// rather than by whoever is reading it. The difference matters exactly when
	// it is worth knowing: a workspace that has been blocked for forty minutes,
	// read by a dashboard opened ten seconds ago, would otherwise report ten
	// seconds.
	Since time.Time `json:"since,omitzero"`

	// Error is a failure worth recording somewhere other than this window — a
	// setup command that did not finish, in a workspace nobody may ever attach
	// to. The list copies it into opentree's error log; the chat is where it is
	// acted on.
	Error string `json:"error,omitempty"`

	// Protocol is the wire version this chat speaks and Version is the opentree
	// release it is running. Both travel because they answer different
	// questions: the number is what code compares, and the release is what a row
	// can show somebody. "This chat is running opentree 0.4.1" is a sentence
	// that gets a window closed and reopened; a row that has quietly stopped
	// understanding half of what the dashboard sends it is a bug report.
	//
	// Zero and empty mean a chat older than these fields, which is the ordinary
	// reading for the first upgrade after they land — see ProtocolVersion.
	Protocol int    `json:"protocol,omitempty"`
	Version  string `json:"version,omitempty"`

	// Autopilot is the loop's state, present only when it is on. Nil from a
	// chat where it is off — and from every chat built before the field, which
	// reads the same and should.
	Autopilot *AutopilotStatus `json:"autopilot,omitempty"`
}

// AutopilotStatus is the loop as the dashboard and dispatch read it.
type AutopilotStatus struct {
	Enabled bool `json:"enabled"`

	// Phase is where the loop is: "idle", "asking", "checking", "publishing",
	// "halted".
	Phase string `json:"phase,omitempty"`

	// Iteration counts consecutive autopilot-fed turns since the last human
	// prompt, which is what "halted" is measured against.
	Iteration int `json:"iteration,omitempty"`

	// PRURL is the PR the loop last published, once there is one.
	PRURL string `json:"pr_url,omitempty"`

	// Outcome is "published" once a publish has succeeded in this chat's
	// lifetime. It is how a headless dispatch knows it is done.
	Outcome string `json:"outcome,omitempty"`
}

// Behind reports whether the chat that published this status speaks an older
// version of the protocol than the binary reading it.
//
// True is not a fault and not an error to raise. Every chat window that was
// open when opentree was replaced answers yes, which is most of them for the
// rest of the day — it is a thing for a row to say, not a reason to stop
// reading one.
func (s Status) Behind() bool { return s.Protocol < ProtocolVersion }

// Permission is an escalation waiting on a human, mirrored so the workspace
// list can answer it without attaching.
type Permission struct {
	Title   string                 `json:"title"`
	Options []acp.PermissionOption `json:"options"`

	// ToolCallID names the request this is, so an answer can be matched to the
	// question it was given for. The list's view of a session is up to a
	// refresh tick stale, and the answer used to be applied to whatever was at
	// the head of the queue when it arrived — which after a tick may be a
	// different tool asking a different thing.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Command types the list can send back.
const (
	CommandPermission = "permission"
	CommandInterrupt  = "interrupt"
	CommandPrompt     = "prompt"

	// CommandAutopilot flips the loop, Text carrying "on" or "off".
	CommandAutopilot = "autopilot"
)

type Command struct {
	Type     string `json:"type"`
	OptionID string `json:"option_id,omitempty"`
	Text     string `json:"text,omitempty"`

	// ToolCallID is the permission this answer was given for, copied from the
	// Permission the sender rendered.
	//
	// Checked only when it is set. A chat running a newer binary than the
	// dashboard beside it — which is the normal state of things, since a chat
	// window is never relaunched on upgrade — would otherwise refuse every
	// remote answer it got, and the refusal would look like the dashboard
	// being broken.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// Protocol is the wire version the sender speaks, stamped by Send so no
	// caller has to remember it. Read only to explain a refusal: a command a
	// chat has no case for is an unknown command when it came from a peer of the
	// same age, and an out-of-date window when it came from a newer one.
	Protocol int `json:"protocol,omitempty"`
}

// Result is the chat's answer to a command. A command that arrives at a moment
// the chat cannot honour it — a prompt while the agent is mid-turn, a
// permission that was already answered in the window — is refused with a
// reason. Accepting the bytes is not the same as acting on them, and the list
// reporting "sent" for a command that went nowhere is worse than an error.
type Result struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// socketRoot is /tmp rather than os.TempDir() because macOS resolves TMPDIR to
// a long /var/folders/... path, and unix socket paths are capped near 104
// bytes. Everything opentree runs on has /tmp; it already requires tmux.
const socketRoot = "/tmp"

// socketNameMax is how much of a workspace's name a socket path can carry
// before it has to be shortened. The limit is the ~104-byte cap on a unix
// socket path, shared with the repo directory above it.
const socketNameMax = 32

// SocketPath is where a workspace's chat process listens.
//
// Sockets live outside the repo for length reasons: ".opentree/s/<branch>"
// under a worktree that is itself nested a few directories deep overruns the
// socket path limit, and the bind fails with "invalid argument". The repo is
// identified by a hash of its root so two checkouts of the same project get
// their own directory.
//
// A name too long to fit keeps its first bytes and gains a hash of the whole
// of it. Truncating alone was not enough: two branches sharing 32 characters —
// which is one afternoon's worth of feature/<ticket>-<description> — resolved
// to one path, and the second chat to start would unlink the first's live
// socket and bind over it. Prompts then ran in the wrong worktree.
//
// The suffix is added only when the name actually had to be shortened, so
// every ordinary workspace keeps the exact path it has now and a chat started
// by the previous binary stays reachable across the upgrade.
func SocketPath(repoRoot, workspace string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(repoRoot))

	name := strings.NewReplacer("/", "-", ":", "-").Replace(workspace)
	if len(name) > socketNameMax {
		n := fnv.New32a()
		_, _ = n.Write([]byte(name))
		name = fmt.Sprintf("%s-%08x", name[:socketNameMax-9], n.Sum32())
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
func serve(path, workspace string, onCommand func(Command) Result) (*server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// A socket left behind by a killed process would block the bind.
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Named and versioned from the first moment, before the model has published
	// anything. A status that cannot say whose it is cannot be checked against
	// the workspace the caller asked about, and "starting…" is a state a chat
	// can sit in for as long as its agent takes to answer. The release this is
	// running is not known here and arrives with the first published status,
	// which is the chat's first frame away.
	s := &server{ln: ln, last: Status{
		Workspace: workspace,
		State:     StateStarting,
		Protocol:  ProtocolVersion,
	}}
	go s.accept(onCommand)
	return s, nil
}

func (s *server) accept(onCommand func(Command) Result) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn, onCommand)
	}
}

func (s *server) handle(conn net.Conn, onCommand func(Command) Result) {
	defer func() { _ = conn.Close() }()

	s.mu.Lock()
	current := s.last
	s.mu.Unlock()
	if err := json.NewEncoder(conn).Encode(current); err != nil {
		return
	}

	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	for {
		var cmd Command
		if err := dec.Decode(&cmd); err != nil {
			return
		}
		res := Result{OK: true}
		if onCommand != nil {
			res = onCommand(cmd)
		}
		if err := enc.Encode(res); err != nil {
			return
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

// answersFor reports whether a greeting came from the workspace the caller
// meant to reach.
//
// A status with no workspace name is accepted: that is a chat running a binary
// from before the name was published, and a chat window survives every upgrade
// because it is only relaunched when it has stopped serving one.
func answersFor(st Status, workspace string) bool {
	return st.Workspace == "" || st.Workspace == workspace
}

// Query asks a workspace's chat process what it is doing. A missing or refused
// socket means no chat is running, which is not an error worth reporting.
//
// A socket answering for a different workspace is treated the same way. Two
// workspaces can be handed the same path — see SocketPath — and reading the
// wrong chat's status is worse than reading none: the row would show another
// branch's agent as its own, and every key acting on it would be aimed there.
func Query(path, workspace string) (Status, bool) {
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
	if !answersFor(st, workspace) {
		return Status{}, false
	}
	return st, true
}

// Send issues one command to a workspace's chat process.
//
// workspace is who the caller believes is listening, checked against the
// greeting before anything is sent. A prompt is a thing an agent will act on
// in a worktree, so delivering one to the wrong chat is not a display bug.
func Send(path, workspace string, cmd Command) error {
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(commandTimeout))

	// One decoder for the whole exchange: it buffers, so a second decoder
	// would lose whatever the first read past the greeting.
	dec := json.NewDecoder(conn)
	var st Status
	_ = dec.Decode(&st) // greeting
	if !answersFor(st, workspace) {
		return fmt.Errorf("the chat listening for %s answers for %q", workspace, st.Workspace)
	}

	// Stamped here rather than by the caller: every sender would otherwise have
	// to remember, and the one that forgot would look like a chat from before
	// the field existed.
	cmd.Protocol = ProtocolVersion
	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return err
	}

	var res Result
	if err := dec.Decode(&res); err != nil {
		return fmt.Errorf("no answer from the agent: %w", err)
	}
	if !res.OK {
		return errors.New(res.Reason)
	}
	return nil
}
