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

	"github.com/axelgar/opentree/pkg/gitutil"
)

// Controller manages tmux sessions and windows
type Controller struct {
	sessionPrefix  string
	repoNameOnce   sync.Once
	cachedRepoName string
	versionOnce    sync.Once
	versionErr     error
}

// New creates a new tmux controller
func New(sessionPrefix string) *Controller {
	return &Controller{
		sessionPrefix: sessionPrefix,
	}
}

// CreateWindow creates a new tmux window and runs a command in it. env holds
// KEY=value pairs set in the window's environment (tmux new-window -e, ≥3.0)
// rather than typed into the shell, keeping the visible command line clean.
func (c *Controller) CreateWindow(name, workdir, command string, env []string, args ...string) error {
	// Fail with a clear message before creating a session: on tmux <3.0 the
	// new-window -e flag below would die with an opaque usage error.
	if err := c.checkVersion(); err != nil {
		return err
	}

	sessionName := c.getSessionName()

	// Ensure tmux session exists
	if !c.sessionExists(sessionName) {
		if err := c.createSession(sessionName); err != nil {
			return fmt.Errorf("failed to create tmux session: %w", err)
		}
	}

	// Create new window, capturing its unique window ID so later commands
	// target exactly this window (names would prefix-match and "." or digits
	// in a name are parsed specially by tmux target syntax).
	windowName := c.sanitizeWindowName(name)
	newWindowArgs := []string{"new-window", "-t", exactSession(sessionName) + ":",
		"-n", windowName, "-c", workdir, "-P", "-F", "#{window_id}"}
	for _, e := range env {
		newWindowArgs = append(newWindowArgs, "-e", e)
	}
	cmd := exec.Command("tmux", newWindowArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create tmux window: %w\nOutput: %s", err, output)
	}
	windowID := strings.TrimSpace(string(output))

	// Send command to the window, raising the file descriptor limit first
	// so that tools like claude and opencode don't hit the default macOS limit.
	fullCmd := command
	if len(args) > 0 {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = shellQuote(a)
		}
		fullCmd = command + " " + strings.Join(quoted, " ")
	}
	fullCmd = fmt.Sprintf("ulimit -n 2147483646 2>/dev/null; %s", fullCmd)

	// -l types the command line literally so tmux never interprets it as key names.
	sendCmd := exec.Command("tmux", "send-keys", "-l", "-t", windowID, "--", fullCmd)
	if output, err := sendCmd.CombinedOutput(); err != nil {
		// Don't leave a dead window behind for the retry to collide with.
		_ = exec.Command("tmux", "kill-window", "-t", windowID).Run()
		return fmt.Errorf("failed to send command to window: %w\nOutput: %s", err, output)
	}
	enterCmd := exec.Command("tmux", "send-keys", "-t", windowID, "Enter")
	if output, err := enterCmd.CombinedOutput(); err != nil {
		_ = exec.Command("tmux", "kill-window", "-t", windowID).Run()
		return fmt.Errorf("failed to send Enter to window: %w\nOutput: %s", err, output)
	}

	return nil
}

// checkVersion fails when the installed tmux predates 3.0, which CreateWindow
// requires for new-window -e. Cached per Controller. A missing/broken tmux
// binary passes: the command that actually needs tmux reports that error.
func (c *Controller) checkVersion() error {
	c.versionOnce.Do(func() {
		out, err := exec.Command("tmux", "-V").Output()
		if err != nil {
			return
		}
		if v := strings.TrimSpace(string(out)); versionBelow3(v) {
			c.versionErr = fmt.Errorf("opentree requires tmux >= 3.0 (found %s)", strings.TrimPrefix(v, "tmux "))
		}
	})
	return c.versionErr
}

