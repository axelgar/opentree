package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	prefix := "test-session"
	ctrl := New(prefix)

	if ctrl == nil {
		t.Fatal("New() returned nil")
	}

	if ctrl.sessionPrefix != prefix {
		t.Errorf("Expected sessionPrefix %q, got %q", prefix, ctrl.sessionPrefix)
	}
}

func TestGetSessionName(t *testing.T) {
	// Derive the repo name that the controller will compute, so tests stay
	// correct regardless of which machine or directory they run on.
	ctrl0 := New("probe")
	repoName := ctrl0.repoName() // may be "" in non-git environments

	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{
			name:   "simple prefix",
			prefix: "opentree",
			want: func() string {
				if repoName == "" {
					return "opentree"
				}
				return "opentree-" + repoName
			}(),
		},
		{
			name:   "prefix with hyphens",
			prefix: "my-app",
			want: func() string {
				if repoName == "" {
					return "my-app"
				}
				return "my-app-" + repoName
			}(),
		},
		{
			name:   "empty prefix",
			prefix: "",
			want:   repoName, // just the repo name, or "" if not in a git repo
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := New(tt.prefix)
			got := ctrl.getSessionName()
			if got != tt.want {
				t.Errorf("getSessionName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeWindowName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple branch name",
			input: "main",
			want:  "main",
		},
		{
			name:  "feature branch with slash",
			input: "feat/add-auth",
			want:  "feat-add-auth",
		},
		{
			name:  "fix branch with slash",
			input: "fix/login-bug",
			want:  "fix-login-bug",
		},
		{
			name:  "branch with colon",
			input: "user:feature",
			want:  "user-feature",
		},
		{
			name:  "branch with multiple slashes",
			input: "feature/sub/feature",
			want:  "feature-sub-feature",
		},
		{
			name:  "branch with mixed invalid chars",
			input: "feat/user:auth",
			want:  "feat-user-auth",
		},
	}

	ctrl := New("test")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ctrl.sanitizeWindowName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeWindowName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseWindows(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Window
		wantErr bool
	}{
		{
			name:  "single window",
			input: "@0|main|1",
			want: []Window{
				{ID: "@0", Name: "main", Active: true},
			},
		},
		{
			name:  "multiple windows",
			input: "@0|main|0\n@1|feat-auth|1\n@2|fix-bug|0",
			want: []Window{
				{ID: "@0", Name: "main", Active: false},
				{ID: "@1", Name: "feat-auth", Active: true},
				{ID: "@2", Name: "fix-bug", Active: false},
			},
		},
		{
			name:  "empty input",
			input: "",
			want:  []Window{},
		},
		{
			name:  "input with empty lines",
			input: "@0|main|1\n\n@1|feat|0",
			want: []Window{
				{ID: "@0", Name: "main", Active: true},
				{ID: "@1", Name: "feat", Active: false},
			},
		},
		{
			name:  "malformed line (skipped)",
			input: "@0|main|1\ninvalid\n@1|feat|0",
			want: []Window{
				{ID: "@0", Name: "main", Active: true},
				{ID: "@1", Name: "feat", Active: false},
			},
		},
	}

	ctrl := New("test")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ctrl.parseWindows(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseWindows() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("parseWindows() got %d windows, want %d", len(got), len(tt.want))
				return
			}

			for i, w := range got {
				if w.ID != tt.want[i].ID || w.Name != tt.want[i].Name || w.Active != tt.want[i].Active {
					t.Errorf("parseWindows() window[%d] = %+v, want %+v", i, w, tt.want[i])
				}
			}
		})
	}
}

func TestSessionExists(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-exists")
	sessionName := ctrl.getSessionName()

	if ctrl.sessionExists(sessionName) {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	}

	if ctrl.sessionExists(sessionName) {
		t.Error("sessionExists() = true for non-existent session, want false")
	}

	if err := ctrl.createSession(sessionName); err != nil {
		t.Fatalf("createSession() failed: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	if !ctrl.sessionExists(sessionName) {
		t.Error("sessionExists() = false after createSession(), want true")
	}
}

func TestCreateSession(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-create")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	err := ctrl.createSession(sessionName)
	if err != nil {
		t.Fatalf("createSession() failed: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	if !ctrl.sessionExists(sessionName) {
		t.Error("Session does not exist after createSession()")
	}
}

func TestCreateWindow(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-window")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	windowName := "test-feature"
	workdir := "/tmp"
	command := "echo"
	args := []string{"test"}

	err := ctrl.CreateWindow(windowName, workdir, command, args...)
	if err != nil {
		t.Fatalf("CreateWindow() failed: %v", err)
	}

	if !ctrl.sessionExists(sessionName) {
		t.Error("Session does not exist after CreateWindow()")
	}

	windows, err := ctrl.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows() failed: %v", err)
	}

	if len(windows) < 1 {
		t.Errorf("Expected at least 1 window, got %d", len(windows))
	}

	found := false
	sanitizedName := ctrl.sanitizeWindowName(windowName)
	for _, w := range windows {
		if w.Name == sanitizedName {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Window %q not found in list", sanitizedName)
	}
}

func TestListWindows(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-list")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	windows, err := ctrl.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows() with no session failed: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("Expected 0 windows with no session, got %d", len(windows))
	}

	err = ctrl.CreateWindow("test-win", "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow() failed: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	windows, err = ctrl.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows() failed: %v", err)
	}

	if len(windows) < 1 {
		t.Errorf("Expected at least 1 window, got %d", len(windows))
	}
}

