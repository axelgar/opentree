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
	return initGitRepoAt(t, t.TempDir())
}

// initGitRepoAt is initGitRepo in a directory of the caller's choosing, for
// a test that needs two repositories with one name.
func initGitRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

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
	// Ensure the default branch is "main" regardless of git's init.defaultBranch setting.
	run("git", "branch", "-m", "main")

	return dir
}

// ---- parseWorktrees (pure, no git required) ----

func TestParseWorktrees_Empty(t *testing.T) {
	m := &Manager{repoRoot: "/repo", base: "/repo/.opentree"}
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
	m := &Manager{repoRoot: "/repo", base: "/repo/.opentree"}
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
	m := &Manager{repoRoot: "/repo", base: "/repo/.opentree"}
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
	m := &Manager{repoRoot: "/repo", base: "/repo/.opentree"}
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
	m := &Manager{repoRoot: "/repo", base: "/repo/.opentree"}
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
	m := &Manager{repoRoot: "/repo", base: "/repo/.opentree"}
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
	// Drop the local branch the helper left behind so this exercises the
	// real remote-only path (worktree add --track -b).
	delCmd := exec.Command("git", "branch", "-D", "feat/remote-thing")
	delCmd.Dir = localDir
	if out, err := delCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to delete local branch: %v\n%s", err, out)
	}
	m := New(localDir, ".opentree")

	createdBranch, err := m.CreateFromRemote("feat/remote-thing")
	if err != nil {
		t.Fatalf("CreateFromRemote() failed: %v", err)
	}
	if !createdBranch {
		t.Error("CreateFromRemote() should report a newly created local branch")
	}

	expectedDir := filepath.Join(localDir, ".opentree", "feat-remote-thing")
	if _, err := os.Stat(expectedDir); err != nil {
		t.Errorf("worktree directory not created at %q: %v", expectedDir, err)
	}
}

// Regression: when the local branch already exists, CreateFromRemote checks
// it out instead of creating it — and must report createdBranch=false so
// cleanup paths don't `branch -D` a branch holding the user's local commits.
func TestCreateFromRemote_PreexistingLocalBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	// initRepoWithRemote leaves the local branch in the clone, which is
	// exactly the pre-existing-branch case.
	localDir := initRepoWithRemote(t, "feat/existing")
	m := New(localDir, ".opentree")
	createdBranch, err := m.CreateFromRemote("feat/existing")
	if err != nil {
		t.Fatalf("CreateFromRemote() failed: %v", err)
	}
	if createdBranch {
		t.Error("CreateFromRemote() must report createdBranch=false for a pre-existing local branch")
	}
}