// versionBelow3 reports whether `tmux -V` output identifies a tmux older than
// 3.0. Only the major version matters (the floor is 3.0), and unparseable
// versions ("tmux master", unnumbered builds) are assumed modern so unusual
// builds are never blocked.
func versionBelow3(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "tmux ")
	start := strings.IndexFunc(v, func(r rune) bool { return r >= '0' && r <= '9' })
	if start < 0 {
		return false
	}
	end := start
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	major, err := strconv.Atoi(v[start:end])
	return err == nil && major < 3
}

// exactSession prefixes a session name with "=" so tmux matches it exactly
// instead of by prefix (without it, "opentree-app" would match a session
// named "opentree-app-docs").
func exactSession(name string) string {
	return "=" + name
}

// findWindowID returns the unique tmux window ID (e.g. "@3") for the window
// with the given name. Matching is exact and done in Go: tmux "-t sess:name"
// targets prefix-match window names and parse "." and digits specially, so
// they can silently resolve to the wrong window.
func (c *Controller) findWindowID(name string) (string, error) {
	windowName := c.sanitizeWindowName(name)
	windows, err := c.ListWindows()
	if err != nil {
		return "", err
	}
	for _, w := range windows {
		if w.Name == windowName {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no tmux window named %q", windowName)
}

// shellQuote single-quotes s for safe inclusion in a POSIX shell command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ListWindows returns all windows in the opentree session
func (c *Controller) ListWindows() ([]Window, error) {
	sessionName := c.getSessionName()

	if !c.sessionExists(sessionName) {
		return []Window{}, nil
	}

	// Format: window_id window_active window_name — the name is last so
	// names containing "|" survive parsing (SplitN keeps the remainder).
	cmd := exec.Command("tmux", "list-windows", "-t", exactSession(sessionName), "-F", "#{window_id}|#{window_active}|#{window_name}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list windows: %w", err)
	}

	return c.parseWindows(string(output))
}

// SelectWindow selects a tmux window by name without attaching.
func (c *Controller) SelectWindow(name string) error {
	windowID, err := c.findWindowID(name)
	if err != nil {
		return err
	}
	cmd := exec.Command("tmux", "select-window", "-t", windowID)
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

// AttachWindow attaches to a specific tmux window using the correct
// strategy based on the current environment.
func (c *Controller) AttachWindow(name string) error {
	env := c.detectEnv()
	sessionName := c.getSessionName()

	switch env {
	case envNoTTY:
		return fmt.Errorf("attach requires an interactive terminal (no TTY detected)")

	case envOutsideTmux:
		// Fail on a missing window instead of silently attaching to
		// whatever window happens to be current in the session.
		if err := c.SelectWindow(name); err != nil {
			return err
		}
		cmd := exec.Command("tmux", "attach-session", "-t", exactSession(sessionName))
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
		if err := c.SelectWindow(name); err != nil {
			return err
		}
		cmd := exec.Command("tmux", "switch-client", "-t", exactSession(sessionName))
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

	switch env {
	case envNoTTY:
		return nil, fmt.Errorf("attach requires an interactive terminal (no TTY detected)")

	case envOutsideTmux:
		if err := c.SelectWindow(name); err != nil {
			return nil, err
		}
		return exec.Command("tmux", "attach-session", "-t", exactSession(sessionName)), nil

	case envInsideSameSession:
		windowID, err := c.findWindowID(name)
		if err != nil {
			return nil, err
		}
		return exec.Command("tmux", "select-window", "-t", windowID), nil

	case envInsideDifferentSession:
		if err := c.SelectWindow(name); err != nil {
			return nil, err
		}
		return exec.Command("tmux", "switch-client", "-t", exactSession(sessionName)), nil
	}

	return nil, fmt.Errorf("unknown tmux environment")
}

// KillWindow stops and removes a tmux window
func (c *Controller) KillWindow(name string) error {
	windowID, err := c.findWindowID(name)
	if err != nil {
		return err
	}

	cmd := exec.Command("tmux", "kill-window", "-t", windowID)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to kill window: %w\nOutput: %s", err, output)
	}

	return nil
}

// SendMessage sends a text message to a tmux window as if typed by the user,
// followed by Enter. The text is delivered through a tmux paste buffer with
// bracketed paste so multi-line payloads and words that look like tmux key
// names ("Enter", "C-c", ...) arrive literally instead of being interpreted.
func (c *Controller) SendMessage(name, text string) error {
	target, err := c.findWindowID(name)
	if err != nil {
		return err
	}

	load := exec.Command("tmux", "load-buffer", "-b", "opentree-msg", "-")
	load.Stdin = strings.NewReader(text)
	if output, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to load message buffer: %w\nOutput: %s", err, output)
	}
	paste := exec.Command("tmux", "paste-buffer", "-p", "-d", "-b", "opentree-msg", "-t", target)
	if output, err := paste.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to paste message to window: %w\nOutput: %s", err, output)
	}
	enter := exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if output, err := enter.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to send Enter to window: %w\nOutput: %s", err, output)
	}
	return nil
}

