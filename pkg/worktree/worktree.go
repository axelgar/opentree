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
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = resolved
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

// worktreePath is where a branch's worktree belongs, or an error saying why
// that is not somewhere opentree may touch.
//
// Two things put a name out of bounds. It can collide with opentree's own
// files, which share the base directory — `opentree delete state.json` used to
// hand the registry itself to os.RemoveAll, and then fail on the branch, so
// the tool reported an error while having already destroyed the thing it
// tracks everything with. And it can climb: filepath.Join(root, ".opentree",
// "..") is the repository, and the same os.RemoveAll was one branch name away
// from it.
//
// Creating asks through here as well as deleting. The guard is only worth
// anything on the deleting side, but the two have to agree about where a
// worktree lives or the guard is protecting a different path than the one that
// gets removed.
func (m *Manager) worktreePath(branchName string) (string, error) {
	base := filepath.Join(m.repoRoot, m.baseDir)
	dirName := gitutil.SanitizeBranchName(branchName)
	if reservedDirName(dirName) {
		return "", fmt.Errorf("workspace name %q is reserved for opentree's state files", branchName)
	}
	path := filepath.Join(base, dirName)
	// One level down and no further. Anything else — "..", ".", a name that
	// sanitized to nothing, a nested path — leaves the base directory, and a
	// worktree has never lived anywhere but directly inside it.
	if filepath.Dir(path) != base {
		return "", fmt.Errorf("workspace name %q does not name a directory inside %s — refusing to touch %s", branchName, base, path)
	}
	return path, nil
}

// ensureBaseDir creates the directory the worktrees live in, and on the way
// makes sure git ignores it.
func (m *Manager) ensureBaseDir() error {
	opentreeDir := filepath.Join(m.repoRoot, m.baseDir)
	if err := os.MkdirAll(opentreeDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s directory: %w", m.baseDir, err)
	}
	m.excludeBaseDir()
	return nil
}

// excludeBaseDir asks git to ignore the directory the worktrees live in.
//
// The base directory defaults to .opentree inside the repository, so a single
// `opentree new feat/x` leaves a complete second checkout sitting in the user's
// working tree. Git notices: `git add -A` warns "adding embedded git
// repository" and stages a gitlink — mode 160000, pointing at a commit that
// exists nowhere but this machine — and whoever checks that branch out gets an
// empty directory with no way to fill it. Nothing in opentree used to write an
// ignore rule anywhere, so every repository except the author's own, which has
// carried the entry by hand for as long as it has existed, met that on its
// first workspace.
//
// The rule goes in .git/info/exclude rather than .gitignore. .gitignore is
// tracked and belongs to the project: writing to it turns "make me a worktree"
// into an uncommitted change in a shared file, which the user then has to
// either carry through somebody's review or explain away, and in a repository
// they have merely cloned they may want neither. The worktrees are one
// person's working area, their location is configurable per user, and none of
// it is shared, so a local-only exclude is the honest scope. It also keeps
// opentree from putting a file inside the base directory, where a workspace
// named .gitignore would collide with it.
//
// Every failure here is swallowed. A read-only .git, an exclude file that is
// not a file, a git too old to answer, no repository at all: none of that is a
// reason to refuse a worktree the user asked for, and opentree has no log to
// report it to.
func (m *Manager) excludeBaseDir() {
	entry, ok := m.excludeEntry()
	if !ok {
		return
	}

	// Ask git rather than read the file. The rule may already be in force from
	// an earlier run, from the project's own .gitignore, or from a global
	// core.excludesFile, and a second copy of a rule that already works is
	// litter in a file opentree does not own.
	if m.ignoredByGit(strings.TrimPrefix(entry, "/")) {
		return
	}

	commonDir := m.gitCommonDir()
	if commonDir == "" {
		return
	}
	excludeFile := filepath.Join(commonDir, "info", "exclude")

	existing, err := os.ReadFile(excludeFile)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	// check-ignore is the authority on whether the rule bites; this second look
	// is for the case where it could not answer, and stops a run of those from
	// stacking identical lines.
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return
		}
	}

	if err := os.MkdirAll(filepath.Dir(excludeFile), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(excludeFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// Append, never rewrite. Whatever the user keeps in here is theirs and in
	// the order they put it; a file that does not end in a newline would
	// otherwise have the rule welded onto its last line.
	var addition strings.Builder
	if len(existing) > 0 {
		if !strings.HasSuffix(string(existing), "\n") {
			addition.WriteString("\n")
		}
		addition.WriteString("\n")
	}
	addition.WriteString("# opentree's worktrees\n")
	addition.WriteString(entry + "\n")
	_, _ = f.WriteString(addition.String())
}

// excludeEntry is the gitignore pattern for the base directory, and whether git
// has any use for one. The documented ../worktrees layout puts the worktrees
// outside the repository, where nothing git does will ever look at them.
func (m *Manager) excludeEntry() (string, bool) {
	rel, err := filepath.Rel(m.repoRoot, filepath.Join(m.repoRoot, m.baseDir))
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	// Anchored, and marked as a directory: "/.opentree/" ignores the one
	// directory opentree owns and not whatever else happens to share the name
	// further down the tree.
	return "/" + rel + "/", true
}

// ignoredByGit reports whether git already ignores this path, whichever file
// the rule came from. The trailing slash the caller leaves on the path is what
// tells git to judge it as a directory: a "/.opentree/" rule matches
// directories only, and the question is worth asking before the directory
// exists.
func (m *Manager) ignoredByGit(path string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", "--", path)
	cmd.Dir = m.repoRoot
	return cmd.Run() == nil
}

// gitCommonDir is where the repository keeps what every worktree shares,
// info/exclude among it. That is .git in an ordinary clone, but a repository
// which is itself a linked worktree has a .git file and a private directory of
// its own, and the exclude git actually reads is in neither.
func (m *Manager) gitCommonDir() string {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = m.repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(m.repoRoot, dir)
	}
	return dir
}

