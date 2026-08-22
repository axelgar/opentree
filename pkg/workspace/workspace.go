package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/skills"
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

// launchAgentWindow starts the workspace's agent in a new tmux window for
// name's worktree. On failure the just-created worktree is rolled back;
// deleteBranch controls whether its branch is deleted too (a pre-existing
// branch may hold the user's own local-only commits).
func (s *Service) launchAgentWindow(name string, deleteBranch bool) (string, error) {
	worktreePath := s.WorktreePath(name)

	launch, err := s.agentLaunch(name, s.cfg.Agent.Command, worktreePath)
	if err != nil {
		_ = s.worktrees.Delete(name, deleteBranch)
		return "", err
	}
	if err := launch(); err != nil {
		_ = s.worktrees.Delete(name, deleteBranch)
		return "", fmt.Errorf("failed to create tmux window: %w", err)
	}
	return worktreePath, nil
}

// agentLaunch is the not-yet-run window creation for a workspace. Both callers
// go through it so the choice cannot drift between creating a workspace and
// reopening one.
//
// Every window runs `opentree chat`, which holds the ACP connection and draws
// the conversation itself. An agent the registry does not know is an error
// rather than a fallback: it is a workspace created by an older opentree that
// still supported agents drawing their own screen, and quietly opening a chat
// for a binary that cannot serve one would fail further from the cause.
func (s *Service) agentLaunch(name, agentName, worktreePath string) (func() error, error) {
	agent := config.FindAgent(agentName)
	if agent == nil {
		return nil, config.UnknownAgentError(agentName)
	}
	command, env, args := chatCommand(name, agent.Command)
	return func() error { return s.process.CreateAppWindow(name, worktreePath, command, env, args...) }, nil
}

// chatCommand is how a tmux window runs opentree's own chat view.
//
// The binary is resolved from the running process rather than PATH: opentree is
// frequently run from a build directory, and a window that launches a different
// opentree than the one that created it would be a confusing way to fail.
//
// The agent is passed explicitly for a subtler reason. The chat runs with its
// working directory inside the worktree, where opentree.toml is a checked-out
// file that can name a different agent than the repository's — a branch that
// edits it, or simply an uncommitted change to the one the launcher read.
// Deciding once here and telling the child is the only way the two agree.
func chatCommand(name, agentCommand string) (string, []string, []string) {
	exe, err := os.Executable()
	if err != nil {
		exe = "opentree"
	}
	return exe, nil, []string{"chat", name, "--agent", agentCommand}
}

// EnsureWindow reopens a workspace's agent window when it no longer serves one,
// and reports whether it had to. Losing the window is an ordinary thing: the
// chat is its window's process, so anything that ends it — a killed window, a
// restarted tmux server, a chat run outside opentree and quit — takes the
// window too, while the worktree and its resumable conversation both outlive
// that. Attaching should bring the workspace back rather than refuse.
func (s *Service) EnsureWindow(name string) (bool, error) {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return false, err
	}

	if !s.needsWindow(name) {
		return false, nil
	}

	worktreePath := s.WorktreePath(name)
	launch, err := s.agentLaunch(name, ws.Agent, worktreePath)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(worktreePath); err != nil {
		return false, fmt.Errorf("worktree for %q is missing: %w", name, err)
	}
	if err := launch(); err != nil {
		return false, fmt.Errorf("failed to reopen window for %q: %w", name, err)
	}
	return true, nil
}

// needsWindow reports whether the workspace has no usable window.
//
// A missing window is the easy case. A window sitting at a bare shell is the
// one worth naming: the chat is its window's process, so a shell in its place
// means the chat is gone and attaching would land the user at a prompt rather
// than in the conversation.
func (s *Service) needsWindow(name string) bool {
	cmd, err := s.process.PaneCurrentCommand(name)
	if err != nil {
		return true // no window at all
	}
	return isShell(cmd)
}

