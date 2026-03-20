package workspace

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"

	"github.com/axelgar/opentree/pkg/gitutil"
)

// maxScrollbackCurBytes is the maximum size of the current-line accumulation
// buffer. A cap prevents a malformed escape sequence with no newline from
// accumulating unbounded memory.
const maxScrollbackCurBytes = 4096

const maxScrollbackLines = 2000

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
	done       chan struct{} // closed when readLoop exits (PTY fully closed)
	procDone   chan struct{} // closed when the main process exits (may precede done)
	exitErr    error
	killed     int32 // atomically set to 1 by KillWindow to prevent double-close

	// respLoopDone is closed by responseLoop when its main goroutine exits.
	// readLoop selects on this to avoid racing with a concurrent vt.Read() call.
	respLoopDone chan struct{}

	// scrollback buffer: accumulates stripped plain-text lines from PTY output.
	sbMu  sync.Mutex
	sbBuf []string // ring buffer, max maxScrollbackLines entries
	sbCur []byte   // current incomplete line bytes
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
	return n.CreateWindowSized(name, workdir, command, n.cols, n.rows, args...)
}

// cleanupExistingWindow checks if a window with the given name exists and, if
// the process has exited, cleans it up. This runs outside the main lock so the
// blocking wait doesn't stall other operations. Returns an error only if the
// window exists and is still actively running.
func (n *NativeProcessManager) cleanupExistingWindow(sanitized, originalName string) error {
	n.mu.RLock()
	existing, exists := n.windows[sanitized]
	n.mu.RUnlock()
	if !exists {
		return nil
	}

	select {
	case <-existing.done:
		// PTY fully closed — remove it.
		n.mu.Lock()
		delete(n.windows, sanitized)
		n.mu.Unlock()
		return nil
	case <-existing.procDone:
		// Main process exited but PTY may still be alive. Force cleanup.
		if existing.cmd.Process != nil {
			_ = syscall.Kill(-existing.cmd.Process.Pid, syscall.SIGKILL)
		}
		existing.pty.Close()
		// Wait outside the lock so we don't block other operations.
		select {
		case <-existing.done:
		case <-time.After(500 * time.Millisecond):
		}
		n.mu.Lock()
		delete(n.windows, sanitized)
		n.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("window %q already exists", originalName)
	}
}