// Create creates a new git worktree for the given branch
func (m *Manager) Create(branchName, baseBranch string) error {
	worktreePath, err := m.worktreePath(branchName)
	if err != nil {
		return err
	}

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	if err := m.ensureBaseDir(); err != nil {
		return err
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
	worktreePath, err := m.worktreePath(branchName)
	if err != nil {
		return false, err
	}

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return false, fmt.Errorf("worktree already exists: %s", worktreePath)
	}

	if err := m.ensureBaseDir(); err != nil {
		return false, err
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
	output, err := gitutil.Output(m.repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	return m.parseWorktrees(string(output))
}

// Delete removes a worktree and optionally deletes the branch
func (m *Manager) Delete(branchName string, deleteBranch bool) error {
	worktreePath, err := m.worktreePath(branchName)
	if err != nil {
		return err
	}

	// Distinct branch names can sanitize to the same directory ("feat/x" and
	// "feat-x" both map to feat-x), so make sure the directory we are about
	// to remove actually holds the requested branch's worktree. The same pass
	// records whether git still registers this path as a worktree at all.
	registered := false
	wts, listErr := m.List()
	if listErr == nil {
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

	// base_dir is where a worktree goes, not where it is. A workspace made
	// under one setting and deleted under another — the config edited, or a
	// repository-supplied value that is no longer honoured — computes a path
	// with nothing at it, and the delete would remove nothing, fail on a branch
	// git still considers checked out, and leave a row that cannot be deleted
	// at all.
	//
	// So ask git where the branch actually is. Its answer is not something a
	// config file can steer: the path comes from this repository's own worktree
	// registrations, matched on the exact branch, which is the same guarantee
	// the check above provides in the ordinary case.
	if !registered {
		if found, ok := m.worktreeForBranch(branchName); ok {
			worktreePath, registered = found, true
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
		//
		// A worktree is a directory, though. Lstat rather than Stat, so a
		// symlink is judged as the link it is rather than as whatever it
		// points at: git would not have put either one here, and this is the
		// arm that removes without asking git anything.
		if info, err := os.Lstat(worktreePath); err == nil && !info.IsDir() {
			return fmt.Errorf("%s is not a worktree directory — refusing to remove it", worktreePath)
		}
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("failed to remove leftover worktree directory: %w", err)
		}
		_ = m.Prune()
	}

	// Delete branch if requested, and only if there is one. A branch that has
	// already gone — deleted by hand, or never created because this worktree
	// adopted a detached HEAD — used to fail here, after the worktree was
	// already removed, and the caller reads that as "the delete failed": the
	// tmux window and the state entry survive, so the row cannot be deleted
	// and its dev server keeps the port.
	//
	// Asked with rev-parse rather than read off git's complaint, which is
	// translated.
	if deleteBranch && m.branchExists(branchName) {
		cmd := exec.Command("git", "branch", "-D", "--", branchName)
		cmd.Dir = m.repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to delete branch: %w\nOutput: %s", err, output)
		}
	}

	return nil
}

// branchExists reports whether the repository still has a local branch by this
// name.
func (m *Manager) branchExists(branchName string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branchName)
	cmd.Dir = m.repoRoot
	return cmd.Run() == nil
}

// worktreeForBranch is where git says a branch is checked out, across every
// worktree of this repository rather than only the ones under the current base
// dir — which is the point of it: the caller is asking precisely because the
// base dir no longer describes where the worktree went.
//
// Matched on the exact branch, so the path is one this repository's own git
// registered for the name that was asked for. A config file has no say in it.
func (m *Manager) worktreeForBranch(branchName string) (string, bool) {
	if branchName == "" {
		return "", false
	}
	out, err := gitutil.Output(m.repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	path := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if strings.TrimPrefix(line, "branch refs/heads/") == branchName && path != "" {
				return path, true
			}
		}
	}
	return "", false
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
	output, err := gitutil.Output(worktreePath, "diff", "--stat", baseCommit)
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %w", err)
	}

	return string(output), nil
}

