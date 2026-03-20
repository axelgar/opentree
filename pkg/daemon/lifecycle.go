package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lockPath returns the path to the file used for exclusive daemon-start locking.
func lockPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".opentree", "daemon.lock")
}

// SockPath returns the daemon socket path for a given repo root.
func SockPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".opentree", "daemon.sock")
}

// PidPath returns the daemon PID file path for a given repo root.
func PidPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".opentree", "daemon.pid")
}

// EnsureDaemon ensures a daemon is running for the given repo root.
// If the daemon is not running, it starts one and waits for it to be ready.
// A file lock serializes concurrent callers so only one daemon is ever started.
func EnsureDaemon(repoRoot string) error {
	sockPath := SockPath(repoRoot)

	// Fast path: daemon already responding with compatible version — skip the lock.
	switch pingCheck(sockPath) {
	case pingOK:
		return nil
	case pingVersionMismatch:
		// Daemon is alive but running an incompatible protocol version.
		// Stop it so we can start a fresh one below.
		_ = StopDaemon(repoRoot)
		// Give the old daemon a moment to release the socket.
		time.Sleep(200 * time.Millisecond)
	case pingUnreachable:
		// Fall through to start a new daemon.
	}

	// Acquire an exclusive file lock so that concurrent callers (e.g., two
	// CLI invocations in different shells) cannot both observe "daemon not
	// running" and both attempt to launch one.
	opentreeDir := filepath.Join(repoRoot, ".opentree")
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		return fmt.Errorf("failed to create .opentree dir: %w", err)
	}
	lf, err := os.OpenFile(lockPath(repoRoot), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open daemon lock file: %w", err)
	}
	defer lf.Close()

	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire daemon lock: %w", err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck

	// Re-check inside the lock: another process may have started the daemon
	// between our initial check and acquiring the lock.
	switch pingCheck(sockPath) {
	case pingOK:
		return nil
	case pingVersionMismatch:
		_ = StopDaemon(repoRoot)
		time.Sleep(200 * time.Millisecond)
	case pingUnreachable:
		// proceed
	}

	// Clean up stale socket/PID files.
	cleanStale(repoRoot)

	// Start a new daemon process.
	if err := startDaemon(repoRoot); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for daemon to become ready (up to 3 seconds).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ping(sockPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not start within 3 seconds")
}

type pingResult int

const (
	pingOK               pingResult = iota
	pingVersionMismatch             // daemon responded but protocol version differs
	pingUnreachable                 // daemon not running or socket error
)

func pingCheck(sockPath string) pingResult {
	c := &Client{sockPath: sockPath}
	_, err := c.call(MethodPing, nil)
	if err == nil {
		return pingOK
	}
	if strings.Contains(err.Error(), "protocol version mismatch") {
		return pingVersionMismatch
	}
	return pingUnreachable
}

func ping(sockPath string) bool {
	return pingCheck(sockPath) == pingOK
}

func cleanStale(repoRoot string) {
	pidPath := PidPath(repoRoot)
	sockPath := SockPath(repoRoot)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		os.Remove(sockPath)
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(sockPath)
		os.Remove(pidPath)
		return
	}

	// On Unix, FindProcess always succeeds. Use signal 0 to check if alive.
	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(sockPath)
		os.Remove(pidPath)
		return
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// Process is dead — remove stale files.
		os.Remove(sockPath)
		os.Remove(pidPath)
	}
}

func startDaemon(repoRoot string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}

	opentreeDir := filepath.Join(repoRoot, ".opentree")
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		return fmt.Errorf("failed to create .opentree dir: %w", err)
	}

	logPath := filepath.Join(opentreeDir, "daemon.log")
	// Append to preserve crash logs from previous runs.
	// Cap the file at 1 MB to prevent unbounded growth.
	if info, statErr := os.Stat(logPath); statErr == nil && info.Size() > 1<<20 {
		_ = os.Truncate(logPath, 0)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open daemon log: %w", err)
	}

	cmd := exec.Command(exe, "daemon", "start", "--repo-root", repoRoot)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	// Release the child process so it survives our exit.
	logFile.Close()
	cmd.Process.Release()

	return nil
}

// StopDaemon sends SIGTERM to the daemon process.
func StopDaemon(repoRoot string) error {
	pidPath := PidPath(repoRoot)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("no daemon PID file found: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process %d not found: %w", pid, err)
	}

	return proc.Signal(syscall.SIGTERM)
}

// DaemonStatus returns "running" or "stopped" for the daemon.
func DaemonStatus(repoRoot string) string {
	if ping(SockPath(repoRoot)) {
		return "running"
	}
	return "stopped"
}
