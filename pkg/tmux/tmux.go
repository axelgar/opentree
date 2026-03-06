package tmux

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Controller manages tmux sessions and windows
type Controller struct {
	sessionPrefix    string
	repoNameOnce     sync.Once
	cachedRepoName   string
}

// New creates a new tmux controller
func New(sessionPrefix string) *Controller {
	return &Controller{
		sessionPrefix: sessionPrefix,
	}
}

// CreateWindow creates a new tmux window and runs a command in it
func (c *Controller) CreateWindow(name, workdir, command string, args ...string) error {
	sessionName := c.getSessionName()
	
	// Ensure tmux session exists
	if !c.sessionExists(sessionName) {
		if err := c.createSession(sessionName); err != nil {
			return fmt.Errorf("failed to create tmux session: %w", err)
		}
	}

	// Create new window
	windowName := c.sanitizeWindowName(name)
	cmd := exec.Command("tmux", "new-window", "-t", sessionName, "-n", windowName, "-c", workdir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create tmux window: %w\nOutput: %s", err, output)
	}

	// Send command to the window
	fullCmd := command
	if len(args) > 0 {
		fullCmd = fmt.Sprintf("%s %s", command, strings.Join(args, " "))
	}
	
	sendCmd := exec.Command("tmux", "send-keys", "-t", fmt.Sprintf("%s:%s", sessionName, windowName), fullCmd, "Enter")
	if output, err := sendCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to send command to window: %w\nOutput: %s", err, output)
	}

	return nil
}

// ListWindows returns all windows in the opentree session
func (c *Controller) ListWindows() ([]Window, error) {
	sessionName := c.getSessionName()
	
	if !c.sessionExists(sessionName) {
		return []Window{}, nil
	}

	// Format: window_id window_name window_active
	cmd := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_id}|#{window_name}|#{window_active}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list windows: %w", err)
	}

	return c.parseWindows(string(output))
}

// SelectWindow selects a tmux window by name without attaching.
func (c *Controller) SelectWindow(name string) error {
	sessionName := c.getSessionName()
	windowName := c.sanitizeWindowName(name)
	cmd := exec.Command("tmux", "select-window", "-t", fmt.Sprintf("%s:%s", sessionName, windowName))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to select window: %w\nOutput: %s", err, output)
	}
	return nil
}

type tmuxEnv int

const (
	envOutsideTmux tmuxEnv = iota
	envInsideSameSession
	envInsideDifferentSession
	envNoTTY
)

func (c *Controller) detectEnv() tmuxEnv {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return envNoTTY
	}
	tmuxVar := os.Getenv("TMUX")
	if tmuxVar == "" {
		return envOutsideTmux
	}
	currentSession := c.getCurrentSessionName()
	if currentSession == c.getSessionName() {
		return envInsideSameSession
	}
	return envInsideDifferentSession
}

func (c *Controller) getCurrentSessionName() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// IsInsideTmux reports whether the current process is running inside tmux.
func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// AttachWindow attaches to a specific tmux window using the correct
// strategy based on the current environment.
func (c *Controller) AttachWindow(name string) error {
	env := c.detectEnv()
	sessionName := c.getSessionName()
	windowTarget := fmt.Sprintf("%s:%s", sessionName, c.sanitizeWindowName(name))

	switch env {
	case envNoTTY:
		return fmt.Errorf("attach requires an interactive terminal (no TTY detected)")

	case envOutsideTmux:
		_ = c.SelectWindow(name)
		cmd := exec.Command("tmux", "attach-session", "-t", sessionName)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		var stderr bytes.Buffer
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return fmt.Errorf("%s", msg)
			}
			return fmt.Errorf("tmux attach-session failed: %w", err)
		}
		return nil
	case envInsideSameSession:
		return c.SelectWindow(name)

	case envInsideDifferentSession:
		cmd := exec.Command("tmux", "switch-client", "-t", windowTarget)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return fmt.Errorf("%s", msg)
			}
			return fmt.Errorf("tmux switch-client failed: %w", err)
		}
		return nil
	}

	return fmt.Errorf("unknown tmux environment")
}

