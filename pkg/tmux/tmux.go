package tmux

import (
	"bytes"
	"errors"
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

// ErrNotInstalled is what window creation fails with when there is no tmux on
// PATH. It is named rather than left to exec's "executable file not found in
// $PATH", which arrives wrapped twice over and is the first thing a truncating
// caller cuts — leaving the user with a window that failed and no reason why.
var ErrNotInstalled = errors.New("tmux is not installed — install it (macOS: brew install tmux) and try again")

// Installed reports whether a tmux binary is on PATH. Every workspace's agent
// runs in a tmux window, so this is the difference between opentree working and
// nothing working at all; the dashboard asks once at startup so it can say so
// before the user has tried and failed.
func Installed() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

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

// CreateAppWindow creates a window whose process *is* the command. Nothing is
// typed into a shell, which is the difference that matters for a full-screen
// program: tmux's alternate screen preserves whatever the pane held before the
// program started and puts it back when it exits, so a shell launched first
// leaves its startup output, its prompt and the launch line sitting behind the
// program — reachable by scrolling, and revealed the moment it quits.
//
// Running the program directly leaves the pane with nothing behind it, and the
// window closes when the program does.
func (c *Controller) CreateAppWindow(name, workdir, command string, env []string, args ...string) error {
	// exec so the program replaces the shell rather than being its child;
	// otherwise the pane's process is sh and nothing has really changed.
	_, err := c.newWindow(name, workdir, env, "--", "sh", "-c", launchLine(command, args))
	return err
}

// newWindow creates the window and returns its unique tmux id. Commands target
// the id rather than the name: names prefix-match, and "." or digits in one are
// parsed specially by tmux's target syntax.
func (c *Controller) newWindow(name, workdir string, env []string, trailing ...string) (string, error) {
	// Fail with a clear message before creating a session: on tmux <3.0 the
	// new-window -e flag below would die with an opaque usage error.
	if err := c.checkVersion(); err != nil {
		return "", err
	}

	sessionName := c.getSessionName()
	if !c.sessionExists(sessionName) {
		if err := c.createSession(sessionName); err != nil {
			return "", fmt.Errorf("failed to create tmux session: %w", err)
		}
	} else {
		// Sessions predating this option, or made by hand, get it too — setting
		// it is idempotent and cheaper than explaining why one repo's windows
		// scroll and another's do not.
		c.enableMouse(sessionName)
	}

	args := []string{"new-window", "-t", exactSession(sessionName) + ":",
		"-n", c.sanitizeWindowName(name), "-c", workdir, "-P", "-F", "#{window_id}"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, trailing...)

	output, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create tmux window: %w\nOutput: %s", err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// launchLine is the shell line that starts the window's program. The file
// descriptor limit is raised first so the agents behind the chat do not hit the
// default macOS limit; exec then replaces the shell, so the pane's process is
// the program itself.
func launchLine(command string, args []string) string {
	parts := []string{"exec", command}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return fmt.Sprintf("ulimit -n 2147483646 2>/dev/null; %s", strings.Join(parts, " "))
}

// checkVersion fails when there is no tmux at all, and when the installed one
// predates 3.0 (which newWindow requires for new-window -e). Cached per
// Controller. A tmux that is present but cannot answer -V still passes: the
// command that actually needs it reports that error, and refusing on an
// unreadable version would block unusual builds that work fine.
func (c *Controller) checkVersion() error {
	c.versionOnce.Do(func() {
		if !Installed() {
			c.versionErr = ErrNotInstalled
			return
		}
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

// returnOption is left on a window opentree attaches to, naming how a
// full-screen program running there hands the terminal back to the workspace
// list. Only the attaching client knows the answer — the list may be outside
// tmux, in another window of this session, or in another session entirely —
// and tmux's own "last" targets cover the two inside cases, so the value never
// has to name a target.
const returnOption = "@opentree-return"

const (
	returnDetach  = "detach"
	returnWindow  = "window"
	returnSession = "session"
)

// recordReturn stamps name's window with the way back, best effort: failing to
// record it costs a nicer exit, not the attach that is about to happen.
func (c *Controller) recordReturn(name string, env tmuxEnv) {
	var value string
	switch env {
	case envOutsideTmux:
		value = returnDetach
	case envInsideSameSession:
		value = returnWindow
	case envInsideDifferentSession:
		value = returnSession
	default:
		return
	}
	windowID, err := c.findWindowID(name)
	if err != nil {
		return
	}
	_ = exec.Command("tmux", "set-option", "-w", "-t", windowID, returnOption, value).Run()
}

// ReturnToList sends the client viewing the current pane back to opentree's
// workspace list, and reports whether it could. It is for the programs opentree
// runs as a tmux window's own process: exiting drops the user in whatever
// window tmux picks next, which is not where they came from.
func ReturnToList() bool {
	if os.Getenv("TMUX") == "" {
		return false
	}
	out, err := exec.Command("tmux", "show-options", "-wqv", returnOption).Output()
	if err != nil {
		return false
	}
	args := returnArgs(strings.TrimSpace(string(out)))
	if args == nil {
		return false
	}
	return exec.Command("tmux", args...).Run() == nil
}

// returnArgs maps a recorded return to the tmux command that performs it. A
// missing or unknown value returns nil: a window opentree never attached to is
// one whose caller has to decide for itself what leaving means.
func returnArgs(value string) []string {
	switch value {
	case returnDetach:
		return []string{"detach-client"}
	case returnWindow:
		return []string{"select-window", "-l"}
	case returnSession:
		return []string{"switch-client", "-l"}
	}
	return nil
}

// AttachWindow attaches to a specific tmux window using the correct
// strategy based on the current environment.
func (c *Controller) AttachWindow(name string) error {
	env := c.detectEnv()
	sessionName := c.getSessionName()
	c.recordReturn(name, env)

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
	c.recordReturn(name, env)

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
	c.enableMouse(name)
	return nil
}

// enableMouse makes tmux report mouse events to the program in the pane. tmux
// defaults to mouse off, which does not mean "no mouse" — it means tmux never
// asks the outer terminal for mouse reporting, so the wheel is handled by the
// terminal itself and scrolls its scrollback, walking straight out of whatever
// full-screen view opentree is drawing and into the shell history behind it.
// Both of opentree's views capture the mouse; this is what lets the events
// reach them.
//
// Scoped to opentree's own session with -t rather than -g: the user's global
// tmux preference is theirs, and opentree only speaks for the session it made.
// Best-effort — a tmux too old to know the option is not a reason to fail
// creating the session.
//
// The target needs the trailing colon. set-option rejects a bare "=name" with
// "no such session"; "=name:" is the form that both resolves exactly and is
// accepted, which is why it is what new-window uses too.
func (c *Controller) enableMouse(session string) {
	_ = exec.Command("tmux", "set-option", "-t", exactSession(session)+":", "mouse", "on").Run()
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
