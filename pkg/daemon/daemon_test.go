package daemon

import (
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/axelgar/opentree/pkg/workspace"
)

// startTestServer starts a daemon server on a short temp directory (to stay
// within the ~104-byte Unix socket path limit on macOS) and returns the
// server, a fake repo root whose .opentree/ contains the socket, and a cleanup.
func startTestServer(t *testing.T) (*Server, string, func()) {
	t.Helper()

	// Use a short temp dir to avoid exceeding the Unix socket path limit.
	dir, err := os.MkdirTemp("", "ot")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	opentreeDir := filepath.Join(dir, ".opentree")
	os.MkdirAll(opentreeDir, 0755)

	srv := &Server{
		pm:       workspace.NewNativeProcessManager(80, 24),
		sockPath: filepath.Join(opentreeDir, "d.sock"),
		pidPath:  filepath.Join(opentreeDir, "d.pid"),
		logger:   log.New(os.Stderr, "", 0),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for socket to accept connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(srv.sockPath); err == nil {
			conn, err := net.DialTimeout("unix", srv.sockPath, 500*time.Millisecond)
			if err == nil {
				conn.Close()
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	cleanup := func() {
		srv.shutdown()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
		os.RemoveAll(dir)
	}

	return srv, srv.sockPath, cleanup
}

// newTestClient creates a client that connects to the given socket path directly.
func newTestClient(sockPath string) *Client {
	return &Client{sockPath: sockPath}
}

func TestPing(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)
	_, err := c.call(MethodPing, nil)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestCreateAndListWindows(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("test-win", os.TempDir(), "echo", "hello")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	windows, err := c.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].Name != "test-win" {
		t.Errorf("window name = %q, want %q", windows[0].Name, "test-win")
	}
}

func TestCreateWindowSized(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindowSized("sized-win", os.TempDir(), "echo", 100, 30, "hi")
	if err != nil {
		t.Fatalf("CreateWindowSized: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	windows, err := c.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
}

func TestIsRunning(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	if c.IsRunning("nope") {
		t.Error("IsRunning should return false for non-existent window")
	}

	err := c.CreateWindow("sleeper", os.TempDir(), "sleep", "10")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if !c.IsRunning("sleeper") {
		t.Error("IsRunning should return true for running window")
	}

	if err := c.KillWindow("sleeper"); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}

	if c.IsRunning("sleeper") {
		t.Error("IsRunning should return false after KillWindow")
	}
}

func TestRenderScreen(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("render-win", os.TempDir(), "echo", "hello world")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	screen, err := c.RenderScreen("render-win")
	if err != nil {
		t.Fatalf("RenderScreen: %v", err)
	}

	if screen == "" {
		t.Error("expected non-empty screen output")
	}
}

func TestSendMessage(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("cat-win", os.TempDir(), "cat")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := c.SendMessage("cat-win", "hello from test"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
}

func TestCapturePane(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("cap-win", os.TempDir(), "echo", "captured text")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	output, err := c.CapturePane("cap-win", 10)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if output == "" {
		t.Error("expected non-empty capture output")
	}
}

func TestGetWindowActivity(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("act-win", os.TempDir(), "echo", "activity")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	activity, err := c.GetWindowActivity("act-win")
	if err != nil {
		t.Fatalf("GetWindowActivity: %v", err)
	}
	if activity.IsZero() {
		t.Error("expected non-zero activity time")
	}
}

func TestKillSession(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	c.CreateWindow("win1", os.TempDir(), "sleep", "10")
	c.CreateWindow("win2", os.TempDir(), "sleep", "10")
	time.Sleep(100 * time.Millisecond)

	windows, _ := c.ListWindows()
	if len(windows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(windows))
	}

	if err := c.KillSession(); err != nil {
		t.Fatalf("KillSession: %v", err)
	}

	windows, _ = c.ListWindows()
	if len(windows) != 0 {
		t.Errorf("expected 0 windows after KillSession, got %d", len(windows))
	}
}

func TestSelectWindow_NoOp(t *testing.T) {
	c := &Client{sockPath: "/nonexistent"}
	if err := c.SelectWindow("anything"); err != nil {
		t.Errorf("SelectWindow should be no-op, got error: %v", err)
	}
}

func TestAttachWindow_Error(t *testing.T) {
	c := &Client{sockPath: "/nonexistent"}
	if err := c.AttachWindow("anything"); err == nil {
		t.Error("AttachWindow should return error")
	}
}

func TestAttachCmd_Error(t *testing.T) {
	c := &Client{sockPath: "/nonexistent"}
	_, err := c.AttachCmd("anything")
	if err == nil {
		t.Error("AttachCmd should return error")
	}
}

func TestDaemonUnavailableError(t *testing.T) {
	c := NewClient("/nonexistent/repo")
	_, err := c.call(MethodPing, nil)
	if err == nil {
		t.Fatal("expected error connecting to non-existent socket")
	}

	var dErr *DaemonUnavailableError
	if !errors.As(err, &dErr) {
		t.Errorf("expected DaemonUnavailableError, got %T: %v", err, err)
	}
}

func TestWriteInput(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("input-win", os.TempDir(), "cat")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := c.WriteInput("input-win", []byte("raw input\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
}

func TestResizeWindow(t *testing.T) {
	_, sockPath, cleanup := startTestServer(t)
	defer cleanup()

	c := newTestClient(sockPath)

	err := c.CreateWindow("resize-win", os.TempDir(), "sleep", "10")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := c.ResizeWindow("resize-win", 120, 40); err != nil {
		t.Fatalf("ResizeWindow: %v", err)
	}
}
