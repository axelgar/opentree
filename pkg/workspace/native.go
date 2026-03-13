package workspace

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"

	"github.com/axelgar/opentree/pkg/gitutil"
)

// Compile-time check that NativeProcessManager satisfies ProcessManager.
var _ ProcessManager = (*NativeProcessManager)(nil)

// ptyWindow represents a single process running in a PTY with a virtual
// terminal emulator capturing its output.
type ptyWindow struct {
	name    string
	workdir string
	cmd     *exec.Cmd
	pty     xpty.Pty
	vt      *vt.SafeEmulator

	mu         sync.Mutex   // guards pty writes
	lastActive atomic.Value // stores time.Time
	done       chan struct{}
	exitErr    error
}

// NativeProcessManager manages processes in in-process PTYs with virtual
// terminal emulators, replacing tmux as the ProcessManager backend.
type NativeProcessManager struct {
	mu      sync.RWMutex
	windows map[string]*ptyWindow
	cols    int
	rows    int
}

// NewNativeProcessManager creates a new NativeProcessManager.
// If cols or rows are 0, defaults of 120 and 40 are used.
func NewNativeProcessManager(cols, rows int) *NativeProcessManager {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 40
	}
	return &NativeProcessManager{
		windows: make(map[string]*ptyWindow),
		cols:    cols,
		rows:    rows,
	}
}

func (n *NativeProcessManager) CreateWindow(name, workdir, command string, args ...string) error {
	sanitized := gitutil.SanitizeBranchName(name)

	n.mu.Lock()
	if _, exists := n.windows[sanitized]; exists {
		n.mu.Unlock()
		return fmt.Errorf("window %q already exists", name)
	}
	n.mu.Unlock()

	// Build the full command with args
	var shellCmd string
	if len(args) > 0 {
		shellCmd = command + " " + strings.Join(args, " ")
	} else {
		shellCmd = command
	}

	// Create PTY
	pty, err := xpty.NewPty(n.cols, n.rows)
	if err != nil {
		return fmt.Errorf("failed to create PTY: %w", err)
	}

	// Create VT emulator
	term := vt.NewSafeEmulator(n.cols, n.rows)

	// Build command
	cmd := exec.Command("sh", "-c", shellCmd)
	cmd.Dir = workdir
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Start process on PTY
	if err := pty.Start(cmd); err != nil {
		pty.Close()
		return fmt.Errorf("failed to start process: %w", err)
	}

	w := &ptyWindow{
		name:    sanitized,
		workdir: workdir,
		cmd:     cmd,
		pty:     pty,
		vt:      term,
		done:    make(chan struct{}),
	}
	w.lastActive.Store(time.Now())

	n.mu.Lock()
	n.windows[sanitized] = w
	n.mu.Unlock()

	// Start read loop: PTY output → VT emulator
	go n.readLoop(w)

	return nil
}

// readLoop reads output from the PTY and feeds it to the VT emulator.
func (n *NativeProcessManager) readLoop(w *ptyWindow) {
	buf := make([]byte, 32*1024)
	for {
		nr, err := w.pty.Read(buf)
		if nr > 0 {
			w.vt.Write(buf[:nr])
			w.lastActive.Store(time.Now())
		}
		if err != nil {
			break
		}
	}

	// Process exited — wait for it and record exit error
	w.exitErr = w.cmd.Wait()
	close(w.done)
}

func (n *NativeProcessManager) ListWindows() ([]Window, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	windows := make([]Window, 0, len(n.windows))
	for _, w := range n.windows {
		active := true
		select {
		case <-w.done:
			active = false
		default:
		}
		windows = append(windows, Window{
			ID:     w.name,
			Name:   w.name,
			Active: active,
		})
	}
	return windows, nil
}

func (n *NativeProcessManager) SelectWindow(_ string) error {
	// No-op for native PTY — selection is handled in TUI
	return nil
}

func (n *NativeProcessManager) AttachWindow(_ string) error {
	return fmt.Errorf("attach is not supported with native PTY — use the TUI terminal view")
}

func (n *NativeProcessManager) AttachCmd(_ string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("attach is not supported with native PTY — use the TUI terminal view")
}

