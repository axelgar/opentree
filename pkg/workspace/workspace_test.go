package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/worktree"
)

// mockProcessManager is a test double for ProcessManager that records calls
// and returns configurable results.
type mockProcessManager struct {
	createWindowCalls []string
	killWindowCalls   []string
	killSessionCalled bool
}

func (m *mockProcessManager) CreateWindow(name, workdir, command string, args ...string) error {
	m.createWindowCalls = append(m.createWindowCalls, name)
	return nil
}

func (m *mockProcessManager) ListWindows() ([]Window, error) { return nil, nil }
func (m *mockProcessManager) SelectWindow(name string) error  { return nil }
func (m *mockProcessManager) AttachWindow(name string) error   { return nil }
func (m *mockProcessManager) AttachCmd(name string) (*exec.Cmd, error) {
	return exec.Command("echo", "mock"), nil
}
func (m *mockProcessManager) KillWindow(name string) error {
	m.killWindowCalls = append(m.killWindowCalls, name)
	return nil
}
func (m *mockProcessManager) KillSession() error {
	m.killSessionCalled = true
	return nil
}
func (m *mockProcessManager) CapturePane(name string, lines int) (string, error) { return "", nil }
func (m *mockProcessManager) GetWindowActivity(name string) (time.Time, error) {
	return time.Time{}, nil
}

