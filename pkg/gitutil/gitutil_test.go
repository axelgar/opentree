package gitutil

import (
	"os/exec"
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
