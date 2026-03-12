package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// ListRemoteBranches returns up to limit remote branches sorted by most recent commit.
// It reads locally cached remote-tracking refs (no network call required).
// Branch names are returned without the "origin/" prefix.
func ListRemoteBranches(repoRoot string, limit int) ([]string, error) {
	cmd := exec.Command(
		"git", "for-each-ref",
		fmt.Sprintf("--count=%d", limit),
		"--sort=-committerdate",
		"--format=%(refname:short)",
		"refs/remotes/origin",
	)
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list remote branches: %w", err)
	}

	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip "origin/" prefix
		branch := strings.TrimPrefix(line, "origin/")
		branches = append(branches, branch)
	}
	return branches, nil
}

// RepoRoot returns the root directory of the current git repository.
func RepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(output)), nil
}

// ValidateBranchName checks whether name is a valid git branch name
// by running `git check-ref-format --branch`.
func ValidateBranchName(name string) error {
	if name == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	cmd := exec.Command("git", "check-ref-format", "--branch", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("invalid branch name %q: %s", name, msg)
		}
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

// SanitizeBranchName converts a branch name to a safe directory/window name.
// Replaces "/" and ":" with "-".
func SanitizeBranchName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

// BranchToTitle converts a git branch name into a human-readable PR title.
// It strips a conventional prefix (e.g. "feat/", "fix/"), replaces hyphens
// and underscores with spaces, and capitalizes the first letter.
func BranchToTitle(branch string) string {
	// Strip everything up to and including the last "/"
	if idx := strings.LastIndex(branch, "/"); idx != -1 {
		branch = branch[idx+1:]
	}
	branch = strings.ReplaceAll(branch, "-", " ")
	branch = strings.ReplaceAll(branch, "_", " ")
	if len(branch) > 0 {
		branch = strings.ToUpper(branch[:1]) + branch[1:]
	}
	return branch
}
