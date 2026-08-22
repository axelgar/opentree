package gitutil

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Output runs a git command in dir and returns what it wrote to standard
// output, for the callers whose output is data rather than a message.
//
// stdout alone is the whole point. CombinedOutput folds stderr into the same
// bytes, and git writes a great deal to stderr that is not a failure — "warning:
// refname 'x' is ambiguous", the advice blocks it prints on detached HEAD,
// progress from a repository large enough to need it. Every one of those
// arrives glued to the front of the value: a merge-base whose sha now has a
// sentence in front of it resolves to nothing, and a --numstat with a warning
// on line one parses one line short. Neither fails loudly.
//
// What stderr said is not thrown away, it is moved to where it belongs. It goes
// into the error, which is what a caller reporting a failure wants to print,
// rather than into the value a caller reporting success is about to parse.
//
// Commands whose output is only ever a message keep CombinedOutput on purpose —
// `worktree add`, `fetch`, `push`, `branch -D`. There is nothing to corrupt, and
// interleaved is how a human reads them.
func Output(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := bytes.TrimSpace(exitErr.Stderr); len(stderr) > 0 {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr)
		}
	}
	return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}

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
		if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
			root = resolved
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

// WorktreeRoot returns the top level of the working tree the process is
// standing in — the linked worktree's own root, where RepoRoot returns the
// main repository's.
//
// The two differ exactly when the caller is inside a worktree opentree made,
// which is what makes it worth asking: opentree runs from both, and a
// worktree's checked-out files belong to a branch rather than to the project.
func WorktreeRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	root := strings.TrimSpace(string(out))
	// Resolved for the same reason RepoRoot resolves its own: the two are
	// compared, and only one of them would otherwise have been through
	// /var → /private/var.
	if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
		root = resolved
	}
	return root, nil
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