// seedWorktree gives a fresh worktree what git does not carry: the
// repository's own skills, and the untracked config files the project names.
//
// One function because they are one idea — a worktree holds only tracked
// files, and the things it needs anyway have to be put there by whatever made
// it. Skills are the instance opentree already knew about; the seed list is the
// general case.
//
// Best-effort, in the shape of the other worktree conveniences: a workspace
// that came up without its skills or its .env is still a workspace, and failing
// creation over a symlink would trade a smaller problem for a larger one. Every
// mistake readable out of the config was already reported by checkPrerequisites
// before the worktree existed, and the Skills view reports what is missing — so
// a failure here is visible where it can be acted on rather than swallowed.
func (s *Service) seedWorktree(name string) {
	worktreePath := s.WorktreePath(name)
	_, _ = skills.Link(s.repoRoot, worktreePath)
	_, _ = bootstrap.Seed(s.repoRoot, worktreePath, s.cfg.Workspace.Seed)
}

// checkPrerequisites rejects a configuration no workspace could be created
// from, before anything is created.
//
// Both are the same kind of mistake: wrong for every workspace this repository
// will ever make, with no per-workspace recovery. Failing here is one clear
// message rather than a "✓ Launched" followed by a dead shell window, or a
// worktree that quietly never receives its .env.
func (s *Service) checkPrerequisites() error {
	if err := s.cfg.Agent.Validate(); err != nil {
		return err
	}
	return bootstrap.ValidateSeed(s.repoRoot, s.cfg.Workspace.Seed)
}

// Create creates a new workspace: git worktree, tmux window with agent, and state entry.
func (s *Service) Create(name, baseBranch string) (*state.Workspace, error) {
	if err := s.checkPrerequisites(); err != nil {
		return nil, err
	}

	if err := s.worktrees.Create(name, baseBranch); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w", err)
	}
	s.seedWorktree(name)

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

	// Update workspace with issue metadata. Set on both the stored record and
	// the struct being returned — the caller gets this one, not the store's.
	ws.IssueNumber = issue.Number
	ws.IssueTitle = issue.Title
	if err := s.state.Update(ws.Name, func(w *state.Workspace) error {
		w.IssueNumber = issue.Number
		w.IssueTitle = issue.Title
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to update workspace with issue metadata: %w", err)
	}

	return ws, nil
}

// CreateFromRemoteBranch creates a workspace from an existing remote branch.
// The branch is fetched from origin and checked out into a new worktree.
func (s *Service) CreateFromRemoteBranch(branchName string) (*state.Workspace, error) {
	if err := s.checkPrerequisites(); err != nil {
		return nil, err
	}

	createdBranch, err := s.worktrees.CreateFromRemote(branchName)
	if err != nil {
		return nil, fmt.Errorf("failed to create worktree from remote: %w", err)
	}
	s.seedWorktree(branchName)

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
		// The branch was already here and this workspace only checked it out.
		// Deleting the workspace must not take it with them.
		AdoptedBranch: !createdBranch,
	}
	if err := s.state.AddWorkspace(ws); err != nil {
		_ = s.process.KillWindow(branchName)
		_ = s.worktrees.Delete(branchName, createdBranch)
		return nil, fmt.Errorf("failed to save workspace state: %w", err)
	}

	return ws, nil
}

// deletesBranch reports whether deleting this workspace should take its branch
// with it.
//
// It should for a branch opentree created, which is every workspace made with
// `opentree new` and nothing else is left holding it. It should not for one
// this workspace adopted — the branch existed before, somebody else's work is
// on it, and `git branch -D` does not ask.
//
// A workspace opentree cannot find is treated as created, which is what every
// delete did before the distinction was recorded.
func (s *Service) deletesBranch(name string) bool {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return true
	}
	return !ws.AdoptedBranch
}

// Delete removes a workspace: removes worktree and branch, kills the tmux
// window, and deletes state. If this was the last workspace, the tmux
// session is also killed.
func (s *Service) Delete(name string) error {
	// Remove the worktree BEFORE killing the window: if removal fails
	// (locked worktree, cwd inside it, ...) the agent session survives.
	if err := s.worktrees.Delete(name, s.deletesBranch(name)); err != nil {
		return fmt.Errorf("failed to delete worktree: %w", err)
	}

	// Kill tmux window (ignore error if window doesn't exist), and the server's
	// window with it: the directory it was serving has just gone, and a dev
	// server left running against a deleted worktree holds its port and prints
	// stack traces at nobody.
	_ = s.process.KillWindow(name)
	_ = s.process.KillWindow(s.ServerWindow(name))

	if err := s.state.DeleteWorkspace(name); err != nil {
		return fmt.Errorf("failed to delete workspace state: %w", err)
	}

	// Clean up tmux session if no workspaces remain
	s.killSessionIfOurs()

	return nil
}

