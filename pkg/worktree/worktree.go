package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Resolve symlinks so that path prefix comparisons against git output work
	// correctly on macOS, where os.TempDir() / t.TempDir() may return a
	// symlinked path (e.g. /var/folders/...) while git resolves to the real path
	// (e.g. /private/var/folders/...).
	if real, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = real
	}
	return &Manager{
		repoRoot: repoRoot,
		baseDir:  baseDir,
	}
}

// reservedDirName reports whether a sanitized workspace directory name would
// collide with opentree's own files, which live in the same base directory.
// A worktree at .opentree/state.json bricks every subsequent command.
func reservedDirName(dirName string) bool {
	switch dirName {
	case "state.json", "state.lock", "state.json.tmp":
		return true
	}
	return false
}

// Create creates a new git worktree for the given branch
func (m *Manager) Create(branchName, baseBranch string) error {
	dirName := gitutil.SanitizeBranchName(branchName)
	if reservedDirName(dirName) {
		return fmt.Errorf("workspace name %q is reserved for opentree's state files", branchName)
	}
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
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, "--", worktreePath, baseBranch)
	cmd.Dir = m.repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create git worktree: %w\nOutput: %s", err, output)
	}

	return nil
}

// CreateFromRemote creates a new git worktree for a branch that already exists on the remote.
// It fetches the branch from origin and checks it out into a new worktree directory.
// The returned createdBranch reports whether a new local branch was created (as
// opposed to checking out a pre-existing one) so cleanup paths know whether
// deleting the branch would destroy the user's own work.
func (m *Manager) CreateFromRemote(branchName string) (createdBranch bool, err error) {
	dirName := gitutil.SanitizeBranchName(branchName)
	if reservedDirName(dirName) {
		return false, fmt.Errorf("workspace name %q is reserved for opentree's state files", branchName)
	}
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return false, fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	// Create base directory if it doesn't exist
	opentreeDir := filepath.Join(m.repoRoot, m.baseDir)
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create %s directory: %w", m.baseDir, err)
	}

	// Fetch the remote branch
	fetchCmd := exec.Command("git", "fetch", "origin", branchName)
	fetchCmd.Dir = m.repoRoot
	if output, err := fetchCmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("failed to fetch remote branch %q: %w\nOutput: %s", branchName, err, output)
	}

	// Try to create worktree tracking the remote branch (creates local branch)
	cmd := exec.Command("git", "worktree", "add", "--track", "-b", branchName, "--", worktreePath, "origin/"+branchName)
	cmd.Dir = m.repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		// Local branch may already exist; fall back to checking it out directly
		cmd2 := exec.Command("git", "worktree", "add", "--", worktreePath, branchName)
		cmd2.Dir = m.repoRoot
		if output2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return false, fmt.Errorf("failed to create git worktree: %w\nOutput: %s\nFallback output: %s", err, output, output2)
		}
		return false, nil
	}

	return true, nil
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

	// Distinct branch names can sanitize to the same directory ("feat/x" and
	// "feat-x" both map to feat-x), so make sure the directory we are about
	// to remove actually holds the requested branch's worktree. The same pass
	// records whether git still registers this path as a worktree at all.
	registered := false
	if wts, err := m.List(); err == nil {
		for _, wt := range wts {
			if wt.Path != worktreePath {
				continue
			}
			registered = true
			if wt.Branch != "" && wt.Branch != branchName {
				return fmt.Errorf("worktree at %s has branch %q checked out, not %q — refusing to delete", worktreePath, wt.Branch, branchName)
			}
		}
	}

	if registered {
		// Remove worktree (--force handles untracked files)
		cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
		cmd.Dir = m.repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to remove worktree: %w\nOutput: %s", err, output)
		}
	} else {
		// Git no longer tracks this path as a worktree, but the directory may
		// linger from a partial/orphaned removal. `git worktree remove` would
		// fail with "is not a working tree", so remove the leftover directory
		// directly and prune any dangling metadata; then branch/state cleanup
		// can proceed as usual. os.RemoveAll is a no-op if it's already gone.
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("failed to remove leftover worktree directory: %w", err)
		}
		_ = m.Prune()
	}

	// Delete branch if requested
	if deleteBranch {
		cmd := exec.Command("git", "branch", "-D", "--", branchName)
		cmd.Dir = m.repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to delete branch: %w\nOutput: %s", err, output)
		}
	}

	return nil
}

// Push pushes a worktree's branch to origin, setting the upstream.
func (m *Manager) Push(branchName string) error {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Push the branch by name, not HEAD: if the agent switched branches
	// inside the worktree, HEAD would push the wrong branch while the PR is
	// created against branchName.
	cmd := exec.Command("git", "push", "-u", "origin", "refs/heads/"+branchName)
	cmd.Dir = worktreePath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push branch: %w\nOutput: %s", err, output)
	}
	return nil
}

// Prune removes stale git worktree metadata (registered worktrees whose
// directories no longer exist on disk).
func (m *Manager) Prune() error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = m.repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to prune worktrees: %w\nOutput: %s", err, output)
	}
	return nil
}

// defaultBase returns the base branch, defaulting to "main" when unset.
func defaultBase(baseBranch ...string) string {
	if len(baseBranch) > 0 && baseBranch[0] != "" {
		return baseBranch[0]
	}
	return "main"
}

// Diff returns the diffstat for a worktree vs its base branch.
// Includes both committed and uncommitted changes (compares merge-base to working tree).
// If baseBranch is empty, it defaults to "main".
func (m *Manager) Diff(branchName string, baseBranch ...string) (string, error) {
	base := defaultBase(baseBranch...)

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	baseCommit := m.resolveBase(branchName, base, worktreePath)
	// Compare merge-base to working tree (no HEAD) to include uncommitted changes
	cmd := exec.Command("git", "diff", "--stat", baseCommit)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %w\nOutput: %s", err, output)
	}

	return string(output), nil
}