// AttachCmd returns the appropriate *exec.Cmd for attaching to a workspace.
// This is intended for callers that need to control execution themselves
// (e.g., Bubble Tea's ExecProcess).
func (c *Controller) AttachCmd(name string) (*exec.Cmd, error) {
	env := c.detectEnv()
	sessionName := c.getSessionName()
	windowTarget := fmt.Sprintf("%s:%s", sessionName, c.sanitizeWindowName(name))

	switch env {
	case envNoTTY:
		return nil, fmt.Errorf("attach requires an interactive terminal (no TTY detected)")

	case envOutsideTmux:
		_ = c.SelectWindow(name)
		return exec.Command("tmux", "attach-session", "-t", sessionName), nil

	case envInsideSameSession:
		return exec.Command("tmux", "select-window", "-t", windowTarget), nil

	case envInsideDifferentSession:
		return exec.Command("tmux", "switch-client", "-t", windowTarget), nil
	}

	return nil, fmt.Errorf("unknown tmux environment")
}

// KillWindow stops and removes a tmux window
func (c *Controller) KillWindow(name string) error {
	sessionName := c.getSessionName()
	windowName := c.sanitizeWindowName(name)
	
	cmd := exec.Command("tmux", "kill-window", "-t", fmt.Sprintf("%s:%s", sessionName, windowName))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to kill window: %w\nOutput: %s", err, output)
	}

	return nil
}

// CapturePane captures recent output from a window
func (c *Controller) CapturePane(name string, lines int) (string, error) {
	sessionName := c.getSessionName()
	windowName := c.sanitizeWindowName(name)
	
	cmd := exec.Command("tmux", "capture-pane", "-t", fmt.Sprintf("%s:%s", sessionName, windowName), 
		"-p", "-S", fmt.Sprintf("-%d", lines))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to capture pane: %w", err)
	}

	return string(output), nil
}

// GetWindowActivity returns the timestamp of the last activity in a tmux window.
func (c *Controller) GetWindowActivity(name string) (time.Time, error) {
	sessionName := c.getSessionName()
	windowName := c.sanitizeWindowName(name)
	cmd := exec.Command("tmux", "display-message", "-t",
		fmt.Sprintf("%s:%s", sessionName, windowName), "-p", "#{window_activity}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get window activity: %w", err)
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse activity timestamp: %w", err)
	}
	return time.Unix(sec, 0), nil
}

// getSessionName returns the tmux session name for this repository.
// It includes the repository directory name so multiple repos can coexist.
func (c *Controller) getSessionName() string {
	repoName := c.repoName()
	if repoName == "" {
		return c.sessionPrefix
	}
	if c.sessionPrefix == "" {
		return repoName
	}
	return c.sessionPrefix + "-" + repoName
}

// repoName derives a short, sanitized name from the current git repository root.
// The result is computed once and cached.
func (c *Controller) repoName() string {
	c.repoNameOnce.Do(func() {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return
		}
		name := filepath.Base(strings.TrimSpace(string(out)))
		// Replace characters that are problematic in tmux session names.
		name = strings.ReplaceAll(name, ".", "-")
		name = strings.ReplaceAll(name, ":", "-")
		c.cachedRepoName = name
	})
	return c.cachedRepoName
}

// sessionExists checks if a tmux session exists
func (c *Controller) sessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// createSession creates a new detached tmux session
func (c *Controller) createSession(name string) error {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create session: %w\nOutput: %s", err, output)
	}
	return nil
}

// sanitizeWindowName converts a branch name to a valid tmux window name
func (c *Controller) sanitizeWindowName(name string) string {
	// Replace invalid characters
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

// parseWindows parses tmux list-windows output
func (c *Controller) parseWindows(output string) ([]Window, error) {
	var windows []Window
	
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			continue
		}
		
		windows = append(windows, Window{
			ID:     parts[0],
			Name:   parts[1],
			Active: parts[2] == "1",
		})
	}
	
	return windows, nil
}

// Window represents a tmux window
type Window struct {
	ID     string
	Name   string
	Active bool
}
