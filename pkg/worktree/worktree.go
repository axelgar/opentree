package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager handles git worktree operations
type Manager struct {
	repoRoot string
	baseDir  string
}

// New creates a new worktree manager
func New() *Manager {
	return &Manager{
		baseDir: ".opentree",
	}
}

// Create creates a new git worktree for the given branch
func (m *Manager) Create(branchName, baseBranch string) error {
	if err := m.ensureGitRepo(); err != nil {
		return err
	}

	// Sanitize branch name for directory
	dirName := strings.ReplaceAll(branchName, "/", "-")
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	// Create .opentree directory if it doesn't exist
	opentreeDir := filepath.Join(m.repoRoot, m.baseDir)
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		return fmt.Errorf("failed to create .opentree directory: %w", err)
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
	if err := m.ensureGitRepo(); err != nil {
		return nil, err
	}

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
	if err := m.ensureGitRepo(); err != nil {
		return err
	}

	dirName := strings.ReplaceAll(branchName, "/", "-")
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

// Diff returns the diffstat for a worktree vs its base branch
func (m *Manager) Diff(branchName string) (string, error) {
	if err := m.ensureGitRepo(); err != nil {
		return "", err
	}

	dirName := strings.ReplaceAll(branchName, "/", "-")
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Get the base branch (merge-base)
	cmd := exec.Command("git", "merge-base", branchName, "main")
	cmd.Dir = worktreePath
	baseOutput, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback to origin/main
		cmd = exec.Command("git", "diff", "--stat", "origin/main...HEAD")
	} else {
		baseCommit := strings.TrimSpace(string(baseOutput))
		cmd = exec.Command("git", "diff", "--stat", baseCommit)
	}

	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %w", err)
	}

	return string(output), nil
}

// DiffFull returns the full unified diff for a worktree vs its base branch.
func (m *Manager) DiffFull(branchName string) (string, error) {
	if err := m.ensureGitRepo(); err != nil {
		return "", err
	}

	dirName := strings.ReplaceAll(branchName, "/", "-")
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	cmd := exec.Command("git", "merge-base", branchName, "main")
	cmd.Dir = worktreePath
	baseOutput, err := cmd.CombinedOutput()
	if err != nil {
		cmd = exec.Command("git", "diff", "origin/main...HEAD")
	} else {
		baseCommit := strings.TrimSpace(string(baseOutput))
		cmd = exec.Command("git", "diff", baseCommit)
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
	if err := m.ensureGitRepo(); err != nil {
		return "", err
	}

	// Use git diff to compare the two branches
	cmd := exec.Command("git", "diff", "--stat", baseBranch+"..."+branchName)
	cmd.Dir = m.repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get diff between branches: %w", err)
	}

	return string(output), nil
}

// ensureGitRepo finds the git repository root
func (m *Manager) ensureGitRepo() error {
	if m.repoRoot != "" {
		return nil
	}

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("not in a git repository")
	}

	m.repoRoot = strings.TrimSpace(string(output))
	return nil
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
				// Only include worktrees in .opentree directory
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
	FileName string
	Added    int
	Removed  int
}

// DiffFileStats returns per-file change stats for a worktree vs its base branch.
func (m *Manager) DiffFileStats(branchName string) ([]FileChange, error) {
	if err := m.ensureGitRepo(); err != nil {
		return nil, err
	}

	dirName := strings.ReplaceAll(branchName, "/", "-")
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	cmd := exec.Command("git", "merge-base", branchName, "main")
	cmd.Dir = worktreePath
	baseOutput, err := cmd.CombinedOutput()
	if err != nil {
		cmd = exec.Command("git", "diff", "--numstat", "origin/main...HEAD")
	} else {
		baseCommit := strings.TrimSpace(string(baseOutput))
		cmd = exec.Command("git", "diff", "--numstat", baseCommit)
	}

	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}

	return parseNumstat(string(output)), nil
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