func TestCreateFromRemote_AlreadyExists(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/dup-remote")
	m := New(localDir, ".opentree")

	if _, err := m.CreateFromRemote("feat/dup-remote"); err != nil {
		t.Fatalf("CreateFromRemote() first call failed: %v", err)
	}
	_, err := m.CreateFromRemote("feat/dup-remote")
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

	_, err := m.CreateFromRemote("no-such-branch")
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

func TestDiffCombined_SectionsAndContent(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	out, err := m.DiffCombined(branch)
	if err != nil {
		t.Fatalf("DiffCombined: %v", err)
	}
	// Both committed and uncommitted changes exist, so both files and both
	// section headers must appear.
	for _, want := range []string{
		"done.txt",
		"wip.txt",
		"══════ Committed Changes ══════",
		"══════ Uncommitted Changes ══════",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DiffCombined output missing %q:\n%s", want, out)
		}
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

func TestPush(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/seed")
	m := New(localDir, ".opentree")

	// A brand-new branch that origin doesn't know about yet.
	if err := m.Create("feat/push-me", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Push("feat/push-me"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	cmd := exec.Command("git", "ls-remote", "--heads", "origin", "feat/push-me")
	cmd.Dir = localDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls-remote: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("branch not found on origin after Push()")
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

// Regression: once git loses a worktree's registration (a partial/orphaned
// removal) while the directory still lingers on disk, `git worktree remove`
// fails with "is not a working tree" and Delete used to abort — leaving the
// branch and state entry uncleanable. Delete must instead remove the leftover
// directory and branch.
func TestDelete_OrphanedWorktreeDirectory(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)

	m := New(repoDir, ".opentree")

	// A branch that exists locally, mirroring a squash-merged PR branch that
	// opentree deletes with -D.
	if out, err := exec.Command("git", "-C", repoDir, "branch", "feat/orphaned", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git branch failed: %v\n%s", err, out)
	}

	// The worktree directory lingers on disk with leftover build artifacts but
	// is NOT a registered git worktree (no .git pointer file), so
	// `git worktree remove` would fail here.
	worktreePath := filepath.Join(repoDir, ".opentree", "feat-orphaned")
	if err := os.MkdirAll(filepath.Join(worktreePath, ".next"), 0755); err != nil {
		t.Fatalf("failed to create leftover worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".opentree-status.json"), []byte(`{"status":"needs_input"}`), 0644); err != nil {
		t.Fatalf("failed to write leftover status file: %v", err)
	}

	if err := m.Delete("feat/orphaned", true); err != nil {
		t.Fatalf("Delete() on orphaned worktree failed: %v", err)
	}

	// Leftover directory must be gone.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("leftover worktree directory still exists after Delete()")
	}

	// Branch must be gone (force-deleted).
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", "feat/orphaned").Output()
	if strings.Contains(string(out), "feat/orphaned") {
		t.Errorf("branch feat/orphaned should be deleted after Delete(deleteBranch=true)")
	}
}

// ---- DiffStats ----

func TestDiffStats_NoChanges(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	// Create a worktree with no changes vs base
	branch := "diffstats-no-changes"
	if err := m.Create(branch, "HEAD"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stat, files, err := m.DiffStats(branch)
	if err != nil {
		t.Fatalf("DiffStats() error: %v", err)
	}

	// Expect empty stat and no file changes
	if strings.TrimSpace(stat) != "" {
		t.Errorf("DiffStats stat = %q, want empty", stat)
	}
	if len(files) != 0 {
		t.Errorf("DiffStats files = %d, want 0", len(files))
	}
}

func TestDiffStats_WithCommittedChange(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	stat, files, err := m.DiffStats(branch)
	if err != nil {
		t.Fatalf("DiffStats() error: %v", err)
	}

	// Should report at least 1 file (done.txt committed)
	if len(files) < 1 {
		t.Fatalf("DiffStats expected at least 1 file, got %d", len(files))
	}

	// Stat string should be non-empty
	if strings.TrimSpace(stat) == "" {
		t.Errorf("DiffStats stat should be non-empty when changes exist")
	}

	// done.txt should be present and not marked uncommitted
	fileMap := make(map[string]FileChange)
	for _, f := range files {
		fileMap[f.FileName] = f
	}

	if _, ok := fileMap["done.txt"]; !ok {
		t.Error("DiffStats should include done.txt (committed change)")
	}
	if f, ok := fileMap["done.txt"]; ok && f.Uncommitted {
		t.Error("done.txt should NOT be marked Uncommitted")
	}
}

func TestDiffStats_MarksUncommittedFiles(t *testing.T) {
	_, branch, m := initWorktreeRepo(t)

	_, files, err := m.DiffStats(branch)
	if err != nil {
		t.Fatalf("DiffStats() error: %v", err)
	}

	fileMap := make(map[string]FileChange)
	for _, f := range files {
		fileMap[f.FileName] = f
	}

	// wip.txt is staged but not committed — should be marked Uncommitted
	if wip, ok := fileMap["wip.txt"]; !ok {
		t.Error("DiffStats should include wip.txt (staged but uncommitted)")
	} else if !wip.Uncommitted {
		t.Error("wip.txt should be marked as Uncommitted")
	}
}

// Regression: numstat rename lines ("old => new", "dir/{old => new}") used to
// be stored verbatim as the file name, so renamed files displayed wrong and
// never matched the uncommitted-files set.
func TestNumstatFileName_Renames(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain.txt", "plain.txt"},
		{"a.txt => b.txt", "b.txt"},
		{"dir/{old.txt => new.txt}", "dir/new.txt"},
		{"pkg/{a => b}/file.go", "pkg/b/file.go"},
		{"{ => sub}/file.go", "sub/file.go"},
		{"{sub => }/file.go", "file.go"},
	}
	for _, tt := range tests {
		if got := numstatFileName(tt.in); got != tt.want {
			t.Errorf("numstatFileName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Regression: names that sanitize to opentree's own state files could brick
// the repo (a worktree at .opentree/state.json fails every later load).
func TestCreate_ReservedNamesRejected(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initGitRepo(t)
	m := New(localDir, ".opentree")

	for _, name := range []string{"state.json", "state.lock", "state:json"} {
		if err := m.Create(name, "main"); err == nil {
			t.Errorf("Create(%q) should be rejected", name)
		}
	}
}

// ---- Delete guards ----

// Regression: `opentree delete state.json` used to reach os.RemoveAll on the
// registry itself. Nothing registers state.json as a worktree, so Delete took
// the unregistered arm, removed the file, and only then failed on the branch —
// reporting an error having already destroyed the thing that tracks every
// workspace. Create rejected the same name; Delete did not.
func TestDelete_ReservedNamesRejected(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	opentreeDir := filepath.Join(repoDir, ".opentree")
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	statePath := filepath.Join(opentreeDir, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"workspaces":{}}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, name := range []string{"state.json", "state.lock", "state.json.tmp"} {
		if err := m.Delete(name, true); err == nil {
			t.Errorf("Delete(%q) should be refused", name)
		}
	}

	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("state.json was removed by a refused Delete: %v", err)
	}
}

// A name that climbs out of the base directory resolves to the base directory's
// own parent — filepath.Join(root, ".opentree", "..") is the repository — and
// the unregistered arm would hand that to os.RemoveAll.
func TestDelete_EscapingNamesRejected(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	canary := filepath.Join(repoDir, "keep-me")
	if err := os.WriteFile(canary, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// "sub/../.." is deliberately absent: SanitizeBranchName turns every "/"
	// into "-", so it arrives as the ordinary directory name "sub-..-..". The
	// names that still climb are the ones with no separator to flatten.
	for _, name := range []string{"..", ".", ""} {
		if err := m.Delete(name, false); err == nil {
			t.Errorf("Delete(%q) should be refused", name)
		}
	}

	if _, err := os.Stat(canary); err != nil {
		t.Errorf("repository contents were removed by a refused Delete: %v", err)
	}
}

// The unregistered arm removes a leftover directory. Anything else under the
// base directory is one of opentree's own files, or somebody else's, and git
// would never have put it there — so it is refused rather than removed.
func TestDelete_UnregisteredNonDirectoryRefused(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	opentreeDir := filepath.Join(repoDir, ".opentree")
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stray := filepath.Join(opentreeDir, "notes")
	if err := os.WriteFile(stray, []byte("not a worktree"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := m.Delete("notes", false); err == nil {
		t.Error("Delete() on a plain file should be refused")
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("the file was removed anyway: %v", err)
	}
}

// Regression: two branch names can sanitize to the same directory ("feat/x" and
// "feat-x" both give feat-x), so Delete refuses when the worktree there holds a
// different branch than the one asked for. The guard had no coverage.
func TestDelete_WrongBranchAtPathRefused(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	// "feat-x" sanitizes to the same directory, where "feat/x" is checked out.
	err := m.Delete("feat-x", true)
	if err == nil {
		t.Fatal("Delete(\"feat-x\") should be refused — feat/x is checked out there")
	}
	if !strings.Contains(err.Error(), "refusing to delete") {
		t.Errorf("error = %v, want it to say why it refused", err)
	}

	// Neither the worktree nor the branch may have been touched.
	if _, statErr := os.Stat(filepath.Join(repoDir, ".opentree", "feat-x")); statErr != nil {
		t.Errorf("worktree was removed by a refused Delete: %v", statErr)
	}
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", "feat/x").Output()
	if !strings.Contains(string(out), "feat/x") {
		t.Error("branch feat/x was deleted by a refused Delete")
	}
}

// Regression: a workspace whose branch has already gone — deleted by hand, or
// squash-merged and pruned — used to fail the whole delete at `git branch -D`,
// after the worktree was already removed. The caller reads that as a failure,
// so the tmux window and the state entry survive: an undeletable row whose dev
// server still holds its port.
func TestDelete_MissingBranchStillSucceeds(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	if err := m.Create("feat/gone", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	// Unregister the worktree, then remove the branch behind opentree's back.
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "remove", "--force",
		filepath.Join(repoDir, ".opentree", "feat-gone")).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "branch", "-D", "feat/gone").CombinedOutput(); err != nil {
		t.Fatalf("git branch -D: %v\n%s", err, out)
	}

	if err := m.Delete("feat/gone", true); err != nil {
		t.Fatalf("Delete() with an already-deleted branch should succeed, got %v", err)
	}
}

// ---- base directory exclusion ----

// readExclude returns the contents of a repository's .git/info/exclude, or ""
// when git has not written one yet.
func readExclude(t *testing.T, repoDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoDir, ".git", "info", "exclude"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read exclude: %v", err)
	}
	return string(data)
}

// Regression: the base directory sits inside the repository and nothing taught
// git to ignore it, so the first workspace made the repository dirty. `git add
// -A` warned "adding embedded git repository" and staged a gitlink pointing at
// a commit that existed only on the machine that made it.
func TestCreate_ExcludesTheBaseDirectory(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	if got := readExclude(t, repoDir); !strings.Contains(got, "/.opentree/") {
		t.Errorf(".git/info/exclude does not exclude the base directory:\n%s", got)
	}

	// The rule is only worth anything if git agrees it bites, and the whole
	// point is that `git add -A` leaves the worktree alone.
	if out, err := exec.Command("git", "-C", repoDir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add -A: %v\n%s", err, out)
	}
	staged, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, staged)
	}
	if strings.Contains(string(staged), ".opentree") {
		t.Errorf("the worktree was staged by `git add -A`:\n%s", staged)
	}
}

// The exclude file belongs to the user; opentree may add its rule once and
// never again, however many workspaces are made.
func TestCreate_ExcludeRuleIsWrittenOnce(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	if err := m.Create("feat/one", "main"); err != nil {
		t.Fatalf("Create(feat/one): %v", err)
	}
	first := readExclude(t, repoDir)
	if err := m.Create("feat/two", "main"); err != nil {
		t.Fatalf("Create(feat/two): %v", err)
	}
	second := readExclude(t, repoDir)

	if second != first {
		t.Errorf("a second Create rewrote .git/info/exclude:\nbefore:\n%s\nafter:\n%s", first, second)
	}
	if n := strings.Count(second, "/.opentree/"); n != 1 {
		t.Errorf("the exclude rule appears %d times, want 1:\n%s", n, second)
	}
}

// A project that already ignores the base directory — this one has carried
// `.opentree/` in its .gitignore by hand since before opentree wrote any rule
// of its own — gets nothing appended anywhere.
func TestCreate_AlreadyIgnoredBaseDirectoryIsLeftAlone(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte(".opentree/\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	before := readExclude(t, repoDir)

	m := New(repoDir, ".opentree")
	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	if after := readExclude(t, repoDir); after != before {
		t.Errorf(".git/info/exclude was touched although .gitignore already covers the base directory:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A ../worktrees layout puts the worktrees outside the repository altogether.
// Git will never look there, so a rule for it would be a line in the user's
// exclude file that means nothing. The state directory's rule still goes in:
// that one is inside the working tree whatever base_dir says.
func TestCreate_BaseDirectoryOutsideTheRepositoryIsNotExcluded(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)

	m := New(repoDir, filepath.Join("..", "worktrees"))
	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "..", "worktrees", "feat-x")); err != nil {
		t.Fatalf("worktree not created outside the repository: %v", err)
	}

	after := readExclude(t, repoDir)
	if strings.Contains(after, "worktrees") {
		t.Errorf("exclude file names an out-of-repository base directory:\n%s", after)
	}
	if n := strings.Count(after, "/.opentree/"); n != 1 {
		t.Errorf("the state directory's rule appears %d times, want 1:\n%s", n, after)
	}
}

// The exclude is a courtesy, not a precondition: whatever is wrong with the
// repository's own files, the worktree the user asked for still gets made.
// `.git/info` is replaced with a plain file here, which is the cheapest way to
// make every path to the exclude fail — read, mkdir and open alike — without
// depending on file permissions, which do nothing when the tests run as root.
func TestCreate_SucceedsWhenTheExcludeCannotBeWritten(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	infoDir := filepath.Join(repoDir, ".git", "info")
	if err := os.RemoveAll(infoDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.WriteFile(infoDir, []byte("not a directory\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := New(repoDir, ".opentree")
	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create() should not fail because the exclude could not be written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".opentree", "feat-x")); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
}

// Outside a git repository there is no exclude file to find, and the manager
// must not invent one: creating .git/info/exclude in a directory that is not a
// repository would leave a .git behind that git itself would then choke on.
func TestExcludeBaseDir_OutsideARepositoryWritesNothing(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, ".opentree")

	if entry, ok := m.excludeEntry(); ok {
		m.exclude(entry, "worktrees")
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Errorf("a .git directory was created outside a repository (err = %v)", err)
	}
}

// Regression: base_dir says where a worktree goes, not where it is. A workspace
// created under one setting and deleted under another — the config edited, or a
// repository-supplied value that is no longer honoured — computed a path with
// nothing at it. The delete removed nothing, then failed on a branch git still
// considered checked out, leaving a row that could not be deleted at all.
func TestDelete_FindsAWorktreeThatMovedOutFromUnderTheConfig(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)

	// Created under one base dir...
	made := New(repoDir, ".opentree")
	if err := made.Create("feat/moved", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	worktreePath := filepath.Join(repoDir, ".opentree", "feat-moved")

	// ...and deleted under another, which is what an upgrade that stops
	// honouring a repository's base_dir looks like from here.
	if err := New(repoDir, ".elsewhere").Delete("feat/moved", true); err != nil {
		t.Fatalf("Delete() after the base dir changed: %v", err)
	}

	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("the worktree survived a delete that should have found it")
	}
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", "feat/moved").Output()
	if strings.Contains(string(out), "feat/moved") {
		t.Error("the branch survived, so the row would still be undeletable")
	}
}

// The fallback asks git, so it is still the branch that decides. A name with no
// worktree anywhere must not be answered with somebody else's.
func TestDelete_TheFallbackOnlyMatchesTheBranchAsked(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")

	if err := m.Create("feat/kept", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	// A workspace whose worktree never existed. It has nothing to remove, and
	// must not adopt the one that does.
	if out, err := exec.Command("git", "-C", repoDir, "branch", "feat/absent", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	if err := m.Delete("feat/absent", true); err != nil {
		t.Fatalf("Delete() of a workspace with no worktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoDir, ".opentree", "feat-kept")); err != nil {
		t.Errorf("deleting one workspace removed another's worktree: %v", err)
	}
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", "feat/kept").Output()
	if !strings.Contains(string(out), "feat/kept") {
		t.Error("deleting one workspace removed another's branch")
	}
}

// ---- where worktrees go ----

func TestBaseDir_ResolvesEachSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct{ configured, want string }{
		{"", filepath.Join(home, ".opentree", "worktrees", "myapp")},
		{".opentree", "/src/myapp/.opentree"},
		{"build/worktrees", "/src/myapp/build/worktrees"},
		{"~/wt", filepath.Join(home, "wt")},
		{"/elsewhere/wt", "/elsewhere/wt"},
	}
	for _, c := range cases {
		if got := BaseDir("/src/myapp", c.configured); got != c.want {
			t.Errorf("BaseDir(%q) = %q, want %q", c.configured, got, c.want)
		}
	}
}

// The default layout: nothing configured, and the worktree lands under the
// user's own opentree directory rather than inside the working tree, where
// every tool that walks the project would meet it.
func TestCreate_DefaultBaseLeavesTheWorkingTree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := initGitRepo(t)
	m := New(repoDir, "")

	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	want := filepath.Join(home, ".opentree", "worktrees", filepath.Base(repoDir), "feat-x")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("no worktree at %s: %v", want, err)
	}
	if got := m.Path("feat/x"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if owner, ok := readMarker(m.Base()); !ok || owner != m.repoRoot {
		t.Errorf("marker = %q, %v; want the repository %q", owner, ok, m.repoRoot)
	}

	// The working tree has nothing new in it — not a worktree, not a marker.
	status, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, status)
	}
	if strings.TrimSpace(string(status)) != "" {
		t.Errorf("the working tree changed:\n%s", status)
	}
}

// Two clones with one directory name must not share a base directory: the
// second would see the first's worktrees as its own, and delete them.
func TestCreate_ASecondRepositoryWithTheSameNameGetsItsOwnBase(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	t.Setenv("HOME", t.TempDir())
	repoA := initGitRepoAt(t, filepath.Join(t.TempDir(), "app"))
	repoB := initGitRepoAt(t, filepath.Join(t.TempDir(), "app"))

	a := New(repoA, "")
	if err := a.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create() in the first repository: %v", err)
	}

	b := New(repoB, "")
	if b.Base() == a.Base() {
		t.Fatalf("both repositories resolved to %s", a.Base())
	}
	if !strings.HasPrefix(filepath.Base(b.Base()), "app-") {
		t.Errorf("the second repository's base is %q, want app-<hash>", b.Base())
	}
	if err := b.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create() in the second repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.Base(), "feat-x")); err != nil {
		t.Errorf("the second repository's worktree is not under its own base: %v", err)
	}
	// And the first is untouched by all of it.
	if got := a.Path("feat/x"); !strings.HasPrefix(got, a.Base()) {
		t.Errorf("the first repository's worktree moved to %q", got)
	}
}

// A workspace made under one base_dir and looked up under another is still
// where it was made — which, since the default left the working tree, is
// every workspace made before it did.
func TestPath_FindsAWorktreeMadeUnderAnotherBase(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	t.Setenv("HOME", t.TempDir())
	repoDir := initGitRepo(t)
	legacy := New(repoDir, ".opentree")
	if err := legacy.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	made := legacy.Path("feat/x")

	now := New(repoDir, "")
	if got := now.Path("feat/x"); got != made {
		t.Errorf("Path() = %q, want where it was made, %q", got, made)
	}
	if _, err := now.Diff("feat/x", "main"); err != nil {
		t.Errorf("Diff() through the old location: %v", err)
	}
	wts, err := now.List()
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(wts) != 1 || wts[0].Branch != "feat/x" {
		t.Errorf("List() = %+v, want the worktree under the old layout", wts)
	}
	// Somewhere nothing was ever made is where a new one would go.
	if got := now.Path("feat/new"); !strings.HasPrefix(got, now.Base()) {
		t.Errorf("Path() for a new branch = %q, want it under %s", got, now.Base())
	}
}

// state.json still lives inside the working tree whatever base_dir says, and
// git has to be told to ignore it even when no worktree is going there.
func TestCreate_ExcludesTheStateDirectoryWhateverTheBase(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := initGitRepo(t)
	m := New(repoDir, "")
	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	got := readExclude(t, repoDir)
	if !strings.Contains(got, "/.opentree/") {
		t.Errorf(".git/info/exclude does not exclude the state directory:\n%s", got)
	}
	if strings.Contains(got, home) {
		t.Errorf(".git/info/exclude names a directory outside the repository:\n%s", got)
	}
}

// ---- fetching the base first ----

// advanceOrigin moves origin's main past what the clone at localDir has, from
// a second clone, the way a colleague's merge does.
func advanceOrigin(t *testing.T, localDir string) string {
	t.Helper()
	remote, err := exec.Command("git", "-C", localDir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("remote get-url: %v", err)
	}
	other := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = other
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	run("git", "clone", "--quiet", "--branch", "main", strings.TrimSpace(string(remote)), ".")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "merged upstream")
	run("git", "push", "--quiet", "origin", "HEAD:main")
	sha, err := exec.Command("git", "-C", other, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(sha))
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	sha, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(sha))
}

