package gitutil

import "testing"

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
