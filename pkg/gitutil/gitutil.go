package gitutil

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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
		// Skip anything that isn't origin/<branch> — notably the
		// origin/HEAD symref, whose short name is literally "origin" and
		// used to show up in the branch picker as a bogus entry.
		branch, ok := strings.CutPrefix(line, "origin/")
		if !ok || branch == "" || branch == "HEAD" {
			continue
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

// RepoRoot returns the root directory of the main git repository, even when
// run from inside a linked worktree — where `--show-toplevel` would return
// the worktree's own root, making opentree nest worktrees and read the wrong
// state file.
func RepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("git is not installed (or not on PATH)")
		}
		return "", fmt.Errorf("not in a git repository")
	}
	commonDir, err := filepath.Abs(strings.TrimSpace(string(out)))
	if err == nil && filepath.Base(commonDir) == ".git" {
		root := filepath.Dir(commonDir)
		// Resolve symlinks (e.g. /var → /private/var on macOS) so the root
		// compares equal no matter where it was computed from.
		if real, rerr := filepath.EvalSymlinks(root); rerr == nil {
			root = real
		}
		return root, nil
	}

	// Fallback for layouts where the common dir isn't <root>/.git
	// (e.g. submodules): the current worktree's top level.
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
