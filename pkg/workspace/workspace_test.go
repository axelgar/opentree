package workspace

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
)

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

	svc, err := New(repoDir, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create workspace — tmux will fail (no server), but worktree + state should succeed up to that point.
	// We test the parts that don't require tmux by checking the worktree was created.
	ws, err := svc.Create("test-branch", "main")
	if err != nil {
		// tmux failure is expected in tests — check if worktree was at least created
		worktreePath := svc.WorktreePath("test-branch")
		if !dirExists(worktreePath) {
			t.Fatalf("Create failed and worktree not created: %v", err)
		}
		t.Skipf("Create partially succeeded (tmux unavailable): %v", err)
	}

	if ws.Name != "test-branch" {
		t.Errorf("ws.Name = %q, want %q", ws.Name, "test-branch")
	}
	if ws.BaseBranch != "main" {
		t.Errorf("ws.BaseBranch = %q, want %q", ws.BaseBranch, "main")
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

func dirExists(path string) bool {
	info, err := exec.Command("test", "-d", path).CombinedOutput()
	_ = info
	return err == nil
}
