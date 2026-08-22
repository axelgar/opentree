package gitutil

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func isGitAvailable() bool {
	return exec.Command("git", "--version").Run() == nil
}

// initRepoWithRemote creates a bare origin and a local clone with branchNames pushed.
func initRepoWithRemote(t *testing.T, branchNames ...string) string {
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

	for _, b := range branchNames {
		runIn(localDir, "git", "checkout", "-b", b)
		runIn(localDir, "git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "commit for "+b)
		runIn(localDir, "git", "push", "origin", b)
		runIn(localDir, "git", "checkout", "main")
	}
	return localDir
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"main", "main"},
		{"feature/auth", "feature-auth"},
		{"feature/auth/login", "feature-auth-login"},
		{"fix:bug", "fix-bug"},
		{"feat/scope:thing", "feat-scope-thing"},
		{"no-change", "no-change"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := SanitizeBranchName(tt.input); got != tt.want {
			t.Errorf("SanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBranchToTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feat/improve-default-pr-title", "Improve default pr title"},
		{"fix/some-bug", "Some bug"},
		{"chore/update_deps", "Update deps"},
		{"my-feature-branch", "My feature branch"},
		{"main", "Main"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := BranchToTitle(tt.input); got != tt.want {
			t.Errorf("BranchToTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestListRemoteBranches(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/alpha", "feat/beta")
	branches, err := ListRemoteBranches(localDir, 10)
	if err != nil {
		t.Fatalf("ListRemoteBranches() error: %v", err)
	}

	found := make(map[string]bool)
	for _, b := range branches {
		found[b] = true
	}
	if !found["feat/alpha"] {
		t.Error("expected feat/alpha in results")
	}
	if !found["feat/beta"] {
		t.Error("expected feat/beta in results")
	}
}

func TestListRemoteBranches_LimitRespected(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "br-1", "br-2", "br-3", "br-4", "br-5")
	branches, err := ListRemoteBranches(localDir, 3)
	if err != nil {
		t.Fatalf("ListRemoteBranches() error: %v", err)
	}
	if len(branches) > 3 {
		t.Errorf("expected at most 3 branches, got %d", len(branches))
	}
}

func TestListRemoteBranches_NoRemote(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	// Create a plain local repo with no remote configured
	localDir := t.TempDir()
	runIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	runIn("git", "init")
	runIn("git", "config", "user.email", "test@example.com")
	runIn("git", "config", "user.name", "Test")
	runIn("git", "config", "commit.gpgsign", "false")
	runIn("git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "init")

	// No remote → no refs/remotes/origin → should return empty, no error
	branches, err := ListRemoteBranches(localDir, 10)
	if err != nil {
		t.Fatalf("ListRemoteBranches() on repo without remote: unexpected error: %v", err)
	}
	if len(branches) != 0 {
		t.Errorf("expected 0 branches for repo without remote, got %d", len(branches))
	}
}

func TestRepoRoot_InGitRepo(t *testing.T) {
	// This test runs inside the opentree repo, so it should succeed.
	root, err := RepoRoot()
	if err != nil {
		t.Skipf("not in a git repo (expected in CI): %v", err)
	}
	if root == "" {
		t.Error("RepoRoot() returned empty string")
	}
}

// Regression: RepoRoot used --show-toplevel, which inside a linked worktree
// returns the worktree's root instead of the main repo's — so opentree run
// from inside a workspace read the wrong state file and nested worktrees.
func TestRepoRoot_InsideLinkedWorktree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t)
	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
	wtDir := filepath.Join(localDir, ".opentree", "feat-x")
	runIn(localDir, "git", "worktree", "add", "-b", "feat-x", wtDir)

	t.Chdir(localDir)
	fromRoot, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() from repo root: %v", err)
	}

	t.Chdir(wtDir)
	fromWorktree, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() from inside worktree: %v", err)
	}

	if fromWorktree != fromRoot {
		t.Errorf("RepoRoot() inside worktree = %q, want main repo root %q", fromWorktree, fromRoot)
	}
}

// ---- Output ----

// Regression: everything that parsed git's output used CombinedOutput, which
// glues stderr to the front of it. A `git merge-base` that also printed a
// warning returned a sha with a sentence in front of it, and a `--numstat`
// with one on line one parsed a file short. Neither failed — they returned
// plausible nonsense.
func TestOutput_StderrDoesNotReachTheCaller(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	dir := initRepo(t)

	// An ambiguous name: a branch and a tag called the same thing. git
	// answers on stdout and warns on stderr, in one command.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "branch", "ambiguous")
	run("git", "tag", "ambiguous")

	out, err := Output(dir, "rev-parse", "ambiguous")
	if err != nil {
		t.Fatalf("Output(): %v", err)
	}
	got := strings.TrimSpace(string(out))
	if strings.Contains(strings.ToLower(got), "warning") {
		t.Errorf("Output() = %q — git's warning came back as part of the value", got)
	}
	if len(got) != 40 {
		t.Errorf("Output() = %q, want a bare 40-character sha", got)
	}
}

// A failure keeps what git said about it: the caller printing the error is the
// one who wants stderr, and it is the only place it now appears.
func TestOutput_FailureCarriesWhatGitSaid(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	dir := initRepo(t)

	out, err := Output(dir, "rev-parse", "--verify", "refs/heads/no-such-branch")
	if err == nil {
		t.Fatalf("Output() succeeded on a branch that does not exist, returning %q", out)
	}
	if out != nil {
		t.Errorf("Output() returned %q alongside an error, want nil", out)
	}
	if !strings.Contains(err.Error(), "rev-parse") {
		t.Errorf("error = %v, want it to name the command that failed", err)
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("error = %v, want git's own message in it", err)
	}
}

// initRepo is a git repository with one commit, for the cases that need no remote.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "--no-gpg-sign", "-q", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}