// DeleteFailure is one workspace a batch delete could not remove, and why.
type DeleteFailure struct {
	Name string
	Err  error
}

// DeleteBatchError reports a batch delete that only partly happened: which
// workspaces went, which stayed, and what stopped each one that stayed.
//
// Deleting a batch is not a transaction and cannot be made into one — each
// worktree is off the disk and its branch gone long before the next name is
// reached — so "it failed" is never the whole truth about a mixed batch. A
// caller told only that much either leaves rows on screen for workspaces that
// no longer exist, or clears rows for workspaces that are still there. Both
// lists are carried here so it does not have to guess, and so the message the
// user reads names the workspaces still waiting for them.
//
// The joined error this replaced carried the same facts as one flat string with
// the names buried inside it, which nothing could read back out.
type DeleteBatchError struct {
	// Deleted names the workspaces that went, in the order they were asked for.
	// Their state entries have been removed and their windows killed.
	Deleted []string
	// Failed names the workspaces that did not, each with its own reason.
	Failed []DeleteFailure
}

func (e *DeleteBatchError) Error() string {
	reasons := make([]string, 0, len(e.Failed))
	for _, f := range e.Failed {
		reasons = append(reasons, f.Name+": "+f.Err.Error())
	}
	msg := "could not delete " + strings.Join(reasons, "; ")
	if len(e.Deleted) > 0 {
		msg += " (deleted " + strings.Join(e.Deleted, ", ") + ")"
	}
	return msg
}

// Unwrap keeps errors.Is and errors.As working through the batch, the way they
// did when this was an errors.Join.
func (e *DeleteBatchError) Unwrap() []error {
	errs := make([]error, 0, len(e.Failed))
	for _, f := range e.Failed {
		errs = append(errs, f.Err)
	}
	return errs
}

// DeleteMultiple removes multiple workspaces in sequence. A failure on one
// workspace does not abandon the rest, and the rest are genuinely gone: their
// windows are killed and their state entries removed whatever happened to their
// neighbours. A mixed batch returns a *DeleteBatchError naming both halves.
func (s *Service) DeleteMultiple(names []string) error {
	batch := &DeleteBatchError{}
	for _, name := range names {
		if err := s.worktrees.Delete(name, s.deletesBranch(name)); err != nil {
			batch.Failed = append(batch.Failed, DeleteFailure{Name: name, Err: fmt.Errorf("failed to delete worktree: %w", err)})
			continue
		}
		_ = s.process.KillWindow(name)
		_ = s.process.KillWindow(s.ServerWindow(name))
		if err := s.state.DeleteWorkspace(name); err != nil {
			batch.Failed = append(batch.Failed, DeleteFailure{Name: name, Err: fmt.Errorf("failed to delete workspace state: %w", err)})
			continue
		}
		batch.Deleted = append(batch.Deleted, name)
	}

	s.killSessionIfOurs()

	// A typed nil in an error interface is not nil, and every caller here tests
	// the result against nil.
	if len(batch.Failed) == 0 {
		return nil
	}
	return batch
}