// The local main is whatever was last pulled. A workspace branched from it
// starts behind origin, and its PR carries or conflicts with what merged in
// the meantime — so the base is fetched first, and the branch made from
// origin's copy.
func TestCreate_FetchesTheBaseFirst(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initRepoWithRemote(t, "feat/other")
	upstream := advanceOrigin(t, localDir)
	if headOf(t, localDir) == upstream {
		t.Fatal("the clone already has origin's commit; nothing to fetch")
	}

	m := New(localDir, ".opentree")
	start, err := m.CreateFrom("feat/x", "main", true)
	if err != nil {
		t.Fatalf("CreateFrom(): %v", err)
	}
	if start.Ref != "origin/main" || start.Offline != "" {
		t.Errorf("start = %+v, want origin/main with nothing offline", start)
	}
	if got := headOf(t, m.Path("feat/x")); got != upstream {
		t.Errorf("the worktree starts at %s, want origin's %s", got, upstream)
	}
	if start.Note() != "fetched origin/main first" {
		t.Errorf("Note() = %q", start.Note())
	}

	// And it is a branch of its own, not one tracking origin/main: git status
	// must not call a feature branch "up to date with origin/main".
	if out, _ := exec.Command("git", "-C", localDir, "config", "branch.feat/x.merge").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("feat/x tracks %s; a new branch must not track its base", out)
	}
}

