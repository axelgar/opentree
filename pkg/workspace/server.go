package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
)

// A workspace's dev server runs in a tmux window of its own, beside the one
// holding its chat. That window is the whole implementation: starting, stopping,
// checking and finding an orphan are things opentree already knows how to do to
// a window, and "where did my output go" is answered by attaching to it.
//
// Servers start on demand and never on creation. Five worktrees each running
// `next dev` is five Node processes and several gigabytes nobody asked for, and
// most workspaces are agent-only.

// ServerWindow is the tmux window a workspace's server runs in.
func (s *Service) ServerWindow(name string) string {
	return gitutil.SanitizeBranchName(name) + tmux.RunSuffix
}

// ServerRunning reports whether the workspace's run window exists.
//
// Derived from tmux rather than remembered: a server is a process, and the
// process list is the only thing about it that cannot be stale. A dashboard
// that trusted its own record would offer to stop a server killed by hand, and
// to start one that is already running.
func (s *Service) ServerRunning(name string) bool {
	windows, err := s.process.ListWindows()
	if err != nil {
		return false
	}
	window := s.ServerWindow(name)
	for _, w := range windows {
		if w.Name == window {
			return true
		}
	}
	return false
}

// StartServer runs the project's run command in the workspace's own window and
// reports the port it was given.
func (s *Service) StartServer(name string) (int, error) {
	run := s.cfg.Workspace.Run
	if run == "" {
		return 0, fmt.Errorf("no [workspace] run command configured in opentree.toml")
	}
	// The same gate the setup commands pass: run is executable code from the
	// same tracked file, and approving one without the other would let a
	// payload move one key down. Nothing here can ask — the question belongs on
	// a screen somebody is looking at — so an unapproved command is refused
	// with the way to approve it.
	if !bootstrap.Trusted(s.repoRoot, s.cfg.Workspace.Setup, run, s.cfg.Workspace.Check) {
		return 0, fmt.Errorf("this repository's run command is not approved on this machine — run `opentree trust`")
	}
	if s.ServerRunning(name) {
		return 0, fmt.Errorf("%s's server is already running", name)
	}

	port, err := s.ensurePort(name)
	if err != nil {
		return 0, err
	}

	// PORT, and no rewriting of the command. Injecting --port needs a table of
	// framework flags that goes stale every time one ships a new CLI, and it
	// gives up on exactly the commands people write by hand. A stack that
	// ignores PORT can be told `--port $PORT` by whoever wrote the command.
	//
	// PORTLESS_APP_PORT pins portless to the same number rather than letting it
	// hand out one of its own. The port is the workspace's, recorded and
	// unchanging; it is also what the Servers tab dials to tell a server that is
	// up from one still compiling, and that check has to be asking about the
	// process opentree started.
	env := []string{
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("PORTLESS_APP_PORT=%d", port),
	}
	command, args := s.serverCommand(name, run)
	if err := s.process.CreateAppWindow(s.ServerWindow(name), s.WorktreePath(name), command, env, args...); err != nil {
		return 0, fmt.Errorf("failed to start %s's server: %w", name, err)
	}
	return port, nil
}

// serverCommand is how the run command is launched: behind portless when it is
// there and working, and directly otherwise.
//
// The command itself is never touched either way. Under portless it is still
// `sh -c`, which also means portless has nothing to recognise and so injects
// none of the framework flags it would otherwise guess at.
func (s *Service) serverCommand(name, run string) (string, []string) {
	if p := bootstrap.CheckPortless(); p.Ready {
		host := bootstrap.PortlessHost(name, filepath.Base(s.repoRoot))
		// `portless <name> <command> [args...]`, with the name given rather
		// than inferred — every worktree of one repository infers the same one.
		return "portless", []string{strings.TrimSuffix(host, ".localhost"), "sh", "-c", run}
	}
	return "sh", []string{"-c", run}
}

// StopServer kills the workspace's run window, and everything the command
// started with it.
func (s *Service) StopServer(name string) error {
	if !s.ServerRunning(name) {
		return fmt.Errorf("%s's server is not running", name)
	}
	if err := s.process.KillWindow(s.ServerWindow(name)); err != nil {
		return fmt.Errorf("failed to stop %s's server: %w", name, err)
	}
	return nil
}

// ServerPort is the port a workspace's server would use, or 0 if it has never
// been given one.
func (s *Service) ServerPort(name string) int {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return 0
	}
	return ws.Port
}

// ensurePort gives a workspace its port, once, and remembers it.
//
// Remembered because a port is not only opentree's business: OAuth redirect
// URIs are registered against an exact localhost:PORT, and a port that moved on
// every restart would break every login flow set up against it.
func (s *Service) ensurePort(name string) (int, error) {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return 0, err
	}
	if ws.Port != 0 {
		return ws.Port, nil
	}

	// Read before the Update, not inside it: Update runs its callback holding
	// the store's lock, and ListWorkspaces takes the same one.
	taken := make(map[int]bool)
	for _, other := range s.state.ListWorkspaces() {
		if other.Port != 0 {
			taken[other.Port] = true
		}
	}
	port, err := bootstrap.AssignPort(name, taken)
	if err != nil {
		return 0, err
	}

	if err := s.state.Update(name, func(w *state.Workspace) error {
		// Another process may have assigned one between the read above and
		// this lock. Theirs is already on disk and may already be bound, so
		// it wins; the port picked here is simply dropped.
		if w.Port != 0 {
			port = w.Port
			return nil
		}
		w.Port = port
		return nil
	}); err != nil {
		return 0, fmt.Errorf("failed to record %s's port: %w", name, err)
	}
	return port, nil
}