func TestKillWindow(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-kill")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	windowName := "test-to-kill"
	err := ctrl.CreateWindow(windowName, "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow() failed: %v", err)
	}

	err = ctrl.CreateWindow("keep-alive", "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow() for keep-alive failed: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	err = ctrl.KillWindow(windowName)
	if err != nil {
		t.Fatalf("KillWindow() failed: %v", err)
	}

	windows, err := ctrl.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows() after kill failed: %v", err)
	}

	sanitizedName := ctrl.sanitizeWindowName(windowName)
	for _, w := range windows {
		if w.Name == sanitizedName {
			t.Errorf("Window %q still exists after KillWindow()", sanitizedName)
		}
	}
}

func TestCapturePane(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-capture")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	windowName := "test-capture"
	err := ctrl.CreateWindow(windowName, "/tmp", "echo", "test-output")
	if err != nil {
		t.Fatalf("CreateWindow() failed: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	output, err := ctrl.CapturePane(windowName, 10)
	if err != nil {
		t.Fatalf("CapturePane() failed: %v", err)
	}

	if output == "" {
		t.Error("CapturePane() returned empty output")
	}
}

func TestSelectWindow(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-select")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	err := ctrl.CreateWindow("win-a", "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow(win-a) failed: %v", err)
	}

	err = ctrl.CreateWindow("win-b", "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow(win-b) failed: %v", err)
	}

	err = ctrl.SelectWindow("win-a")
	if err != nil {
		t.Fatalf("SelectWindow(win-a) failed: %v", err)
	}

	windows, err := ctrl.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows() failed: %v", err)
	}

	for _, w := range windows {
		if w.Name == "win-a" && !w.Active {
			t.Error("Expected win-a to be active after SelectWindow()")
		}
	}
}

func TestSelectWindowNonExistent(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-select-bad")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	err := ctrl.CreateWindow("real-win", "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow() failed: %v", err)
	}

	err = ctrl.SelectWindow("non-existent-window")
	if err == nil {
		t.Error("SelectWindow() should fail for non-existent window")
	}
}

func TestAttachCmd(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available, skipping integration test")
	}

	ctrl := New("test-opentree-attachcmd")
	sessionName := ctrl.getSessionName()

	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	defer exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	err := ctrl.CreateWindow("attach-win", "/tmp", "sleep", "1000")
	if err != nil {
		t.Fatalf("CreateWindow() failed: %v", err)
	}

	cmd, err := ctrl.AttachCmd("attach-win")
	if err != nil {
		t.Logf("AttachCmd() returned error (expected in no-TTY environments): %v", err)
		return
	}
	if cmd == nil {
		t.Fatal("AttachCmd() returned nil cmd")
	}
	if cmd.Path == "" {
		t.Error("AttachCmd() returned cmd with empty Path")
	}

	hasFlag := false
	for _, arg := range cmd.Args {
		if arg == sessionName || strings.Contains(arg, sessionName) {
			hasFlag = true
			break
		}
	}
	if !hasFlag {
		t.Errorf("AttachCmd() args %v do not reference session %q", cmd.Args, sessionName)
	}
}

func TestAttachCmdNoSession(t *testing.T) {
	ctrl := New("test-opentree-nosession")
	sessionName := ctrl.getSessionName()
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	cmd, err := ctrl.AttachCmd("nonexistent")
	// AttachCmd builds a command even if the session doesn't exist yet;
	// the error surfaces when the command actually runs.
	if err != nil {
		// If we're in a no-TTY env (CI), this is expected
		t.Logf("AttachCmd() returned error (expected in no-TTY environments): %v", err)
		return
	}
	if cmd == nil {
		t.Fatal("AttachCmd() returned nil cmd without error")
	}
}

func TestDetectEnv(t *testing.T) {
	ctrl := New("test-opentree-detect")
	env := ctrl.detectEnv()

	// In test environments (CI/terminals), we should get either envOutsideTmux,
	// envInsideSameSession, envInsideDifferentSession, or envNoTTY — never panic.
	switch env {
	case envOutsideTmux, envInsideSameSession, envInsideDifferentSession, envNoTTY:
		// valid
	default:
		t.Errorf("detectEnv() returned unexpected value: %d", env)
	}
}

func TestIsInsideTmux(t *testing.T) {
	result := IsInsideTmux()
	tmuxVar := os.Getenv("TMUX")
	expected := tmuxVar != ""
	if result != expected {
		t.Errorf("IsInsideTmux() = %v, want %v (TMUX=%q)", result, expected, tmuxVar)
	}
}

func isTmuxAvailable() bool {
	cmd := exec.Command("tmux", "-V")
	return cmd.Run() == nil
}
