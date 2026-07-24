package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
)

// Compile-time check that TmuxProcessManager satisfies ProcessManager.
var _ ProcessManager = (*TmuxProcessManager)(nil)

// GitHubManager abstracts GitHub operations so the workspace service is not
// coupled to a specific implementation. *github.PRManager satisfies this.
type GitHubManager interface {
	IsInstalled() bool
	GetIssue(number int) (*github.Issue, error)
	CreatePR(branch, baseBranch, title, body string) (string, error)
	FetchPRReviews(branch string) ([]github.ReviewComment, error)
}

// Compile-time check that *github.PRManager satisfies GitHubManager.
var _ GitHubManager = (*github.PRManager)(nil)

// Service orchestrates workspace lifecycle operations across worktree,
// tmux, state, and github packages. Both the TUI and CLI commands
// delegate to this service instead of orchestrating packages directly.
type Service struct {
	worktrees *worktree.Manager
	process   ProcessManager
	state     *state.Store
	github    GitHubManager
	cfg       *config.Config
	repoRoot  string
}

// New creates a Service by constructing all dependencies from repoRoot and config.
// This is the typical entry point for CLI commands.
func New(repoRoot string, cfg *config.Config) (*Service, error) {
	wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
	st, err := state.New(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state: %w", err)
	}
	tm := NewTmuxProcessManager(tmux.New(cfg.Tmux.SessionPrefix))
	gh := github.New()
	return NewService(repoRoot, cfg, wt, tm, st, gh), nil
}

// NewService creates a workspace service with pre-constructed dependencies.
// Use this when you need to share dependencies with other components (e.g., TUI).
func NewService(repoRoot string, cfg *config.Config, wt *worktree.Manager, pm ProcessManager, st *state.Store, gh GitHubManager) *Service {
	return &Service{
		worktrees: wt,
		process:   pm,
		state:     st,
		github:    gh,
		cfg:       cfg,
		repoRoot:  repoRoot,
	}
}

// Process returns the underlying ProcessManager for read-only access
// (e.g., listing windows, capturing pane output for display).
func (s *Service) Process() ProcessManager {
	return s.process
}

// ListWorkspaces returns all persisted workspaces.
func (s *Service) ListWorkspaces() []*state.Workspace {
	return s.state.ListWorkspaces()
}

// WindowStatuses returns each workspace's live status derived from process
// windows: "active" (window present and focused), "idle" (window exists but
// not focused), or "stopped" (no window). If the window list is unavailable
// (e.g. no tmux session), the returned map is empty and callers should degrade.
func (s *Service) WindowStatuses() map[string]string {
	result := make(map[string]string)
	windows, err := s.process.ListWindows()
	if err != nil {
		return result
	}
	byName := make(map[string]Window, len(windows))
	for _, w := range windows {
		byName[w.Name] = w
	}
	for _, ws := range s.state.ListWorkspaces() {
		win, ok := byName[ws.Name]
		if !ok {
			win, ok = byName[gitutil.SanitizeBranchName(ws.Name)]
		}
		switch {
		case ok && win.Active:
			result[ws.Name] = "active"
		case ok:
			result[ws.Name] = "idle"
		default:
			result[ws.Name] = "stopped"
		}
	}
	return result
}

// WorktreePath returns the filesystem path for a workspace's worktree directory.
func (s *Service) WorktreePath(name string) string {
	return filepath.Join(s.repoRoot, s.cfg.Worktree.BaseDir, gitutil.SanitizeBranchName(name))
}

// StatusFileName is the conventional file agents write to signal completion.
const StatusFileName = ".opentree-status.json"

// agentEnv builds the environment for an agent window: OPENTREE_STATUS_FILE
// tells status hooks (see `opentree agents setup`) where to write; they stay
// inert outside opentree. Set via the window environment (not typed into the
// shell) so the visible launch line stays clean.
func agentEnv(worktreePath string) []string {
	return []string{"OPENTREE_STATUS_FILE=" + filepath.Join(worktreePath, StatusFileName)}
}

// launchAgentWindow git-excludes the agent status file, then starts the
// configured agent in a new window for name's worktree. On failure the
// just-created worktree is rolled back; deleteBranch controls whether its
// branch is deleted too (a pre-existing branch may hold the user's own
// local-only commits).
func (s *Service) launchAgentWindow(name string, deleteBranch bool) (string, error) {
	worktreePath := s.WorktreePath(name)
	s.worktrees.EnsureExcluded(StatusFileName)
	if err := s.process.CreateWindow(name, worktreePath, s.cfg.Agent.Command, agentEnv(worktreePath), s.cfg.Agent.Args...); err != nil {
		_ = s.worktrees.Delete(name, deleteBranch)
		return "", fmt.Errorf("failed to create tmux window: %w", err)
	}
	return worktreePath, nil
}