// DiffFull returns the full unified diff for a worktree vs its base branch.
// If baseBranch is empty, it defaults to "main".
func (m *Manager) DiffFull(branchName string, baseBranch ...string) (string, error) {
	base := defaultBase(baseBranch...)

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
		return "", fmt.Errorf("failed to get diff: %w\nOutput: %s", err, output)
	}

	return string(output), nil
}

// DiffCombined returns the full unified diff for a worktree: committed changes
// (merge-base → HEAD) followed by uncommitted changes (HEAD → working tree),
// separated by section headers. The committed section is unlabeled when there
// are no uncommitted changes. Returns "No changes." when the worktree is clean.
func (m *Manager) DiffCombined(branchName string, baseBranch ...string) (string, error) {
	committed, err := m.DiffFull(branchName, baseBranch...)
	if err != nil {
		return "", err
	}
	uncommitted, uncommittedErr := m.DiffUncommitted(branchName)

	committedTrimmed := strings.TrimSpace(committed)
	uncommittedTrimmed := strings.TrimSpace(uncommitted)

	var sections []string
	if committedTrimmed != "" {
		if uncommittedTrimmed != "" {
			sections = append(sections, "══════ Committed Changes ══════\n\n"+committedTrimmed)
		} else {
			sections = append(sections, committedTrimmed)
		}
	}
	if uncommittedErr != nil {
		sections = append(sections, "══════ Uncommitted Changes ══════\n\n(error: "+uncommittedErr.Error()+")")
	} else if uncommittedTrimmed != "" {
		sections = append(sections, "══════ Uncommitted Changes ══════\n\n"+uncommittedTrimmed)
	}

	content := strings.Join(sections, "\n\n")
	if content == "" {
		content = "No changes."
	}
	return content, nil
}

// parseWorktrees parses the output of git worktree list --porcelain
func (m *Manager) parseWorktrees(output string) ([]Worktree, error) {
	var worktrees []Worktree
	var current *Worktree

	// Trailing separator so ".opentree" doesn't also match ".opentree-old/x".
	opentreePrefix := filepath.Join(m.repoRoot, m.baseDir) + string(filepath.Separator)

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

// numstatFileName resolves git's rename syntax in a numstat path field —
// "old => new" or "dir/{old => new}/rest" — to the new file name.
func numstatFileName(field string) string {
	arrow := strings.Index(field, " => ")
	if arrow < 0 {
		return field
	}
	if open := strings.Index(field, "{"); open >= 0 && open < arrow {
		if end := strings.Index(field[arrow:], "}"); end >= 0 {
			end += arrow
			resolved := field[:open] + field[arrow+4:end] + field[end+1:]
			// An empty old/new part leaves a doubled or leading slash
			// ("dir//f", "/f"); git paths are repo-relative, so both are safe
			// to collapse.
			resolved = strings.ReplaceAll(resolved, "//", "/")
			return strings.TrimPrefix(resolved, "/")
		}
	}
	return field[arrow+4:]
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

// DiffStats returns both the diffstat string and per-file change stats in a
// single call, computing git merge-base only once.
// If baseBranch is empty, it defaults to "main".
func (m *Manager) DiffStats(branchName string, baseBranch ...string) (stat string, files []FileChange, err error) {
	base := defaultBase(baseBranch...)

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Compute merge-base once.
	baseCommit := m.resolveBase(branchName, base, worktreePath)

	// --stat output
	statCmd := exec.Command("git", "diff", "--stat", baseCommit)
	statCmd.Dir = worktreePath
	statOut, statErr := statCmd.CombinedOutput()
	if statErr != nil {
		err = fmt.Errorf("failed to get diff stat: %w\nOutput: %s", statErr, statOut)
		return
	}
	stat = string(statOut)

	// --numstat output
	numCmd := exec.Command("git", "diff", "--numstat", baseCommit)
	numCmd.Dir = worktreePath
	numOut, numErr := numCmd.CombinedOutput()
	if numErr != nil {
		err = fmt.Errorf("failed to get diff numstat: %w\nOutput: %s", numErr, numOut)
		return
	}
	files = parseNumstat(string(numOut))

	// Mark uncommitted files.
	uncommitted, uncommittedErr := uncommittedFiles(worktreePath)
	if uncommittedErr != nil {
		err = uncommittedErr
		return
	}
	for i := range files {
		if uncommitted[files[i].FileName] {
			files[i].Uncommitted = true
		}
	}
	return
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
		return "", fmt.Errorf("failed to get uncommitted diff: %w\nOutput: %s", err, output)
	}

	return string(output), nil
}

// UntrackedFiles returns the paths of untracked (non-ignored) files in a worktree.
func (m *Manager) UntrackedFiles(branchName string) ([]string, error) {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list untracked files: %w\nOutput: %s", err, out)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// uncommittedFiles returns a set of file names that have uncommitted changes in a worktree.
func uncommittedFiles(worktreePath string) (map[string]bool, error) {
	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list uncommitted files: %w\nOutput: %s", err, out)
	}
	result := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result, nil
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
			if n, err := strconv.Atoi(parts[0]); err == nil {
				added = n
			}
		}
		if parts[1] != "-" {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				removed = n
			}
		}
		files = append(files, FileChange{
			FileName: numstatFileName(parts[2]),
			Added:    added,
			Removed:  removed,
		})
	}
	return files
}