func TestWorktreePath(t *testing.T) {
	cfg := config.Default()
	cfg.Worktree.BaseDir = ".opentree"
	svc := &Service{repoRoot: "/repo", cfg: cfg}

	tests := []struct {
		name string
		want string
	}{
		{"feature-auth", "/repo/.opentree/feature-auth"},
		{"feature/auth", "/repo/.opentree/feature-auth"},
		{"feat:thing", "/repo/.opentree/feat-thing"},
	}

	for _, tt := range tests {
		got := svc.WorktreePath(tt.name)
		if got != tt.want {
			t.Errorf("WorktreePath(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestWorktreePath_CustomBaseDir(t *testing.T) {
	cfg := config.Default()
	cfg.Worktree.BaseDir = "worktrees"
	svc := &Service{repoRoot: "/home/user/project", cfg: cfg}

	got := svc.WorktreePath("my-branch")
	want := "/home/user/project/worktrees/my-branch"
	if got != want {
		t.Errorf("WorktreePath with custom BaseDir = %q, want %q", got, want)
	}
}

// isGitAvailable returns true when git is found on PATH.
func isGitAvailable() bool {
	return exec.Command("git", "--version").Run() == nil
}

// initGitRepo creates a temporary git repository and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	run("git", "init")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "config", "gpg.format", "openpgp")
	run("git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "init")
	// Ensure the default branch is called "main" for test consistency.
	run("git", "branch", "-M", "main")

	return dir
}

func TestCreateAndDelete(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	ws, err := svc.Create("test-branch", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if ws.Name != "test-branch" {
		t.Errorf("ws.Name = %q, want %q", ws.Name, "test-branch")
	}
	if ws.BaseBranch != "main" {
		t.Errorf("ws.BaseBranch = %q, want %q", ws.BaseBranch, "main")
	}
	if len(mock.createWindowCalls) != 1 || mock.createWindowCalls[0] != "test-branch" {
		t.Errorf("expected CreateWindow called with test-branch, got %v", mock.createWindowCalls)
	}

	worktreePath := svc.WorktreePath("test-branch")
	if !dirExists(worktreePath) {
		t.Error("worktree directory should exist after Create")
	}

	// Delete
	if err := svc.Delete("test-branch"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if dirExists(worktreePath) {
		t.Error("worktree directory should not exist after Delete")
	}

	workspaces := svc.state.ListWorkspaces()
	if len(workspaces) != 0 {
		t.Errorf("expected 0 workspaces after Delete, got %d", len(workspaces))
	}
	if !mock.killSessionCalled {
		t.Error("expected KillSession to be called when last workspace deleted")
	}
}

func TestDeleteMultiple(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	// Create two workspaces
	if _, err := svc.Create("branch-a", "main"); err != nil {
		t.Fatalf("Create branch-a: %v", err)
	}
	if _, err := svc.Create("branch-b", "main"); err != nil {
		t.Fatalf("Create branch-b: %v", err)
	}

	// Delete both
	if err := svc.DeleteMultiple([]string{"branch-a", "branch-b"}); err != nil {
		t.Fatalf("DeleteMultiple: %v", err)
	}

	if len(mock.killWindowCalls) != 2 {
		t.Errorf("expected 2 KillWindow calls, got %d", len(mock.killWindowCalls))
	}
	if !mock.killSessionCalled {
		t.Error("expected KillSession after deleting all workspaces")
	}
}

// newWithMock creates a Service with a mock ProcessManager for testing.
func newWithMock(repoRoot string, cfg *config.Config, pm ProcessManager) (*Service, error) {
	wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
	st, err := state.New(repoRoot)
	if err != nil {
		return nil, err
	}
	return NewService(repoRoot, cfg, wt, pm, st, nil), nil
}

// initRepoWithRemote creates a bare "origin" repo, clones it locally, and
// pushes branchName to origin. Returns the local clone directory.
func initRepoWithRemote(t *testing.T, branchName string) string {
	t.Helper()
	remoteDir := t.TempDir()
	localDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	runIn(remoteDir, "git", "init", "--bare")
	runIn(localDir, "git", "clone", remoteDir, ".")
	runIn(localDir, "git", "config", "user.email", "test@example.com")
	runIn(localDir, "git", "config", "user.name", "Test")
	runIn(localDir, "git", "config", "commit.gpgsign", "false")
	runIn(localDir, "git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "init")
	runIn(localDir, "git", "push", "origin", "HEAD:main")
	runIn(localDir, "git", "checkout", "-b", branchName)
	runIn(localDir, "git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "feat commit")
	runIn(localDir, "git", "push", "origin", branchName)
	runIn(localDir, "git", "checkout", "main")
	return localDir
}

func TestCreateFromRemoteBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/remote-thing")
	cfg := config.Default()
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(localDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	ws, err := svc.CreateFromRemoteBranch("feat/remote-thing")
	if err != nil {
		t.Fatalf("CreateFromRemoteBranch: %v", err)
	}

	if ws.Name != "feat/remote-thing" {
		t.Errorf("ws.Name = %q, want %q", ws.Name, "feat/remote-thing")
	}
	if ws.BaseBranch != "" {
		t.Errorf("ws.BaseBranch = %q, want empty string", ws.BaseBranch)
	}
	if !ws.BranchPushed {
		t.Error("ws.BranchPushed should be true for a remote branch workspace")
	}
	if len(mock.createWindowCalls) != 1 || mock.createWindowCalls[0] != "feat/remote-thing" {
		t.Errorf("expected CreateWindow called with feat/remote-thing, got %v", mock.createWindowCalls)
	}

	worktreePath := svc.WorktreePath("feat/remote-thing")
	if !dirExists(worktreePath) {
		t.Error("worktree directory should exist after CreateFromRemoteBranch")
	}

	agentsFile := filepath.Join(worktreePath, "AGENTS.md")
	if _, err := os.Stat(agentsFile); err != nil {
		t.Errorf("AGENTS.md not created: %v", err)
	}
}

func TestHasChanges_NoWorkspace(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()

	svc, err := New(repoDir, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// HasChanges on non-existent workspace should return empty string, no error
	diff, err := svc.HasChanges("nonexistent")
	if err != nil {
		t.Errorf("HasChanges on nonexistent: unexpected error: %v", err)
	}
	if diff != "" {
		t.Errorf("HasChanges on nonexistent: expected empty diff, got %q", diff)
	}
}

func TestNewService_NilFields(t *testing.T) {
	cfg := config.Default()
	svc := NewService("/repo", cfg, nil, nil, nil, nil)
	if svc.repoRoot != "/repo" {
		t.Errorf("repoRoot = %q, want %q", svc.repoRoot, "/repo")
	}
	if svc.cfg != cfg {
		t.Error("cfg not set correctly")
	}
}

func TestSanitizeBranchNameInPath(t *testing.T) {
	cfg := config.Default()
	cfg.Worktree.BaseDir = ".opentree"
	svc := &Service{repoRoot: "/repo", cfg: cfg}

	// Verify that SanitizeBranchName is applied correctly
	path := svc.WorktreePath("feature/auth:v2")
	expected := filepath.Join("/repo", ".opentree", gitutil.SanitizeBranchName("feature/auth:v2"))
	if path != expected {
		t.Errorf("WorktreePath = %q, want %q", path, expected)
	}
}

func TestCreate_WritesAgentsFile(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	ws, err := svc.Create("agents-test", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	agentsFile := filepath.Join(ws.WorktreeDir, "AGENTS.md")
	data, err := os.ReadFile(agentsFile)
	if err != nil {
		t.Fatalf("AGENTS.md should exist after Create: %v", err)
	}
	if !strings.Contains(string(data), ".opentree-status.json") {
		t.Error("AGENTS.md should mention .opentree-status.json")
	}
}

func dirExists(path string) bool {
	info, err := exec.Command("test", "-d", path).CombinedOutput()
	_ = info
	return err == nil
}