// HasChanges reports work that would be lost by deleting the workspace:
// commits ahead of the base branch, tracked modifications, and untracked files.
// Returns an empty string when the worktree is clean or absent from disk.
// A missing state entry does not skip the check — the worktree itself is inspected.
func (s *Service) HasChanges(name string) (string, error) {
	if _, err := os.Stat(s.WorktreePath(name)); err != nil {
		return "", nil //nolint:nilerr // no worktree on disk — nothing to lose
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

// isShell reports whether a pane's current command is an interactive shell
// rather than a running chat.
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
	_ = s.state.Update(name, func(w *state.Workspace) error {
		w.BranchPushed = true
		return nil
	})

	return prURL, nil
}

// PruneResult is what a prune reaped: the workspaces whose worktree had gone,
// and the server windows left behind with nothing to serve. Two lists rather
// than one, because they are two different things to tell someone about.
type PruneResult struct {
	Workspaces []string
	Servers    []string
}

// Prune removes state entries (and their tmux windows) for workspaces whose
// worktree directory no longer exists on disk, and clears git's stale
// worktree metadata. Branches are deliberately left intact.
func (s *Service) Prune() (PruneResult, error) {
	var result PruneResult
	if err := s.worktrees.Prune(); err != nil {
		return result, err
	}

	for _, ws := range s.state.ListWorkspaces() {
		dir := ws.WorktreeDir
		if dir == "" {
			dir = s.WorktreePath(ws.Name)
		}
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		_ = s.process.KillWindow(ws.Name)
		_ = s.process.KillWindow(s.ServerWindow(ws.Name))
		if err := s.state.DeleteWorkspace(ws.Name); err != nil {
			return result, fmt.Errorf("failed to prune %s: %w", ws.Name, err)
		}
		result.Workspaces = append(result.Workspaces, ws.Name)
	}
	result.Servers = s.pruneServerWindows()
	return result, nil
}

// pruneServerWindows kills run windows with no workspace behind them, and
// reports what it killed.
//
// The same category of mess prune already means: something opentree started
// that outlived the thing it belonged to. A server survives its worktree being
// removed by hand, and holds its port and a Node process while nothing on
// screen mentions it — the run window is not in the workspace list, so it is
// invisible until something else wants the port.
func (s *Service) pruneServerWindows() []string {
	windows, err := s.process.ListWindows()
	if err != nil {
		return nil
	}

	live := make(map[string]bool)
	for _, ws := range s.state.ListWorkspaces() {
		live[s.ServerWindow(ws.Name)] = true
	}

	var killed []string
	for _, w := range windows {
		if !strings.HasSuffix(w.Name, tmux.RunSuffix) || live[w.Name] {
			continue
		}
		// A run window belonging to another checkout looks exactly like an
		// orphan from here — same session, same :run suffix, and no workspace
		// in this repository's state to claim it. Its working directory is the
		// one thing that tells them apart.
		if !s.ownsWindow(w) {
			continue
		}
		if err := s.process.KillWindow(w.Name); err == nil {
			killed = append(killed, w.Name)
		}
	}
	return killed
}

// ownsWindow reports whether a window in the session belongs to this checkout.
//
// The session is named after the repository directory's base name, so two
// clones of the same project — a fork beside its upstream, a second copy for a
// long-running branch — share one. Everything that kills by name therefore has
// to ask this first, and a window whose working directory tmux did not report
// is treated as this repository's: that is what every version before the
// question existed assumed, and refusing to clean up is the worse failure.
func (s *Service) ownsWindow(w Window) bool {
	if w.Path == "" {
		return true
	}
	// Worktrees do not have to live inside the repository — base_dir may point
	// beside it — so both roots count.
	return under(s.repoRoot, w.Path) ||
		under(filepath.Join(s.repoRoot, s.cfg.Worktree.BaseDir), w.Path)
}

// under reports whether path is root or something inside it, comparing the
// paths as the filesystem resolves them: tmux reports a pane's directory
// resolved, while a repo root arrives as whatever the user typed.
func under(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || filepath.IsLocal(rel)
}

// killSessionIfOurs stops the tmux session once this repository has nothing
// left in it, and only if nothing else does either.
//
// Deleting the last workspace used to kill the session outright, which is
// right when the session is this repository's and wrong when it is shared —
// the other checkout's agents and dev servers went with it. Renaming the
// session would fix that and break more: the name is documented, and every
// session a user is currently attached to answers to the old one.
func (s *Service) killSessionIfOurs() {
	if len(s.state.ListWorkspaces()) != 0 {
		return
	}
	windows, err := s.process.ListWindows()
	if err != nil {
		return // cannot tell; a session left behind is the harmless outcome
	}
	for _, w := range windows {
		if !s.ownsWindow(w) {
			return
		}
	}
	_ = s.process.KillSession()
}
