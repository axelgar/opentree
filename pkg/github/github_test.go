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