// CreateWindowSized is like CreateWindow but creates the PTY with an explicit
// size instead of the manager's default. Use this when the caller already
// knows the target pane dimensions so the process sees the correct terminal
// size from the very first byte of output.
func (n *NativeProcessManager) CreateWindowSized(name, workdir, command string, cols, rows int, args ...string) error {
	sanitized := gitutil.SanitizeBranchName(name)

	// Try to clean up any existing window for this name before acquiring the
	// main lock, so we don't block other operations during the wait.
	if err := n.cleanupExistingWindow(sanitized, name); err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// Double-check after acquiring lock (another goroutine may have re-created).
	if _, exists := n.windows[sanitized]; exists {
		return fmt.Errorf("window %q already exists", name)
	}

	// Create PTY with the target pane dimensions.
	p, err := xpty.NewPty(cols, rows)
	if err != nil {
		return fmt.Errorf("failed to create PTY: %w", err)
	}

	// Create VT emulator at the same size.
	term := vt.NewSafeEmulator(cols, rows)

	// Build command — pass args directly to avoid shell interpretation issues.
	cmd := exec.Command(command, args...)
	cmd.Dir = workdir
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Start process on PTY
	if err := p.Start(cmd); err != nil {
		p.Close()
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Close the PTY slave in the parent process — the child has inherited it.
	// Keeping it open in the parent would prevent the PTY master from receiving
	// EIO when the child and all its descendants close their copies of the slave.
	if unixPty, ok := p.(*xpty.UnixPty); ok {
		_ = unixPty.Slave().Close()
	}

	w := &ptyWindow{
		name:         sanitized,
		workdir:      workdir,
		cmd:          cmd,
		pty:          p,
		vt:           term,
		done:         make(chan struct{}),
		procDone:     make(chan struct{}),
		respLoopDone: make(chan struct{}),
	}
	w.lastActive.Store(time.Now())

	n.windows[sanitized] = w

	// Start read loop: PTY output → VT emulator
	// Start response loop: VT emulator responses → PTY input
	go n.readLoop(w)
	go n.responseLoop(w)
	// Watch for main process exit independently of PTY lifetime.
	// Claude may exit while child processes still hold the PTY slave open,
	// so we cannot rely on PTY EOF alone to detect agent exit.
	go n.watchProcess(w)

	return nil
}

// watchProcess waits for the main process to exit and signals procDone.
// It then closes the PTY to unblock readLoop in case child processes are
// keeping the PTY slave open after the agent has already exited.
func (n *NativeProcessManager) watchProcess(w *ptyWindow) {
	w.exitErr = w.cmd.Wait()
	close(w.procDone)
	// Close PTY master so readLoop unblocks even if child processes still
	// hold the PTY slave open (e.g., background processes spawned by agent).
	w.pty.Close()
}

// readLoop reads output from the PTY and feeds it to the VT emulator.
func (n *NativeProcessManager) readLoop(w *ptyWindow) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("native: readLoop panic for %q: %v", w.name, r)
			select {
			case <-w.done:
			default:
				close(w.done)
			}
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		nr, err := w.pty.Read(buf)
		if nr > 0 {
			w.vt.Write(buf[:nr])
			w.lastActive.Store(time.Now())
			w.appendToScrollback(buf[:nr])
		}
		if err != nil {
			break
		}
	}

	// We intentionally do NOT call w.vt.Close() here. The responseLoop
	// goroutine exits via its procDone select (set by watchProcess), but the
	// goroutine it spawned for the last vt.Read() call may still be blocked
	// inside the library. Calling vt.Close() concurrently would race on the
	// library-internal Emulator.closed bool (charmbracelet/x/vt does not
	// synchronise Read and Close with a mutex). The one goroutine blocked in
	// vt.Read() is idle and leaks until the program exits — acceptable for a
	// bounded, typically small, number of windows.
	<-w.respLoopDone // wait until responseLoop's own goroutine has returned
	// cmd.Wait() is handled by watchProcess goroutine; don't call it here.
	close(w.done)
}

// responseLoop drains the VT emulator's response pipe and forwards the bytes
// to the PTY process. Terminal emulators write responses to queries (cursor
// position, device attributes, etc.) into this pipe; without a reader the
// pipe blocks and deadlocks readLoop inside SafeEmulator.Write.
//
// Each vt.Read call is delegated to a short-lived goroutine so that the outer
// loop can also select on procDone. This ensures responseLoop exits promptly
// when the hosted process dies and, crucially, closes respLoopDone BEFORE
// readLoop calls vt.Close(). That ordering prevents a data race on the
// library-internal Emulator.closed field (charmbracelet/x/vt issue).
func (n *NativeProcessManager) responseLoop(w *ptyWindow) {
	defer close(w.respLoopDone)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("native: responseLoop panic for %q: %v", w.name, r)
		}
	}()

	type readResult struct {
		data []byte
		err  error
	}

	for {
		buf := make([]byte, 4096)
		ch := make(chan readResult, 1)

		go func() {
			nr, err := w.vt.Read(buf)
			var data []byte
			if nr > 0 {
				data = buf[:nr]
			}
			select {
			case ch <- readResult{data, err}:
			case <-w.procDone:
				// responseLoop already exited; discard this result.
			}
		}()

		select {
		case <-w.procDone:
			// Process has exited. The goroutine above is still blocked in
			// vt.Read(); readLoop will call vt.Close() after we return,
			// which unblocks that goroutine safely via io.Pipe semantics.
			return
		case r := <-ch:
			if len(r.data) > 0 {
				if writeErr := n.writeToPty(w, r.data); writeErr != nil {
					return
				}
			}
			if r.err != nil {
				return
			}
		}
	}
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
	// Atomically mark as killed to prevent concurrent KillWindow calls
	// from double-closing the PTY.
	if !atomic.CompareAndSwapInt32(&w.killed, 0, 1) {
		n.mu.Unlock()
		// Another goroutine is already killing this window. Wait for it.
		<-w.done
		return nil
	}
	n.mu.Unlock()

	// Send SIGTERM to the process group, then close the PTY to unblock
	// the readLoop (which may be stuck in pty.Read).
	if w.cmd.Process != nil {
		if err := syscall.Kill(-w.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			log.Printf("native: SIGTERM pid %d: %v", w.cmd.Process.Pid, err)
		}
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
			if err := syscall.Kill(-w.cmd.Process.Pid, syscall.SIGKILL); err != nil {
				log.Printf("native: SIGKILL pid %d: %v", w.cmd.Process.Pid, err)
			}
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

// ResizeWindow resizes the PTY and VT emulator for a window and notifies the
// process via SIGWINCH so it redraws at the new dimensions.
func (n *NativeProcessManager) ResizeWindow(name string, cols, rows int) error {
	w, err := n.getWindow(name)
	if err != nil {
		return err
	}
	if cols <= 0 || rows <= 0 {
		return nil
	}
	// SafeEmulator.Resize() has its own internal lock; only guard pty.Resize().
	w.vt.Resize(cols, rows)
	w.mu.Lock()
	resizeErr := w.pty.Resize(cols, rows)
	w.mu.Unlock()

	// Deliver SIGWINCH to the process group so the hosted process redraws.
	// TIOCSWINSZ on the PTY master may not trigger SIGWINCH on all platforms.
	if w.cmd.Process != nil {
		_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGWINCH)
	}
	return resizeErr
}

