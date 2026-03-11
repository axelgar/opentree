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
	got := pm.IsInstalled()
	want := isGHAvailable()
	if got != want {
		t.Errorf("IsInstalled() = %v, want %v", got, want)
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

// ---- GetBranchAndPRStatus tests ----

func TestGetBranchAndPRStatus_LSRemoteError(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	initCmd := exec.Command("git", "init")
	initCmd.Dir = dir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	pm := New()
	// wasPushed=true: without the fix this would incorrectly set RemoteDeleted=true
	status, err := pm.GetBranchAndPRStatus("main", dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.RemoteCheckFailed {
		t.Error("expected RemoteCheckFailed=true when git ls-remote fails")
	}
	if status.RemoteDeleted {
		t.Error("expected RemoteDeleted=false when remote check failed")
	}
	if status.Pushed {
		t.Error("expected Pushed=false when remote check failed")
	}
}

// ---- parsePRURL tests ----

func TestParsePRURL_Valid(t *testing.T) {
	tests := []struct {
		url        string
		wantOwner  string
		wantRepo   string
		wantNumber int
	}{
		{
			url:        "https://github.com/acme/myrepo/pull/42",
			wantOwner:  "acme",
			wantRepo:   "myrepo",
			wantNumber: 42,
		},
		{
			url:        "https://github.com/org-name/repo.with.dots/pull/1",
			wantOwner:  "org-name",
			wantRepo:   "repo.with.dots",
			wantNumber: 1,
		},
		{
			// URL with trailing path (e.g. #issuecomment anchor)
			url:        "https://github.com/owner/repo/pull/999/files",
			wantOwner:  "owner",
			wantRepo:   "repo",
			wantNumber: 999,
		},
	}
	for _, tt := range tests {
		owner, repo, number, err := parsePRURL(tt.url)
		if err != nil {
			t.Errorf("parsePRURL(%q) unexpected error: %v", tt.url, err)
			continue
		}
		if owner != tt.wantOwner {
			t.Errorf("parsePRURL(%q) owner = %q, want %q", tt.url, owner, tt.wantOwner)
		}
		if repo != tt.wantRepo {
			t.Errorf("parsePRURL(%q) repo = %q, want %q", tt.url, repo, tt.wantRepo)
		}
		if number != tt.wantNumber {
			t.Errorf("parsePRURL(%q) number = %d, want %d", tt.url, number, tt.wantNumber)
		}
	}
}

func TestParsePRURL_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"not-a-url",
		"https://github.com/owner/repo/issues/42",
		"https://gitlab.com/owner/repo/merge_requests/42",
		"https://github.com/owner/pull/42", // missing repo segment
	}
	for _, url := range invalid {
		_, _, _, err := parsePRURL(url)
		if err == nil {
			t.Errorf("parsePRURL(%q) expected error, got nil", url)
		}
	}
}

// ---- FormatReviewsPrompt tests ----

func TestFormatReviewsPrompt_Empty(t *testing.T) {
	got := FormatReviewsPrompt(nil)
	if got != "" {
		t.Errorf("FormatReviewsPrompt(nil) = %q, want empty string", got)
	}
	got = FormatReviewsPrompt([]ReviewComment{})
	if got != "" {
		t.Errorf("FormatReviewsPrompt([]) = %q, want empty string", got)
	}
}

func TestFormatReviewsPrompt_SingleGeneralReview(t *testing.T) {
	comments := []ReviewComment{
		{Author: "alice", Body: "Please add error handling.", State: "CHANGES_REQUESTED"},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "@alice") {
		t.Errorf("prompt missing author: %s", got)
	}
	if !strings.Contains(got, "Please add error handling.") {
		t.Errorf("prompt missing body: %s", got)
	}
	if !strings.Contains(got, "CHANGES_REQUESTED") {
		t.Errorf("prompt missing state: %s", got)
	}
	if !strings.Contains(got, "Please address all of these review comments.") {
		t.Errorf("prompt missing closing instruction: %s", got)
	}
	if !strings.Contains(got, "1 PR review comment(s)") {
		t.Errorf("prompt missing count: %s", got)
	}
}

func TestFormatReviewsPrompt_InlineComment(t *testing.T) {
	comments := []ReviewComment{
		{Author: "bob", Body: "This is too complex.", State: "COMMENTED", Path: "pkg/foo/bar.go", Line: 42},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "pkg/foo/bar.go:42") {
		t.Errorf("prompt missing file:line reference: %s", got)
	}
	if !strings.Contains(got, "@bob") {
		t.Errorf("prompt missing author: %s", got)
	}
	if strings.Contains(got, "COMMENTED") {
		t.Errorf("prompt should not show COMMENTED state, got: %s", got)
	}
}

func TestFormatReviewsPrompt_InlineComment_NoLine(t *testing.T) {
	comments := []ReviewComment{
		{Author: "carol", Body: "Rename this function.", State: "COMMENTED", Path: "main.go", Line: 0},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "main.go") {
		t.Errorf("prompt missing path: %s", got)
	}
	// Should show path but no ":0"
	if strings.Contains(got, "main.go:0") {
		t.Errorf("prompt should not show ':0' for zero line: %s", got)
	}
}

func TestFormatReviewsPrompt_Multiple(t *testing.T) {
	comments := []ReviewComment{
		{Author: "alice", Body: "Fix typo.", State: "CHANGES_REQUESTED"},
		{Author: "bob", Body: "Extract method.", State: "COMMENTED", Path: "pkg/x.go", Line: 10},
		{Author: "carol", Body: "Add tests.", State: "CHANGES_REQUESTED"},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "3 PR review comment(s)") {
		t.Errorf("prompt missing count: %s", got)
	}
	for _, wantBody := range []string{"Fix typo.", "Extract method.", "Add tests."} {
		if !strings.Contains(got, wantBody) {
			t.Errorf("prompt missing body %q: %s", wantBody, got)
		}
	}
}

// ---- FetchPRReviews when gh is not installed ----

func TestFetchPRReviews_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	comments, err := pm.FetchPRReviews("some-branch")
	if err == nil {
		t.Fatal("FetchPRReviews() expected error when gh not installed, got nil")
	}
	if !strings.Contains(err.Error(), "gh CLI is not installed") {
		t.Errorf("FetchPRReviews() error = %q, want 'gh CLI is not installed'", err.Error())
	}
	if comments != nil {
		t.Errorf("FetchPRReviews() comments = %v, want nil on error", comments)
	}
}

func TestFetchPRReviews_NoBranchPR(t *testing.T) {
	if !isGHAvailable() {
		t.Skip("gh not available, skipping integration test")
	}
	pm := New()
	// A branch name that certainly has no PR.
	comments, err := pm.FetchPRReviews("this-branch-certainly-has-no-pr-xyz-99999")
	if err != nil {
		t.Fatalf("FetchPRReviews() unexpected error: %v", err)
	}
	// No PR → nil or empty slice, never an error.
	if len(comments) != 0 {
		t.Errorf("FetchPRReviews() expected 0 comments for non-existent branch PR, got %d", len(comments))
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
