package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	// Create an initial commit so the repo has a valid HEAD.
	run("git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "init")

	return dir
}

// ---- parseWorktrees (pure, no git required) ----

func TestParseWorktrees_Empty(t *testing.T) {
	m := &Manager{repoRoot: "/repo", baseDir: ".opentree"}
	wts, err := m.parseWorktrees("")
	if err != nil {
		t.Fatalf("parseWorktrees(\"\") error: %v", err)
	}
	if len(wts) != 0 {
		t.Errorf("expected 0 worktrees, got %d", len(wts))
	}
}

func TestParseWorktrees_MainWorktreeExcluded(t *testing.T) {
	// The main worktree is not under .opentree, so it should be filtered out.
	m := &Manager{repoRoot: "/repo", baseDir: ".opentree"}
	output := `worktree /repo
HEAD abc123
branch refs/heads/main

`
	wts, err := m.parseWorktrees(output)
	if err != nil {
		t.Fatalf("parseWorktrees() error: %v", err)
	}
	if len(wts) != 0 {
		t.Errorf("expected main worktree to be excluded, got %d result(s)", len(wts))
	}
}

func TestParseWorktrees_OpentreeWorktreeIncluded(t *testing.T) {
	m := &Manager{repoRoot: "/repo", baseDir: ".opentree"}
	output := `worktree /repo/.opentree/feature-auth
HEAD def456
branch refs/heads/feature/auth

`
	wts, err := m.parseWorktrees(output)
	if err != nil {
		t.Fatalf("parseWorktrees() error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].Path != "/repo/.opentree/feature-auth" {
		t.Errorf("Path = %q, want %q", wts[0].Path, "/repo/.opentree/feature-auth")
	}
	if wts[0].Branch != "feature/auth" {
		t.Errorf("Branch = %q, want %q", wts[0].Branch, "feature/auth")
	}
}

func TestParseWorktrees_MultipleWorktrees(t *testing.T) {
	m := &Manager{repoRoot: "/repo", baseDir: ".opentree"}
	output := `worktree /repo
HEAD abc123
branch refs/heads/main

worktree /repo/.opentree/feat-a
HEAD bbb111
branch refs/heads/feat/a

worktree /repo/.opentree/fix-b
HEAD ccc222
branch refs/heads/fix/b

`
	wts, err := m.parseWorktrees(output)
	if err != nil {
		t.Fatalf("parseWorktrees() error: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees (main excluded), got %d", len(wts))
	}

	branches := map[string]bool{}
	for _, w := range wts {
		branches[w.Branch] = true
	}
	if !branches["feat/a"] {
		t.Error("expected branch feat/a in results")
	}
	if !branches["fix/b"] {
		t.Error("expected branch fix/b in results")
	}
}

func TestParseWorktrees_DetachedHEAD(t *testing.T) {
	// Detached HEAD worktrees have no branch line.
	m := &Manager{repoRoot: "/repo", baseDir: ".opentree"}
	output := `worktree /repo/.opentree/detached
HEAD abc123
detached

`
	wts, err := m.parseWorktrees(output)
	if err != nil {
		t.Fatalf("parseWorktrees() error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].Branch != "" {
		t.Errorf("Branch for detached HEAD = %q, want empty string", wts[0].Branch)
	}
}

func TestParseWorktrees_TrailingNewline(t *testing.T) {
	m := &Manager{repoRoot: "/repo", baseDir: ".opentree"}
	output := "worktree /repo/.opentree/ws1\nHEAD aaa\nbranch refs/heads/ws1\n\n"
	wts, err := m.parseWorktrees(output)
	if err != nil {
		t.Fatalf("parseWorktrees() error: %v", err)
	}
	if len(wts) != 1 {
		t.Errorf("expected 1 worktree, got %d", len(wts))
	}
}

// ---- ensureGitRepo ----

func TestEnsureGitRepo_OutsideRepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	m := New()
	// Override working dir to a non-git directory.
	m.repoRoot = "" // force re-detection

	// Use a temp dir that is definitely not a git repo.
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	err := m.ensureGitRepo()
	if err == nil {
		t.Fatal("ensureGitRepo() expected error outside git repo, got nil")
	}
}

func TestEnsureGitRepo_InsideRepo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	if err := m.ensureGitRepo(); err != nil {
		t.Fatalf("ensureGitRepo() failed: %v", err)
	}
	if m.repoRoot == "" {
		t.Error("repoRoot not set after ensureGitRepo()")
	}
}

// ---- Create ----

func TestCreate_NewWorktree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	if err := m.ensureGitRepo(); err != nil {
		t.Fatalf("ensureGitRepo() failed: %v", err)
	}

	branchName := "feature/new-thing"
	if err := m.Create(branchName, "HEAD"); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// Directory should use '-' instead of '/'.
	expectedDir := filepath.Join(repoDir, ".opentree", "feature-new-thing")
	if _, err := os.Stat(expectedDir); err != nil {
		t.Errorf("worktree directory not created at %q: %v", expectedDir, err)
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	m.ensureGitRepo()

	if err := m.Create("dup-branch", "HEAD"); err != nil {
		t.Fatalf("Create() first call failed: %v", err)
	}
	err := m.Create("dup-branch", "HEAD")
	if err == nil {
		t.Fatal("Create() second call expected error for existing worktree, got nil")
	}
}

// ---- List ----

func TestList_Empty(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	wts, err := m.List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	// The main worktree is not under .opentree, so none should appear.
	if len(wts) != 0 {
		t.Errorf("List() expected 0 opentree worktrees, got %d", len(wts))
	}
}

func TestList_AfterCreate(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	m.ensureGitRepo()

	branches := []string{"list-a", "list-b"}
	for _, b := range branches {
		if err := m.Create(b, "HEAD"); err != nil {
			t.Fatalf("Create(%q) failed: %v", b, err)
		}
	}

	wts, err := m.List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(wts) != len(branches) {
		t.Errorf("List() returned %d worktrees, want %d", len(wts), len(branches))
	}
}

// ---- Delete ----

func TestDelete_RemovesWorktree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	m.ensureGitRepo()

	branchName := "to-delete"
	if err := m.Create(branchName, "HEAD"); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	if err := m.Delete(branchName, false); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	// Directory should be gone.
	worktreePath := filepath.Join(repoDir, ".opentree", branchName)
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after Delete()")
	}

	// Branch should still exist when deleteBranch=false.
	out, err := exec.Command("git", "-C", repoDir, "branch", "--list", branchName).Output()
	if err != nil {
		t.Fatalf("git branch --list failed: %v", err)
	}
	if !strings.Contains(string(out), branchName) {
		t.Errorf("branch %q should still exist after Delete(deleteBranch=false)", branchName)
	}
}

func TestDelete_WithDeleteBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New()
	m.ensureGitRepo()

	branchName := "branch-to-delete"
	if err := m.Create(branchName, "HEAD"); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	if err := m.Delete(branchName, true); err != nil {
		t.Fatalf("Delete(deleteBranch=true) failed: %v", err)
	}

	// Branch should be gone too.
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", branchName).Output()
	if strings.Contains(string(out), branchName) {
		t.Errorf("branch %q should be deleted after Delete(deleteBranch=true)", branchName)
	}
}