func TestCreate_NoFetchBranchesFromTheLocalBase(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initRepoWithRemote(t, "feat/other")
	advanceOrigin(t, localDir)
	local := headOf(t, localDir)

	m := New(localDir, ".opentree")
	start, err := m.CreateFrom("feat/x", "main", false)
	if err != nil {
		t.Fatalf("CreateFrom(): %v", err)
	}
	if start.Ref != "main" || start.Note() != "" {
		t.Errorf("start = %+v, want the local main and nothing to say", start)
	}
	if got := headOf(t, m.Path("feat/x")); got != local {
		t.Errorf("the worktree starts at %s, want the local %s", got, local)
	}
}

// Offline is not a reason to refuse the workspace: the branch is made from
// the local base, and the Start says so, for the command to repeat.
func TestCreate_OfflineBranchesFromTheLocalBaseAndSaysSo(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initRepoWithRemote(t, "feat/other")
	if out, err := exec.Command("git", "-C", localDir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone")).CombinedOutput(); err != nil {
		t.Fatalf("set-url: %v\n%s", err, out)
	}
	local := headOf(t, localDir)

	m := New(localDir, ".opentree")
	start, err := m.CreateFrom("feat/x", "main", true)
	if err != nil {
		t.Fatalf("CreateFrom(): %v", err)
	}
	if start.Ref != "main" || start.Offline == "" {
		t.Errorf("start = %+v, want the local main with the fetch's failure", start)
	}
	if !strings.Contains(start.Note(), "could not fetch origin") || !strings.Contains(start.Note(), "local main") {
		t.Errorf("Note() = %q", start.Note())
	}
	if got := headOf(t, m.Path("feat/x")); got != local {
		t.Errorf("the worktree starts at %s, want the local %s", got, local)
	}
}

