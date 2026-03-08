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

// ---- Create ----

func TestCreate_NewWorktree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)

	m := New(repoDir, ".opentree")

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

	m := New(repoDir, ".opentree")

	if err := m.Create("dup-branch", "HEAD"); err != nil {
		t.Fatalf("Create() first call failed: %v", err)
	}
	err := m.Create("dup-branch", "HEAD")
	if err == nil {
		t.Fatal("Create() second call expected error for existing worktree, got nil")
	}
}

// ---- CreateFromRemote ----

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

func TestCreateFromRemote_Success(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/remote-thing")
	m := New(localDir, ".opentree")

	if err := m.CreateFromRemote("feat/remote-thing"); err != nil {
		t.Fatalf("CreateFromRemote() failed: %v", err)
	}

	expectedDir := filepath.Join(localDir, ".opentree", "feat-remote-thing")
	if _, err := os.Stat(expectedDir); err != nil {
		t.Errorf("worktree directory not created at %q: %v", expectedDir, err)
	}
}

func TestCreateFromRemote_AlreadyExists(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/dup-remote")
	m := New(localDir, ".opentree")

	if err := m.CreateFromRemote("feat/dup-remote"); err != nil {
		t.Fatalf("CreateFromRemote() first call failed: %v", err)
	}
	err := m.CreateFromRemote("feat/dup-remote")
	if err == nil {
		t.Fatal("CreateFromRemote() second call expected error, got nil")
	}
	if !strings.Contains(err.Error(), "worktree already exists") {
		t.Errorf("expected 'worktree already exists' in error, got: %v", err)
	}
}

func TestCreateFromRemote_NonExistentBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "real-branch")
	m := New(localDir, ".opentree")

	err := m.CreateFromRemote("no-such-branch")
	if err == nil {
		t.Fatal("CreateFromRemote() expected error for non-existent branch, got nil")
	}
}

// ---- List ----

func TestList_Empty(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)

	m := New(repoDir, ".opentree")
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

	m := New(repoDir, ".opentree")

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

	m := New(repoDir, ".opentree")

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

// ---- parseNumstat (pure, no git required) ----

func TestParseNumstat_Empty(t *testing.T) {
	files := parseNumstat("")
	if len(files) != 0 {
		t.Errorf("parseNumstat(\"\") returned %d files, want 0", len(files))
	}
}

func TestParseNumstat_SingleFile(t *testing.T) {
	files := parseNumstat("10\t3\tsrc/main.go\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].FileName != "src/main.go" {
		t.Errorf("FileName = %q, want %q", files[0].FileName, "src/main.go")
	}
	if files[0].Added != 10 {
		t.Errorf("Added = %d, want 10", files[0].Added)
	}
	if files[0].Removed != 3 {
		t.Errorf("Removed = %d, want 3", files[0].Removed)
	}
	if files[0].Uncommitted {
		t.Error("Uncommitted should default to false")
	}
}

func TestParseNumstat_MultipleFiles(t *testing.T) {
	input := "5\t2\ta.go\n0\t10\tb.go\n1\t0\tc.go\n"
	files := parseNumstat(input)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[1].FileName != "b.go" || files[1].Added != 0 || files[1].Removed != 10 {
		t.Errorf("file[1] = %+v, want b.go +0 -10", files[1])
	}
}

func TestParseNumstat_BinaryFile(t *testing.T) {
	// Binary files use "-" for added/removed counts.
	files := parseNumstat("-\t-\timage.png\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Added != 0 || files[0].Removed != 0 {
		t.Errorf("binary file should have 0/0, got +%d -%d", files[0].Added, files[0].Removed)
	}
}

func TestParseNumstat_MalformedLineSkipped(t *testing.T) {
	files := parseNumstat("not-a-valid-line\n5\t2\tvalid.go\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file (malformed skipped), got %d", len(files))
	}
	if files[0].FileName != "valid.go" {
		t.Errorf("FileName = %q, want %q", files[0].FileName, "valid.go")
	}
}

// ---- Diff integration tests (committed + uncommitted) ----

// initWorktreeRepo creates a temp git repo, creates a worktree with a committed
// file and an uncommitted file, and returns (repoDir, branchName, manager).
func initWorktreeRepo(t *testing.T) (string, string, *Manager) {
	t.Helper()
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)

	m := New(repoDir, ".opentree")

	branch := "diff-test"
	if err := m.Create(branch, "HEAD"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	wtPath := filepath.Join(repoDir, ".opentree", branch)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wtPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	// Committed file
	os.WriteFile(filepath.Join(wtPath, "done.txt"), []byte("hello\n"), 0644)
	run("git", "add", "done.txt")
	run("git", "commit", "--no-gpg-sign", "-m", "add done.txt")

	// Uncommitted file (staged but not committed)
	os.WriteFile(filepath.Join(wtPath, "wip.txt"), []byte("wip\n"), 0644)
	run("git", "add", "wip.txt")

	return repoDir, branch, m
}

func TestDiff_IncludesUncommittedChanges(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	stat, err := m.Diff(branch)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	// Should include both done.txt and wip.txt
	if !strings.Contains(stat, "done.txt") {
		t.Errorf("Diff should include done.txt\ngot: %s", stat)
	}
	if !strings.Contains(stat, "wip.txt") {
		t.Errorf("Diff should include wip.txt\ngot: %s", stat)
	}
}

func TestDiffFull_OnlyCommittedChanges(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	full, err := m.DiffFull(branch)
	if err != nil {
		t.Fatalf("DiffFull: %v", err)
	}
	// DiffFull compares merge-base to HEAD — only committed changes
	if !strings.Contains(full, "done.txt") {
		t.Errorf("DiffFull should include done.txt\ngot: %s", full)
	}
	if strings.Contains(full, "wip.txt") {
		t.Errorf("DiffFull should NOT include wip.txt\ngot: %s", full)
	}
}

func TestDiffUncommitted_OnlyUncommittedChanges(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	uncommitted, err := m.DiffUncommitted(branch)
	if err != nil {
		t.Fatalf("DiffUncommitted: %v", err)
	}
	// DiffUncommitted compares HEAD to working tree
	if !strings.Contains(uncommitted, "wip.txt") {
		t.Errorf("DiffUncommitted should include wip.txt\ngot: %s", uncommitted)
	}
	if strings.Contains(uncommitted, "done.txt") {
		t.Errorf("DiffUncommitted should NOT include done.txt\ngot: %s", uncommitted)
	}
}

func TestDiffFileStats_MarksUncommittedFiles(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	files, err := m.DiffFileStats(branch)
	if err != nil {
		t.Fatalf("DiffFileStats: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	fileMap := make(map[string]FileChange)
	for _, f := range files {
		fileMap[f.FileName] = f
	}

	committed, ok := fileMap["done.txt"]
	if !ok {
		t.Fatal("done.txt not in DiffFileStats results")
	}
	if committed.Uncommitted {
		t.Error("done.txt should NOT be marked as Uncommitted")
	}

	uncommitted, ok := fileMap["wip.txt"]
	if !ok {
		t.Fatal("wip.txt not in DiffFileStats results")
	}
	if !uncommitted.Uncommitted {
		t.Error("wip.txt should be marked as Uncommitted")
	}
}

// ---- Delete ----

func TestDelete_WithDeleteBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	m := New(repoDir, ".opentree")

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
