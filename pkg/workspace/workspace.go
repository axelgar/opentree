package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
)

// Compile-time check that TmuxProcessManager satisfies ProcessManager.
var _ ProcessManager = (*TmuxProcessManager)(nil)

// Service orchestrates workspace lifecycle operations across worktree,
// tmux, state, and github packages. Both the TUI and CLI commands
// delegate to this service instead of orchestrating packages directly.
type Service struct {
	worktrees *worktree.Manager
	process   ProcessManager
	state     *state.Store
	github    *github.PRManager
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
func NewService(repoRoot string, cfg *config.Config, wt *worktree.Manager, pm ProcessManager, st *state.Store, gh *github.PRManager) *Service {
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

// WorktreePath returns the filesystem path for a workspace's worktree directory.
func (s *Service) WorktreePath(name string) string {
	return filepath.Join(s.repoRoot, s.cfg.Worktree.BaseDir, gitutil.SanitizeBranchName(name))
}

// StatusFileName is the conventional file agents write to signal completion.
const StatusFileName = ".opentree-status.json"

// agentsInstructions is written to AGENTS.md in every new worktree so agents
// discover the status-file convention.
const agentsInstructions = `# Opentree Workspace

This workspace is managed by [opentree](https://github.com/axelgar/opentree).

## Signaling completion

When you finish your task, write a JSON status file so the dashboard knows:

` + "```" + `
echo '{"status":"success","message":"Task completed"}' > .opentree-status.json
` + "```" + `

Valid ` + "`status`" + ` values: ` + "`success`" + `, ` + "`failure`" + `, ` + "`error`" + `, ` + "`in_progress`" + `

The ` + "`message`" + ` field is optional free text shown in the dashboard.
`

// Create creates a new workspace: git worktree, tmux window with agent, and state entry.
func (s *Service) Create(name, baseBranch string) (*state.Workspace, error) {
	if err := s.worktrees.Create(name, baseBranch); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w", err)
	}

	worktreePath := s.WorktreePath(name)

	// Write AGENTS.md so agents discover the status-file convention
	_ = os.WriteFile(filepath.Join(worktreePath, "AGENTS.md"), []byte(agentsInstructions), 0644)

	agentCmd := s.cfg.Agent.Command
	if err := s.process.CreateWindow(name, worktreePath, agentCmd, s.cfg.Agent.Args...); err != nil {
		return nil, fmt.Errorf("failed to create tmux window: %w", err)
	}

	ws := &state.Workspace{
		Name:        name,
		Branch:      name,
		BaseBranch:  baseBranch,
		CreatedAt:   time.Now(),
		Status:      "active",
		Agent:       agentCmd,
		WorktreeDir: worktreePath,
	}
	if err := s.state.AddWorkspace(ws); err != nil {
		return nil, fmt.Errorf("failed to save workspace state: %w", err)
	}

	return ws, nil
}

// CreateFromIssue fetches a GitHub issue and creates a workspace with issue context.
// A TASK.md file is written into the worktree with the issue details.
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

	// Write TASK.md with issue context for the AI agent
	taskContent := github.IssueTaskContent(issue)
	taskFile := filepath.Join(ws.WorktreeDir, "TASK.md")
	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		// Non-fatal: workspace was created successfully
		fmt.Printf("Warning: could not write TASK.md: %v\n", err)
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
	if err := s.worktrees.CreateFromRemote(branchName); err != nil {
		return nil, fmt.Errorf("failed to create worktree from remote: %w", err)
	}

	worktreePath := s.WorktreePath(branchName)

	// Write AGENTS.md so agents discover the status-file convention
	_ = os.WriteFile(filepath.Join(worktreePath, "AGENTS.md"), []byte(agentsInstructions), 0644)

	agentCmd := s.cfg.Agent.Command
	if err := s.process.CreateWindow(branchName, worktreePath, agentCmd, s.cfg.Agent.Args...); err != nil {
		return nil, fmt.Errorf("failed to create tmux window: %w", err)
	}

	ws := &state.Workspace{
		Name:         branchName,
		Branch:       branchName,
		BaseBranch:   "",
		CreatedAt:    time.Now(),
		Status:       "active",
		Agent:        agentCmd,
		WorktreeDir:  worktreePath,
		BranchPushed: true,
	}
	if err := s.state.AddWorkspace(ws); err != nil {
		return nil, fmt.Errorf("failed to save workspace state: %w", err)
	}

	return ws, nil
}

// Delete removes a workspace: kills tmux window, removes worktree and branch, deletes state.
// If this was the last workspace, the tmux session is also killed.
func (s *Service) Delete(name string) error {
	// Kill tmux window (ignore error if window doesn't exist)
	_ = s.process.KillWindow(name)

	if err := s.worktrees.Delete(name, true); err != nil {
		return fmt.Errorf("failed to delete worktree: %w", err)
	}

	if err := s.state.DeleteWorkspace(name); err != nil {
		return fmt.Errorf("failed to delete workspace state: %w", err)
	}

	// Clean up tmux session if no workspaces remain
	if len(s.state.ListWorkspaces()) == 0 {
		_ = s.process.KillSession()
	}

	return nil
}

// DeleteMultiple removes multiple workspaces in sequence.
func (s *Service) DeleteMultiple(names []string) error {
	for _, name := range names {
		_ = s.process.KillWindow(name)
		if err := s.worktrees.Delete(name, true); err != nil {
			return fmt.Errorf("delete %s: %w", name, err)
		}
		if err := s.state.DeleteWorkspace(name); err != nil {
			return fmt.Errorf("delete state %s: %w", name, err)
		}
	}

	if len(s.state.ListWorkspaces()) == 0 {
		_ = s.process.KillSession()
	}

	return nil
}

// HasChanges returns the diff between a workspace branch and its base branch.
// Returns empty string if there are no changes. Used by CLI for delete confirmation.
func (s *Service) HasChanges(name string) (string, error) {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return "", nil // No state entry means we can't check
	}

	baseBranch := ws.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	return s.worktrees.DiffBranches(name, baseBranch)
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

	prURL, err := s.github.CreatePR(ws.Branch, ws.BaseBranch, title, body)
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w", err)
	}

	return prURL, nil
}