// No origin, nothing to ask: no note either, since nothing was tried.
func TestCreate_WithoutAnOriginSkipsTheFetch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	m := New(repoDir, ".opentree")
	start, err := m.CreateFrom("feat/x", "main", true)
	if err != nil {
		t.Fatalf("CreateFrom(): %v", err)
	}
	if start.Ref != "main" || start.Offline != "" || start.Note() != "" {
		t.Errorf("start = %+v, want the local main and silence", start)
	}
}

// A base that is not a branch — HEAD, a sha, a tag — is what it is wherever
// it is read, and is not fetched.
func TestCreate_ABaseThatIsNotABranchIsNotFetched(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initRepoWithRemote(t, "feat/other")
	advanceOrigin(t, localDir)
	local := headOf(t, localDir)

	m := New(localDir, ".opentree")
	start, err := m.CreateFrom("feat/x", "HEAD", true)
	if err != nil {
		t.Fatalf("CreateFrom(): %v", err)
	}
	if start.Ref != "HEAD" || start.Note() != "" {
		t.Errorf("start = %+v, want HEAD as given", start)
	}
	if got := headOf(t, m.Path("feat/x")); got != local {
		t.Errorf("the worktree starts at %s, want the local HEAD %s", got, local)
	}
}

// ---- bringing the base in ----

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "--no-gpg-sign", "-q", "-m", message}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
}