// Create creates a new workspace: git worktree, tmux window with agent, and state entry.
func (s *Service) Create(name, baseBranch string) (*state.Workspace, error) {
	// Fail before creating anything, not with a "✓ Launched" success message
	// and a dead shell window.
	if err := s.cfg.Agent.Validate(); err != nil {
		return nil, err
	}

	if err := s.worktrees.Create(name, baseBranch); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w", err)
	}

	worktreePath, err := s.launchAgentWindow(name, true)
	if err != nil {
		return nil, err
	}

	ws := &state.Workspace{
		Name:        name,
		Branch:      name,
		BaseBranch:  baseBranch,
		CreatedAt:   time.Now(),
		Status:      "active",
		Agent:       s.cfg.Agent.Command,
		WorktreeDir: worktreePath,
	}
	if err := s.state.AddWorkspace(ws); err != nil {
		// Roll back: a worktree+window with no state entry is invisible to
		// the TUI and to Prune, so it would leak forever.
		_ = s.process.KillWindow(name)
		_ = s.worktrees.Delete(name, true)
		return nil, fmt.Errorf("failed to save workspace state: %w", err)
	}

	return ws, nil
}

// CreateFromIssue fetches a GitHub issue and creates a workspace whose branch
// name and metadata come from the issue. The user hands the agent the issue
// context themselves.
func (s *Service) CreateFromIssue(issueNum int, baseBranch string) (*state.Workspace, error) {
	if !s.github.IsInstalled() {
		return nil, fmt.Errorf("gh CLI is not installed — install it from https://cli.github.com/")
	}

	issue, err := s.github.GetIssue(issueNum)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch issue: %w", err)
	}

	branchName := github.IssueBranchName(issue.Number, issue.Title)
	if baseBranch == "" {
		baseBranch = s.cfg.Worktree.DefaultBase
	}

	ws, err := s.Create(branchName, baseBranch)
	if err != nil {
		return nil, err
	}

	// Update workspace with issue metadata
	ws.IssueNumber = issue.Number
	ws.IssueTitle = issue.Title
	if err := s.state.UpdateWorkspace(ws); err != nil {
		return nil, fmt.Errorf("failed to update workspace with issue metadata: %w", err)
	}

	return ws, nil
}

// CreateFromRemoteBranch creates a workspace from an existing remote branch.
// The branch is fetched from origin and checked out into a new worktree.
func (s *Service) CreateFromRemoteBranch(branchName string) (*state.Workspace, error) {
	if err := s.cfg.Agent.Validate(); err != nil {
		return nil, err
	}

	createdBranch, err := s.worktrees.CreateFromRemote(branchName)
	if err != nil {
		return nil, fmt.Errorf("failed to create worktree from remote: %w", err)
	}

	worktreePath, err := s.launchAgentWindow(branchName, createdBranch)
	if err != nil {
		return nil, err
	}

	ws := &state.Workspace{
		Name:         branchName,
		Branch:       branchName,
		BaseBranch:   s.cfg.Worktree.DefaultBase,
		CreatedAt:    time.Now(),
		Status:       "active",
		Agent:        s.cfg.Agent.Command,
		WorktreeDir:  worktreePath,
		BranchPushed: true,
	}
	if err := s.state.AddWorkspace(ws); err != nil {
		_ = s.process.KillWindow(branchName)
		_ = s.worktrees.Delete(branchName, createdBranch)
		return nil, fmt.Errorf("failed to save workspace state: %w", err)
	}

	return ws, nil
}

// Delete removes a workspace: removes worktree and branch, kills the tmux
// window, and deletes state. If this was the last workspace, the tmux
// session is also killed.
func (s *Service) Delete(name string) error {
	// Remove the worktree BEFORE killing the window: if removal fails
	// (locked worktree, cwd inside it, ...) the agent session survives.
	if err := s.worktrees.Delete(name, true); err != nil {
		return fmt.Errorf("failed to delete worktree: %w", err)
	}

	// Kill tmux window (ignore error if window doesn't exist)
	_ = s.process.KillWindow(name)

	if err := s.state.DeleteWorkspace(name); err != nil {
		return fmt.Errorf("failed to delete workspace state: %w", err)
	}

	// Clean up tmux session if no workspaces remain
	if len(s.state.ListWorkspaces()) == 0 {
		_ = s.process.KillSession()
	}

	return nil
}

// DeleteMultiple removes multiple workspaces in sequence. A failure on one
// workspace doesn't abandon the rest; all errors are reported together.
func (s *Service) DeleteMultiple(names []string) error {
	var errs []error
	for _, name := range names {
		if err := s.worktrees.Delete(name, true); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", name, err))
			continue
		}
		_ = s.process.KillWindow(name)
		if err := s.state.DeleteWorkspace(name); err != nil {
			errs = append(errs, fmt.Errorf("delete state %s: %w", name, err))
		}
	}

	if len(s.state.ListWorkspaces()) == 0 {
		_ = s.process.KillSession()
	}

	return errors.Join(errs...)
}