// WriteInput writes raw bytes to a window's PTY (used for forwarding keypresses).
func (n *NativeProcessManager) WriteInput(name string, data []byte) error {
	w, err := n.getWindow(name)
	if err != nil {
		return err
	}
	return n.writeToPty(w, data)
}

// IsRunning returns whether the main agent process is still running.
// It checks procDone (set when the agent exits) rather than done (set when
// the PTY closes), so it returns false as soon as the agent exits even if
// child processes are keeping the PTY slave open.
func (n *NativeProcessManager) IsRunning(name string) bool {
	w, err := n.getWindow(name)
	if err != nil {
		return false
	}
	select {
	case <-w.procDone:
		return false
	default:
		return true
	}
}

// ScrollbackLines returns up to count lines from the scrollback buffer, ending
// at (total - offset). offset=0 returns the most recent lines, offset=N goes
// further back. Returns plain text (ANSI codes stripped).
func (n *NativeProcessManager) ScrollbackLines(name string, offset, count int) ([]string, error) {
	w, err := n.getWindow(name)
	if err != nil {
		return nil, err
	}

	w.sbMu.Lock()
	total := len(w.sbBuf)
	lines := make([]string, total)
	copy(lines, w.sbBuf)
	w.sbMu.Unlock()

	end := total - offset
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}
	start := end - count
	if start < 0 {
		start = 0
	}
	return lines[start:end], nil
}

// appendToScrollback parses raw PTY bytes byte-by-byte, handling \r (carriage
// return overwrites) so that progress indicators only store their final state.
// Completed lines (\n-terminated) are appended to the ring buffer after
// stripping ANSI escape sequences for clean plain-text display.
func (w *ptyWindow) appendToScrollback(data []byte) {
	w.sbMu.Lock()
	defer w.sbMu.Unlock()
	for _, b := range data {
		switch b {
		case '\r':
			// Carriage return: overwrite from start of line.
			w.sbCur = w.sbCur[:0]
		case '\n':
			// Complete line: strip ANSI codes and append to ring buffer.
			line := ansi.Strip(string(w.sbCur))
			w.sbCur = w.sbCur[:0]
			w.sbBuf = append(w.sbBuf, line)
			if len(w.sbBuf) > maxScrollbackLines {
				fresh := make([]string, maxScrollbackLines)
				copy(fresh, w.sbBuf[len(w.sbBuf)-maxScrollbackLines:])
				w.sbBuf = fresh
			}
		default:
			// Cap the current-line buffer to prevent unbounded growth from
			// malformed escape sequences that contain no newline.
			if len(w.sbCur) < maxScrollbackCurBytes {
				w.sbCur = append(w.sbCur, b)
			}
		}
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
