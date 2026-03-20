package workspace

// TerminalProcessManager extends ProcessManager with terminal-specific methods
// for managing PTY windows (rendering, resizing, input forwarding, etc.).
type TerminalProcessManager interface {
	ProcessManager
	CreateWindowSized(name, workdir, command string, cols, rows int, args ...string) error
	RenderScreen(name string) (string, error)
	ResizeWindow(name string, cols, rows int) error
	WriteInput(name string, data []byte) error
	IsRunning(name string) bool
	// ScrollbackLines returns up to count plain-text lines ending at
	// (total-offset) from the scrollback buffer. offset=0 = most recent.
	ScrollbackLines(name string, offset, count int) ([]string, error)
}

// Compile-time check that NativeProcessManager satisfies TerminalProcessManager.
var _ TerminalProcessManager = (*NativeProcessManager)(nil)
