package workspace

import (
	"os/exec"
	"time"
)

// Window represents a managed process window (e.g., a tmux window).
type Window struct {
	ID     string // unique window identifier
	Name   string // display name (sanitized branch name)
	Active bool   // whether this window is currently focused
}

// ProcessManager abstracts process/window management so the workspace service
// is not coupled to a specific backend (tmux, terminal tabs, etc.).
type ProcessManager interface {
	// CreateWindow creates a new window running command in workdir.
	CreateWindow(name, workdir, command string, args ...string) error

	// ListWindows returns all windows in the current session.
	ListWindows() ([]Window, error)

	// SelectWindow focuses a window by name without attaching.
	SelectWindow(name string) error

	// AttachWindow attaches to a window interactively (blocks until detach).
	AttachWindow(name string) error

	// AttachCmd returns an *exec.Cmd for attaching to a window.
	// Useful when the caller controls execution (e.g., Bubble Tea ExecProcess).
	AttachCmd(name string) (*exec.Cmd, error)

	// KillWindow stops and removes a window.
	KillWindow(name string) error

	// KillSession stops and removes the entire session.
	KillSession() error

	// CapturePane captures recent output lines from a window.
	CapturePane(name string, lines int) (string, error)

	// GetWindowActivity returns the timestamp of the last activity in a window.
	GetWindowActivity(name string) (time.Time, error)
}
