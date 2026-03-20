package workspace

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls fn every 50ms until it returns true or the deadline (2s) expires.
func waitFor(t *testing.T, desc string, fn func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if fn() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for: %s", desc)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestNativeCreateAndListWindows(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-echo", t.TempDir(), "echo", "hello world")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	waitFor(t, "window to appear", func() bool {
		windows, _ := pm.ListWindows()
		return len(windows) == 1
	})

	windows, err := pm.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if windows[0].Name != "test-echo" {
		t.Errorf("expected name test-echo, got %q", windows[0].Name)
	}
}

func TestNativeCapturePane(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-cap", t.TempDir(), "echo", "capture-me-123")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	waitFor(t, "capture output to contain expected string", func() bool {
		output, err := pm.CapturePane("test-cap", 5)
		return err == nil && strings.Contains(output, "capture-me-123")
	})
}

func TestNativeRenderScreen(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-render", t.TempDir(), "echo", "render-test-456")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	waitFor(t, "render screen to contain expected string", func() bool {
		screen, err := pm.RenderScreen("test-render")
		return err == nil && strings.Contains(screen, "render-test-456")
	})
}

func TestNativeKillWindow(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-kill", t.TempDir(), "sleep", "60")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	if !pm.IsRunning("test-kill") {
		t.Fatal("expected process to be running")
	}

	err = pm.KillWindow("test-kill")
	if err != nil {
		t.Fatalf("KillWindow: %v", err)
	}

	windows, err := pm.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("expected 0 windows after kill, got %d", len(windows))
	}
}

func TestNativeSendMessage(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-cat", t.TempDir(), "cat")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	waitFor(t, "cat process to be running", func() bool {
		return pm.IsRunning("test-cat")
	})

	err = pm.SendMessage("test-cat", "hello from test")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	waitFor(t, "output to contain sent message", func() bool {
		output, err := pm.CapturePane("test-cat", 5)
		return err == nil && strings.Contains(output, "hello from test")
	})
}

func TestNativeGetWindowActivity(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	before := time.Now()
	err := pm.CreateWindow("test-activity", t.TempDir(), "echo", "hi")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	waitFor(t, "activity timestamp to be set", func() bool {
		activity, err := pm.GetWindowActivity("test-activity")
		return err == nil && !activity.Before(before)
	})
}

func TestNativeAttachReturnsError(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)

	err := pm.AttachWindow("anything")
	if err == nil {
		t.Fatal("expected error from AttachWindow")
	}

	_, err = pm.AttachCmd("anything")
	if err == nil {
		t.Fatal("expected error from AttachCmd")
	}
}

func TestNativeDuplicateWindow(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-dup", t.TempDir(), "sleep", "60")
	if err != nil {
		t.Fatalf("first CreateWindow: %v", err)
	}

	err = pm.CreateWindow("test-dup", t.TempDir(), "sleep", "60")
	if err == nil {
		t.Fatal("expected error creating duplicate window")
	}
}

func TestNativeResizeWindow(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-resize", t.TempDir(), "sleep", "60")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	err = pm.ResizeWindow("test-resize", 120, 40)
	if err != nil {
		t.Fatalf("ResizeWindow: %v", err)
	}
}

func TestNativeWindowNotFound(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)

	_, err := pm.CapturePane("nonexistent", 5)
	if err == nil {
		t.Fatal("expected error for nonexistent window")
	}

	_, err = pm.GetWindowActivity("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent window")
	}

	err = pm.SendMessage("nonexistent", "test")
	if err == nil {
		t.Fatal("expected error for nonexistent window")
	}
}

// ---------------------------------------------------------------------------
// Concurrency / race condition tests (run with -race)
// ---------------------------------------------------------------------------

// TestConcurrentCreateWindow verifies that simultaneous CreateWindow calls for
// the same window name result in exactly one success and the rest returning
// "already exists" errors.
func TestConcurrentCreateWindow(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	const workers = 5
	var (
		successCount int32
		wg           sync.WaitGroup
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if err := pm.CreateWindow("concurrent-window", t.TempDir(), "sleep", "60"); err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful CreateWindow, got %d", successCount)
	}
}

// ---------------------------------------------------------------------------
// Scrollback buffer tests
// ---------------------------------------------------------------------------

// TestScrollbackRingBuffer verifies that the scrollback ring buffer caps at
// maxScrollbackLines even when more lines are produced.
func TestScrollbackRingBuffer(t *testing.T) {
	const extraLines = 500
	w := &ptyWindow{}

	for i := 0; i < maxScrollbackLines+extraLines; i++ {
		w.appendToScrollback([]byte(fmt.Sprintf("line-%d\n", i)))
	}

	w.sbMu.Lock()
	total := len(w.sbBuf)
	w.sbMu.Unlock()

	if total != maxScrollbackLines {
		t.Errorf("scrollback length = %d, want %d (ring buffer cap)", total, maxScrollbackLines)
	}
}

// TestScrollbackCurByteCap verifies that the current-line accumulation buffer
// does not grow beyond maxScrollbackCurBytes even when no newline is received.
func TestScrollbackCurByteCap(t *testing.T) {
	w := &ptyWindow{}
	// Feed 2x the cap without any newline.
	data := make([]byte, maxScrollbackCurBytes*2)
	for i := range data {
		data[i] = 'a'
	}
	w.appendToScrollback(data)

	w.sbMu.Lock()
	curLen := len(w.sbCur)
	w.sbMu.Unlock()

	if curLen > maxScrollbackCurBytes {
		t.Errorf("sbCur length = %d, want <= %d", curLen, maxScrollbackCurBytes)
	}
}
