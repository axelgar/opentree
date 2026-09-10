package workspace

import (
	"fmt"
	"os/exec"

	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
)

// A workspace's shell is a tmux window beside its chat, the way its dev server
// is. The chat's window is opentree's, holding the conversation; this one is
// the user's, for running the tests the agent asked about and pasting it the
// answer — which used to mean knowing that feat/x lives at feat-x under a
// directory nobody had printed, and cd-ing there by hand.
//
// One per workspace, reused while it lives: a second "shell" is the same
// shell, with whatever was half-typed still in it.

// ShellWindow is the tmux window a workspace's shell runs in.
func (s *Service) ShellWindow(name string) string {
	return gitutil.SanitizeBranchName(name) + tmux.ShellSuffix
}

// Workspace is one workspace's record, or an error naming the one there is
// not.
func (s *Service) Workspace(name string) (*state.Workspace, error) {
	return s.state.GetWorkspace(name)
}

// OpenShell makes sure the workspace has a shell window, and reports whether
// it had to make one.
func (s *Service) OpenShell(name string) (bool, error) {
	if _, err := s.state.GetWorkspace(name); err != nil {
		return false, err
	}
	if s.windowExists(s.ShellWindow(name)) {
		return false, nil
	}
	if err := s.process.CreateShellWindow(s.ShellWindow(name), s.WorktreePath(name)); err != nil {
		return false, fmt.Errorf("failed to open a shell in %s: %w", name, err)
	}
	return true, nil
}

// ShellCmd is the command that puts the terminal in the workspace's shell
// window, opening one first if there is none — for a caller that controls
// execution, the way the dashboard does.
func (s *Service) ShellCmd(name string) (*exec.Cmd, error) {
	if _, err := s.OpenShell(name); err != nil {
		return nil, err
	}
	return s.process.AttachCmd(s.ShellWindow(name))
}

// windowExists reports whether the session has a window by this name.
//
// Derived from tmux rather than remembered, for the reason ServerRunning
// gives: the window list is the only thing about a window that cannot be
// stale.
func (s *Service) windowExists(window string) bool {
	windows, err := s.process.ListWindows()
	if err != nil {
		return false
	}
	for _, w := range windows {
		if w.Name == window {
			return true
		}
	}
	return false
}