// DiffFull returns the full unified diff for a worktree vs its base branch.
// If baseBranch is empty, it defaults to "main".
func (m *Manager) DiffFull(branchName string, baseBranch ...string) (string, error) {
	base := defaultBase(baseBranch...)

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	args := []string{"diff", "origin/" + base + "...HEAD"}
	if baseOutput, err := gitutil.Output(worktreePath, "merge-base", branchName, base); err == nil {
		args = []string{"diff", strings.TrimSpace(string(baseOutput)), "HEAD"}
	}

	output, err := gitutil.Output(worktreePath, args...)
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %w", err)
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
func (m *Manager) DiffStats(branchName string, baseBranch ...string) (string, []FileChange, error) {
	base := defaultBase(baseBranch...)

	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	// Compute merge-base once.
	baseCommit := m.resolveBase(branchName, base, worktreePath)

	// --stat output
	statOut, err := gitutil.Output(worktreePath, "diff", "--stat", baseCommit)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get diff stat: %w", err)
	}

	// --numstat output
	numOut, err := gitutil.Output(worktreePath, "diff", "--numstat", baseCommit)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get diff numstat: %w", err)
	}
	files := parseNumstat(string(numOut))

	// Mark uncommitted files.
	uncommitted, err := uncommittedFiles(worktreePath)
	if err != nil {
		return "", nil, err
	}
	for i := range files {
		if uncommitted[files[i].FileName] {
			files[i].Uncommitted = true
		}
	}
	return string(statOut), files, nil
}

// resolveBase finds the merge-base commit between branchName and the given base.
// Falls back to "origin/<base>" if merge-base computation fails.
func (m *Manager) resolveBase(branchName, base, worktreePath string) string {
	out, err := gitutil.Output(worktreePath, "merge-base", branchName, base)
	if err != nil {
		return "origin/" + base
	}
	return strings.TrimSpace(string(out))
}

// DiffUncommitted returns the unified diff of uncommitted changes (HEAD vs working tree).
func (m *Manager) DiffUncommitted(branchName string) (string, error) {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	output, err := gitutil.Output(worktreePath, "diff", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get uncommitted diff: %w", err)
	}

	return string(output), nil
}

// UntrackedFiles returns the paths of untracked (non-ignored) files in a worktree.
func (m *Manager) UntrackedFiles(branchName string) ([]string, error) {
	dirName := gitutil.SanitizeBranchName(branchName)
	worktreePath := filepath.Join(m.repoRoot, m.baseDir, dirName)

	out, err := gitutil.Output(worktreePath, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("failed to list untracked files: %w", err)
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
	out, err := gitutil.Output(worktreePath, "diff", "--name-only", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to list uncommitted files: %w", err)
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