func TestSync_MergesOriginsBaseIntoTheBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initRepoWithRemote(t, "feat/other")
	m := New(localDir, ".opentree")
	if _, err := m.CreateFrom("feat/x", "main", false); err != nil {
		t.Fatalf("CreateFrom(): %v", err)
	}
	upstream := advanceOrigin(t, localDir)

	res, err := m.Sync("feat/x", "main", true)
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if res.Ref != "origin/main" || !res.Updated || len(res.Conflicts) != 0 {
		t.Errorf("res = %+v, want origin/main merged with no conflicts", res)
	}
	if err := exec.Command("git", "-C", m.Path("feat/x"), "merge-base", "--is-ancestor", upstream, "HEAD").Run(); err != nil {
		t.Error("origin's commit is not in the branch after the sync")
	}

	again, err := m.Sync("feat/x", "main", true)
	if err != nil {
		t.Fatalf("second Sync(): %v", err)
	}
	if again.Updated {
		t.Error("a second sync claims to have moved the branch")
	}
}

// Conflicts are the ordinary outcome, not an error: listed, with the merge
// left in progress for whoever resolves it.
func TestSync_ReportsConflictsAndLeavesTheMergeInProgress(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	commitFile(t, repoDir, "greeting.txt", "hello\n", "greeting")
	m := New(repoDir, ".opentree")
	if err := m.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	commitFile(t, m.Path("feat/x"), "greeting.txt", "hello from the branch\n", "branch side")
	commitFile(t, repoDir, "greeting.txt", "hello from main\n", "main side")

	res, err := m.Sync("feat/x", "main", false)
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != "greeting.txt" {
		t.Fatalf("Conflicts = %v, want [greeting.txt]", res.Conflicts)
	}
	if err := exec.Command("git", "-C", m.Path("feat/x"), "rev-parse", "-q", "--verify", "MERGE_HEAD").Run(); err != nil {
		t.Error("the merge was not left in progress")
	}
	data, _ := os.ReadFile(filepath.Join(m.Path("feat/x"), "greeting.txt"))
	if !strings.Contains(string(data), "<<<<<<<") {
		t.Errorf("no conflict markers in the file:\n%s", data)
	}
}

func TestSync_RefusesAWorktreeThatIsNotThere(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	m := New(initGitRepo(t), ".opentree")
	if _, err := m.Sync("feat/nope", "main", false); err == nil {
		t.Error("Sync() merged into a worktree that does not exist")
	}
}
