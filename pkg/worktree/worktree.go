package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/axelgar/opentree/pkg/gitutil"
)

// Manager handles git worktree operations
type Manager struct {
	repoRoot string
	baseDir  string
}

// New creates a new worktree manager with explicit repo root and base directory.
func New(repoRoot, baseDir string) *Manager {
	return &Manager{
		repoRoot: repoRoot,
		baseDir:  baseDir,
	}
}

// Create creates a new git worktree for the given branch
func (m *Manager) Create(branchName, baseBranch string) error {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	// Create base directory if it doesn't exist
	opentreeDir := filepath.Join(m.repoRoot, m.baseDir)
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s directory: %w", m.baseDir, err)
	}

	// Create git worktree
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, worktreePath, baseBranch)
	cmd.Dir = m.repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create git worktree: %w\nOutput: %s", err, output)
	}

	return nil
}

// List returns all opentree-managed worktrees
func (m *Manager) List() ([]Worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = m.repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	return m.parseWorktrees(string(output))
}

// Delete removes a worktree and optionally deletes the branch
func (m *Manager) Delete(branchName string, deleteBranch bool) error {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Remove worktree
	cmd := exec.Command("git", "worktree", "remove", worktreePath)
	cmd.Dir = m.repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove worktree: %w\nOutput: %s", err, output)
	}

	// Delete branch if requested
	if deleteBranch {
		cmd = exec.Command("git", "branch", "-D", branchName)
		cmd.Dir = m.repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to delete branch: %w\nOutput: %s", err, output)
		}
	}

	return nil
}

// Diff returns the diffstat for a worktree vs its base branch.
// Includes both committed and uncommitted changes (compares merge-base to working tree).
// If baseBranch is empty, it defaults to "main".
func (m *Manager) Diff(branchName string, baseBranch ...string) (string, error) {
	base := "main"
	if len(baseBranch) > 0 && baseBranch[0] != "" {
		base = baseBranch[0]
	}

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	baseCommit := m.resolveBase(branchName, base, worktreePath)
	// Compare merge-base to working tree (no HEAD) to include uncommitted changes
	cmd := exec.Command("git", "diff", "--stat", baseCommit)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %w", err)
	}

	return string(output), nil
}

// DiffFull returns the full unified diff for a worktree vs its base branch.
// If baseBranch is empty, it defaults to "main".
func (m *Manager) DiffFull(branchName string, baseBranch ...string) (string, error) {
	base := "main"
	if len(baseBranch) > 0 && baseBranch[0] != "" {
		base = baseBranch[0]
	}

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	cmd := exec.Command("git", "merge-base", branchName, base)
	cmd.Dir = worktreePath
	baseOutput, err := cmd.CombinedOutput()
	if err != nil {
		cmd = exec.Command("git", "diff", "origin/"+base+"...HEAD")
	} else {
		baseCommit := strings.TrimSpace(string(baseOutput))
		cmd = exec.Command("git", "diff", baseCommit, "HEAD")
	}

	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %w", err)
	}

	return string(output), nil
}

// DiffBranches compares a worktree branch with a base branch
func (m *Manager) DiffBranches(branchName, baseBranch string) (string, error) {
	// Use git diff to compare the two branches
	cmd := exec.Command("git", "diff", "--stat", baseBranch+"..."+branchName)
	cmd.Dir = m.repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get diff between branches: %w", err)
	}

	return string(output), nil
}

// parseWorktrees parses the output of git worktree list --porcelain
func (m *Manager) parseWorktrees(output string) ([]Worktree, error) {
	var worktrees []Worktree
	var current *Worktree

	opentreePrefix := filepath.Join(m.repoRoot, m.baseDir)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if current != nil {
				if strings.HasPrefix(current.Path, opentreePrefix) {
					worktrees = append(worktrees, *current)
				}
				current = nil
			}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			current = &Worktree{
				Path: strings.TrimPrefix(line, "worktree "),
			}
		} else if current != nil && strings.HasPrefix(line, "branch ") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}

	// Handle last entry
	if current != nil && strings.HasPrefix(current.Path, opentreePrefix) {
		worktrees = append(worktrees, *current)
	}

	return worktrees, nil
}

// Worktree represents a git worktree
type Worktree struct {
	Path   string
	Branch string
}

// FileChange represents per-file diff stats.
type FileChange struct {
	FileName    string
	Added       int
	Removed     int
	Uncommitted bool // true if the file has uncommitted changes
}

// DiffFileStats returns per-file change stats for a worktree vs its base branch.
// Includes both committed and uncommitted changes, with each file marked accordingly.
// If baseBranch is empty, it defaults to "main".
func (m *Manager) DiffFileStats(branchName string, baseBranch ...string) ([]FileChange, error) {
	base := "main"
	if len(baseBranch) > 0 && baseBranch[0] != "" {
		base = baseBranch[0]
	}

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	baseCommit := m.resolveBase(branchName, base, worktreePath)
	cmd := exec.Command("git", "diff", "--numstat", baseCommit)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}

	files := parseNumstat(string(output))

	uncommitted := uncommittedFiles(worktreePath)
	for i := range files {
		if uncommitted[files[i].FileName] {
			files[i].Uncommitted = true
		}
	}

	return files, nil
}

// resolveBase finds the merge-base commit between branchName and the given base.
// Falls back to "origin/<base>" if merge-base computation fails.
func (m *Manager) resolveBase(branchName, base, worktreePath string) string {
	cmd := exec.Command("git", "merge-base", branchName, base)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "origin/" + base
	}
	return strings.TrimSpace(string(out))
}

// DiffUncommitted returns the unified diff of uncommitted changes (HEAD vs working tree).
func (m *Manager) DiffUncommitted(branchName string) (string, error) {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get uncommitted diff: %w", err)
	}

	return string(output), nil
}

// uncommittedFiles returns a set of file names that have uncommitted changes in a worktree.
func uncommittedFiles(worktreePath string) map[string]bool {
	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	result := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result
}

func parseNumstat(output string) []FileChange {
	var files []FileChange
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added := 0
		removed := 0
		if parts[0] != "-" {
			fmt.Sscanf(parts[0], "%d", &added)
		}
		if parts[1] != "-" {
			fmt.Sscanf(parts[1], "%d", &removed)
		}
		files = append(files, FileChange{
			FileName: parts[2],
			Added:    added,
			Removed:  removed,
		})
	}
	return files
}
