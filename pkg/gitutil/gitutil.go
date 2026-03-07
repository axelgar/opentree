package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// RepoRoot returns the root directory of the current git repository.
func RepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(output)), nil
}

// SanitizeBranchName converts a branch name to a safe directory/window name.
// Replaces "/" and ":" with "-".
func SanitizeBranchName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}