// KillSession stops and removes the tmux session
func (c *Controller) KillSession() error {
	sessionName := c.getSessionName()

	if !c.sessionExists(sessionName) {
		return nil // Session doesn't exist, nothing to do
	}

	// Never kill the session this client is running inside (e.g. the TUI
	// open in a shell window of the opentree session): that would SIGHUP
	// the caller mid-operation. Leaving the session behind is harmless.
	if c.detectEnv() == envInsideSameSession {
		return nil
	}

	cmd := exec.Command("tmux", "kill-session", "-t", exactSession(sessionName))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to kill session: %w\nOutput: %s", err, output)
	}

	return nil
}

// CapturePane captures recent output from a window
func (c *Controller) CapturePane(name string, lines int) (string, error) {
	windowID, err := c.findWindowID(name)
	if err != nil {
		return "", err
	}

	cmd := exec.Command("tmux", "capture-pane", "-t", windowID,
		"-p", "-S", fmt.Sprintf("-%d", lines))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to capture pane: %w", err)
	}

	return string(output), nil
}

// PaneCurrentCommand returns the name of the process currently running in a
// window's active pane (e.g. "zsh", "opencode").
func (c *Controller) PaneCurrentCommand(name string) (string, error) {
	windowID, err := c.findWindowID(name)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{pane_current_command}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get pane command: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetWindowActivity returns the timestamp of the last activity in a tmux window.
func (c *Controller) GetWindowActivity(name string) (time.Time, error) {
	windowID, err := c.findWindowID(name)
	if err != nil {
		return time.Time{}, err
	}
	cmd := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{window_activity}")
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
	// "." and ":" are special in tmux target syntax and invalid in session
	// names; sanitize the configured prefix the same way repoName() is.
	prefix := strings.ReplaceAll(c.sessionPrefix, ".", "-")
	prefix = strings.ReplaceAll(prefix, ":", "-")
	repoName := c.repoName()
	if repoName == "" {
		return prefix
	}
	if prefix == "" {
		return repoName
	}
	return prefix + "-" + repoName
}

// repoName derives a short, sanitized name from the current git repository root.
// The result is computed once and cached.
func (c *Controller) repoName() string {
	c.repoNameOnce.Do(func() {
		root, err := gitutil.RepoRoot()
		if err != nil {
			return
		}
		name := filepath.Base(root)
		// Replace characters that are problematic in tmux session names.
		name = strings.ReplaceAll(name, ".", "-")
		name = strings.ReplaceAll(name, ":", "-")
		c.cachedRepoName = name
	})
	return c.cachedRepoName
}

// sessionExists checks if a tmux session exists
func (c *Controller) sessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", exactSession(name))
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
	return gitutil.SanitizeBranchName(name)
}

// parseWindows parses tmux list-windows output
func (c *Controller) parseWindows(output string) ([]Window, error) {
	var windows []Window

	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		// id|active|name — name is last so names containing "|" stay intact.
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}

		windows = append(windows, Window{
			ID:     parts[0],
			Name:   parts[2],
			Active: parts[1] == "1",
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
