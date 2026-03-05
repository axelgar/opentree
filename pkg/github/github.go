package github

import (
	"fmt"
	"os/exec"
	"strings"
)

// PRManager handles GitHub PR operations
type PRManager struct{}

// New creates a new PR manager
func New() *PRManager {
	return &PRManager{}
}

// CreatePR creates a GitHub pull request using gh CLI
func (pm *PRManager) CreatePR(branch, baseBranch, title, body string) (string, error) {
	// Check if gh CLI is installed
	if !pm.isGHInstalled() {
		return "", fmt.Errorf("gh CLI is not installed. Install it from https://cli.github.com/")
	}

	// Check if user is authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("not authenticated with GitHub. Run 'gh auth login'")
	}

	// Create PR
	args := []string{"pr", "create", "--base", baseBranch, "--head", branch}
	
	if title != "" {
		args = append(args, "--title", title)
	} else {
		// Use branch name as title if not provided
		args = append(args, "--title", branch)
	}
	
	if body != "" {
		args = append(args, "--body", body)
	}

	cmd = exec.Command("gh", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w\nOutput: %s", err, output)
	}

	// Extract PR URL from output
	prURL := strings.TrimSpace(string(output))
	return prURL, nil
}

// GetPRStatus checks if a PR exists for the given branch
func (pm *PRManager) GetPRStatus(branch string) (string, error) {
	if !pm.isGHInstalled() {
		return "", nil // Silently fail if gh not installed
	}

	cmd := exec.Command("gh", "pr", "view", branch, "--json", "url", "--jq", ".url")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil // No PR exists
	}

	return strings.TrimSpace(string(output)), nil
}

// isGHInstalled checks if gh CLI is available
func (pm *PRManager) isGHInstalled() bool {
	cmd := exec.Command("gh", "--version")
	return cmd.Run() == nil
}
