package workspace

import (
	"strings"
	"testing"
	"time"
)

func TestNativeCreateAndListWindows(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-echo", t.TempDir(), "echo", "hello world")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	// Wait for process to finish
	time.Sleep(200 * time.Millisecond)

	windows, err := pm.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
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

	// Wait for echo to write output
	time.Sleep(300 * time.Millisecond)

	output, err := pm.CapturePane("test-cap", 5)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(output, "capture-me-123") {
		t.Errorf("expected output to contain 'capture-me-123', got %q", output)
	}
}

func TestNativeRenderScreen(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	err := pm.CreateWindow("test-render", t.TempDir(), "echo", "render-test-456")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	screen, err := pm.RenderScreen("test-render")
	if err != nil {
		t.Fatalf("RenderScreen: %v", err)
	}
	if !strings.Contains(screen, "render-test-456") {
		t.Errorf("expected screen to contain 'render-test-456', got %q", screen)
	}
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
	time.Sleep(100 * time.Millisecond)

	err = pm.SendMessage("test-cat", "hello from test")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	output, err := pm.CapturePane("test-cat", 5)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(output, "hello from test") {
		t.Errorf("expected output to contain 'hello from test', got %q", output)
	}
}

func TestNativeGetWindowActivity(t *testing.T) {
	pm := NewNativeProcessManager(80, 24)
	defer pm.KillSession()

	before := time.Now()
	err := pm.CreateWindow("test-activity", t.TempDir(), "echo", "hi")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	activity, err := pm.GetWindowActivity("test-activity")
	if err != nil {
		t.Fatalf("GetWindowActivity: %v", err)
	}
	if activity.Before(before) {
		t.Errorf("expected activity after %v, got %v", before, activity)
	}
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
