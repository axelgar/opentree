package github

import (
	"os/exec"
	"strings"
	"testing"
)

// isGHAvailable returns true when the gh CLI is found on PATH.
func isGHAvailable() bool {
	return exec.Command("gh", "--version").Run() == nil
}

func TestNew(t *testing.T) {
	pm := New()
	if pm == nil {
		t.Fatal("New() returned nil")
	}
}

func TestIsGHInstalled(t *testing.T) {
	pm := New()
	// Just verify it doesn't panic and returns a consistent result.
	got := pm.isGHInstalled()
	want := isGHAvailable()
	if got != want {
		t.Errorf("isGHInstalled() = %v, want %v", got, want)
	}
}

func TestGetPRStatus_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	url, err := pm.GetPRStatus("some-branch")
	if err != nil {
		t.Fatalf("GetPRStatus() expected nil error when gh not installed, got: %v", err)
	}
	if url != "" {
		t.Errorf("GetPRStatus() expected empty string when gh not installed, got %q", url)
	}
}

func TestGetFullPRStatus_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	url, state, err := pm.GetFullPRStatus("some-branch")
	if err != nil {
		t.Fatalf("GetFullPRStatus() expected nil error when gh not installed, got: %v", err)
	}
	if url != "" || state != "" {
		t.Errorf("GetFullPRStatus() = (%q, %q), want (\"\", \"\") when gh not installed", url, state)
	}
}

func TestCreatePR_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	_, err := pm.CreatePR("branch", "main", "title", "body")
	if err == nil {
		t.Fatal("CreatePR() expected error when gh not installed, got nil")
	}
	if !strings.Contains(err.Error(), "gh CLI is not installed") {
		t.Errorf("CreatePR() error = %q, expected message about gh CLI not installed", err.Error())
	}
}

// ---- IssueBranchName tests ----

func TestIssueBranchName_Basic(t *testing.T) {
	got := IssueBranchName(42, "Add dark mode")
	want := "issue-42-add-dark-mode"
	if got != want {
		t.Errorf("IssueBranchName(42, %q) = %q, want %q", "Add dark mode", got, want)
	}
}

func TestIssueBranchName_SpecialChars(t *testing.T) {
	got := IssueBranchName(7, "Fix: login bug (regression!)")
	want := "issue-7-fix-login-bug-regression"
	if got != want {
		t.Errorf("IssueBranchName = %q, want %q", got, want)
	}
}

func TestIssueBranchName_LongTitle(t *testing.T) {
	title := "This is a very long issue title that exceeds the maximum allowed length for branch names"
	got := IssueBranchName(1, title)
	// Must start with "issue-1-" and be at most 8+40 chars
	if !strings.HasPrefix(got, "issue-1-") {
		t.Errorf("IssueBranchName prefix wrong: %q", got)
	}
	slug := strings.TrimPrefix(got, "issue-1-")
	if len(slug) > 40 {
		t.Errorf("slug too long: %d chars: %q", len(slug), slug)
	}
	if strings.HasSuffix(slug, "-") {
		t.Errorf("slug has trailing dash: %q", slug)
	}
}

func TestIssueBranchName_Uppercase(t *testing.T) {
	got := IssueBranchName(10, "UPPERCASE TITLE")
	want := "issue-10-uppercase-title"
	if got != want {
		t.Errorf("IssueBranchName = %q, want %q", got, want)
	}
}

func TestIssueBranchName_EmptyTitle(t *testing.T) {
	got := IssueBranchName(5, "")
	// When title is empty, slug is also empty → just "issue-5-"
	if !strings.HasPrefix(got, "issue-5") {
		t.Errorf("IssueBranchName with empty title = %q, want prefix 'issue-5'", got)
	}
}

// ---- GetIssue when gh is not installed ----

func TestGetIssue_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	_, err := pm.GetIssue(1)
	if err == nil {
		t.Fatal("GetIssue() expected error when gh not installed, got nil")
	}
	if !strings.Contains(err.Error(), "gh CLI is not installed") {
		t.Errorf("GetIssue() error = %q, expected message about gh CLI", err.Error())
	}
}

// ---- Integration tests (require gh CLI) ----

func TestGetPRStatus_NoPRForBranch(t *testing.T) {
	if !isGHAvailable() {
		t.Skip("gh not available, skipping integration test")
	}
	pm := New()
	// A branch name unlikely to have a PR; errors are swallowed and return "".
	url, err := pm.GetPRStatus("this-branch-certainly-has-no-pr-xyz-12345")
	if err != nil {
		t.Fatalf("GetPRStatus() unexpected error: %v", err)
	}
	// No PR should yield an empty URL (gh returns non-zero exit for missing PRs).
	_ = url // empty or not depends on the repo; we just verify no panic/crash
}

func TestGetFullPRStatus_NoPRForBranch(t *testing.T) {
	if !isGHAvailable() {
		t.Skip("gh not available, skipping integration test")
	}
	pm := New()
	url, state, err := pm.GetFullPRStatus("this-branch-certainly-has-no-pr-xyz-12345")
	if err != nil {
		t.Fatalf("GetFullPRStatus() unexpected error: %v", err)
	}
	// Non-existent branch should yield empty url and state.
	if url != "" || state != "" {
		t.Logf("GetFullPRStatus() = (%q, %q) — may indicate an unexpected PR exists", url, state)
	}
}