// HasChanges reports work that would be lost by deleting the workspace:
// commits ahead of the base branch, tracked modifications, and untracked files.
// Returns an empty string when the worktree is clean or absent from disk.
// A missing state entry does not skip the check — the worktree itself is inspected.
func (s *Service) HasChanges(name string) (string, error) {
	if _, err := os.Stat(s.WorktreePath(name)); err != nil {
		return "", nil // no worktree on disk — nothing to lose
	}

	baseBranch := s.cfg.Worktree.DefaultBase
	if ws, err := s.state.GetWorkspace(name); err == nil && ws.BaseBranch != "" {
		baseBranch = ws.BaseBranch
	}

	// Merge-base → working tree: catches commits ahead plus tracked modifications.
	diff, err := s.worktrees.Diff(name, baseBranch)
	if err != nil {
		return "", err
	}
	untracked, err := s.worktrees.UntrackedFiles(name)
	if err != nil {
		return "", err
	}
	if len(untracked) > 0 {
		diff = strings.TrimRight(diff, "\n") + fmt.Sprintf("\n %d untracked file(s):\n   %s\n",
			len(untracked), strings.Join(untracked, "\n   "))
	}
	return diff, nil
}

// SendReviewsToAgent fetches all PR review comments for the workspace's branch
// and sends them as a formatted prompt to the running agent in the tmux window.
// Returns the number of review comments sent, or 0 if none were found.
func (s *Service) SendReviewsToAgent(name string) (int, error) {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return 0, fmt.Errorf("workspace not found: %w", err)
	}

	comments, err := s.github.FetchPRReviews(ws.Branch)
	// Partial results (top-level reviews fetched, inline-thread fetch failed)
	// are still sent rather than discarded.
	if err != nil && len(comments) == 0 {
		return 0, fmt.Errorf("failed to fetch PR reviews: %w", err)
	}
	if len(comments) == 0 {
		return 0, nil
	}

	// Review bodies are attacker-controlled text (anyone can review a public
	// PR). Pasting them into a pane sitting at a shell prompt — agent
	// crashed or exited — would execute them as shell commands.
	if cmdName, err := s.process.PaneCurrentCommand(name); err == nil && isShell(cmdName) {
		return 0, fmt.Errorf("the agent is not running in workspace %q (its window is at a shell prompt) — start it before sending reviews", name)
	}

	prompt := github.FormatReviewsPrompt(comments)
	if err := s.process.SendMessage(name, prompt); err != nil {
		return 0, fmt.Errorf("failed to send reviews to agent: %w", err)
	}

	return len(comments), nil
}

// isShell reports whether a pane's current command is an interactive shell
// rather than a running agent.
func isShell(command string) bool {
	switch command {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "tcsh", "csh", "nu", "pwsh":
		return true
	}
	return false
}

// CreatePR creates a GitHub pull request for a workspace.
func (s *Service) CreatePR(name, title, body string) (string, error) {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return "", fmt.Errorf("workspace not found: %w", err)
	}

	if !s.github.IsInstalled() {
		return "", fmt.Errorf("gh CLI is not installed — install it from https://cli.github.com/")
	}

	if s.cfg.GitHub.AutoPush != nil && *s.cfg.GitHub.AutoPush {
		if err := s.worktrees.Push(ws.Branch); err != nil {
			return "", fmt.Errorf("failed to push branch: %w", err)
		}
	}

	prURL, err := s.github.CreatePR(ws.Branch, ws.BaseBranch, title, body)
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w", err)
	}

	// Best-effort: the 30s status poll self-corrects BranchPushed from
	// ls-remote, so a failed state write must not fail a created PR.
	ws.BranchPushed = true
	_ = s.state.UpdateWorkspace(ws)

	return prURL, nil
}

// Prune removes state entries (and their tmux windows) for workspaces whose
// worktree directory no longer exists on disk, and clears git's stale
// worktree metadata. Branches are deliberately left intact.
func (s *Service) Prune() ([]string, error) {
	if err := s.worktrees.Prune(); err != nil {
		return nil, err
	}

	var pruned []string
	for _, ws := range s.state.ListWorkspaces() {
		dir := ws.WorktreeDir
		if dir == "" {
			dir = s.WorktreePath(ws.Name)
		}
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		_ = s.process.KillWindow(ws.Name)
		if err := s.state.DeleteWorkspace(ws.Name); err != nil {
			return pruned, fmt.Errorf("failed to prune %s: %w", ws.Name, err)
		}
		pruned = append(pruned, ws.Name)
	}
	return pruned, nil
}