func (n *NativeProcessManager) KillWindow(name string) error {
	sanitized := gitutil.SanitizeBranchName(name)

	n.mu.Lock()
	w, exists := n.windows[sanitized]
	if !exists {
		w, exists = n.windows[name]
		if !exists {
			n.mu.Unlock()
			return nil // already gone
		}
		sanitized = name
	}
	n.mu.Unlock()

	// Send SIGTERM to the process group, then close the PTY to unblock
	// the readLoop (which may be stuck in pty.Read).
	if w.cmd.Process != nil {
		_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGTERM)
	}

	// Close PTY to unblock readLoop's pty.Read
	w.pty.Close()

	// Wait for readLoop to finish (with timeout for safety)
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-w.done:
		timer.Stop()
	case <-timer.C:
		// Force kill if still alive
		if w.cmd.Process != nil {
			_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGKILL)
		}
		<-w.done
	}

	// Remove from map after process is dead to prevent concurrent callers
	// from seeing the window as gone while the process is still running.
	n.mu.Lock()
	delete(n.windows, sanitized)
	n.mu.Unlock()

	return nil
}

func (n *NativeProcessManager) KillSession() error {
	n.mu.Lock()
	names := make([]string, 0, len(n.windows))
	for name := range n.windows {
		names = append(names, name)
	}
	n.mu.Unlock()

	for _, name := range names {
		_ = n.KillWindow(name)
	}
	return nil
}

func (n *NativeProcessManager) CapturePane(name string, lines int) (string, error) {
	w, err := n.getWindow(name)
	if err != nil {
		return "", err
	}

	rendered := w.vt.Render()
	allLines := strings.Split(rendered, "\n")

	// Trim trailing blank lines only (preserve blank lines in the middle of output)
	end := len(allLines)
	for end > 0 && strings.TrimSpace(allLines[end-1]) == "" {
		end--
	}
	allLines = allLines[:end]

	// Return last N lines
	if lines > 0 && len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}
	return strings.Join(allLines, "\n"), nil
}

func (n *NativeProcessManager) GetWindowActivity(name string) (time.Time, error) {
	w, err := n.getWindow(name)
	if err != nil {
		return time.Time{}, err
	}
	if t, ok := w.lastActive.Load().(time.Time); ok {
		return t, nil
	}
	return time.Time{}, nil
}

func (n *NativeProcessManager) SendMessage(name, text string) error {
	w, err := n.getWindow(name)
	if err != nil {
		return err
	}
	return n.writeToPty(w, []byte(text+"\n"))
}

// -- Extra public methods for TUI --

// RenderScreen returns the current VT screen content as a string with ANSI codes.
func (n *NativeProcessManager) RenderScreen(name string) (string, error) {
	w, err := n.getWindow(name)
	if err != nil {
		return "", err
	}
	return w.vt.Render(), nil
}

// ResizeWindow resizes the PTY and VT emulator for a window.
func (n *NativeProcessManager) ResizeWindow(name string, cols, rows int) error {
	w, err := n.getWindow(name)
	if err != nil {
		return err
	}
	if cols <= 0 || rows <= 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.vt.Resize(cols, rows)
	return w.pty.Resize(cols, rows)
}

// WriteInput writes raw bytes to a window's PTY (used for forwarding keypresses).
func (n *NativeProcessManager) WriteInput(name string, data []byte) error {
	w, err := n.getWindow(name)
	if err != nil {
		return err
	}
	return n.writeToPty(w, data)
}

// IsRunning returns whether a window's process is still running.
func (n *NativeProcessManager) IsRunning(name string) bool {
	w, err := n.getWindow(name)
	if err != nil {
		return false
	}
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

// -- Internal helpers --

func (n *NativeProcessManager) getWindow(name string) (*ptyWindow, error) {
	sanitized := gitutil.SanitizeBranchName(name)

	n.mu.RLock()
	defer n.mu.RUnlock()

	if w, ok := n.windows[sanitized]; ok {
		return w, nil
	}
	if w, ok := n.windows[name]; ok {
		return w, nil
	}
	return nil, fmt.Errorf("window %q not found", name)
}

func (n *NativeProcessManager) writeToPty(w *ptyWindow, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.pty.Write(data)
	return err
}
