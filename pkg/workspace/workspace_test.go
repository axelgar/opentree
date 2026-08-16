package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/skills"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/worktree"
)

// mockProcessManager is a test double for ProcessManager that records calls
// and returns configurable results.
type mockProcessManager struct {
	createWindowCalls    []string
	createWindowEnvs     [][]string
	createWindowCommands []string
	createWindowArgs     [][]string
	createWindowErr      error
	appWindowCalls       []string // names passed to CreateAppWindow
	killWindowCalls      []string
	killSessionCalled    bool
	windows              []Window
	paneCommand          string // returned by PaneCurrentCommand; "" simulates "no window"
}

func (m *mockProcessManager) CreateAppWindow(name, workdir, command string, env []string, args ...string) error {
	m.appWindowCalls = append(m.appWindowCalls, name)
	m.createWindowCalls = append(m.createWindowCalls, name)
	m.createWindowEnvs = append(m.createWindowEnvs, env)
	m.createWindowCommands = append(m.createWindowCommands, command)
	m.createWindowArgs = append(m.createWindowArgs, args)
	return m.createWindowErr
}

func (m *mockProcessManager) ListWindows() ([]Window, error) { return m.windows, nil }
func (m *mockProcessManager) SelectWindow(name string) error { return nil }
func (m *mockProcessManager) AttachWindow(name string) error { return nil }
func (m *mockProcessManager) AttachCmd(name string) (*exec.Cmd, error) {
	return exec.Command("echo", "mock"), nil
}
func (m *mockProcessManager) KillWindow(name string) error {
	m.killWindowCalls = append(m.killWindowCalls, name)
	return nil
}
func (m *mockProcessManager) KillSession() error {
	m.killSessionCalled = true
	return nil
}
func (m *mockProcessManager) PaneCurrentCommand(name string) (string, error) {
	if m.paneCommand == "" {
		return "", errors.New("no tmux window")
	}
	return m.paneCommand, nil
}
func (m *mockProcessManager) GetWindowActivity(name string) (time.Time, error) {
	return time.Time{}, nil
}

// mockGitHubManager is a test double for GitHubManager.
type mockGitHubManager struct {
	fetchReviewsResult []github.ReviewComment
	fetchReviewsErr    error
	createPRResult     string
}

func (m *mockGitHubManager) IsInstalled() bool { return true }
func (m *mockGitHubManager) GetIssue(number int) (*github.Issue, error) {
	return nil, errors.New("not implemented")
}
func (m *mockGitHubManager) CreatePR(branch, baseBranch, title, body string) (string, error) {
	if m.createPRResult != "" {
		return m.createPRResult, nil
	}
	return "", errors.New("not implemented")
}
func (m *mockGitHubManager) FetchPRReviews(branch string) ([]github.ReviewComment, error) {
	return m.fetchReviewsResult, m.fetchReviewsErr
}

// newWithMockFull creates a Service with both a mock ProcessManager and a mock GitHubManager.
func newWithMockFull(repoRoot string, cfg *config.Config, pm ProcessManager, gh GitHubManager) (*Service, error) {
	wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
	st, err := state.New(repoRoot)
	if err != nil {
		return nil, err
	}
	return NewService(repoRoot, cfg, wt, pm, st, gh), nil
}

func TestWorktreePath(t *testing.T) {
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"
	svc := &Service{repoRoot: "/repo", cfg: cfg}

	tests := []struct {
		name string
		want string
	}{
		{"feature-auth", "/repo/.opentree/feature-auth"},
		{"feature/auth", "/repo/.opentree/feature-auth"},
		{"feat:thing", "/repo/.opentree/feat-thing"},
	}

	for _, tt := range tests {
		got := svc.WorktreePath(tt.name)
		if got != tt.want {
			t.Errorf("WorktreePath(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestWorktreePath_CustomBaseDir(t *testing.T) {
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = "worktrees"
	svc := &Service{repoRoot: "/home/user/project", cfg: cfg}

	got := svc.WorktreePath("my-branch")
	want := "/home/user/project/worktrees/my-branch"
	if got != want {
		t.Errorf("WorktreePath with custom BaseDir = %q, want %q", got, want)
	}
}

// isGitAvailable returns true when git is found on PATH.
func isGitAvailable() bool {
	return exec.Command("git", "--version").Run() == nil
}

// initGitRepo creates a temporary git repository and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	run("git", "init")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "config", "gpg.format", "openpgp")
	run("git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "init")
	// Ensure the default branch is called "main" for test consistency.
	run("git", "branch", "-M", "main")

	return dir
}

func TestCreateAndDelete(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	ws, err := svc.Create("test-branch", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if ws.Name != "test-branch" {
		t.Errorf("ws.Name = %q, want %q", ws.Name, "test-branch")
	}
	if ws.BaseBranch != "main" {
		t.Errorf("ws.BaseBranch = %q, want %q", ws.BaseBranch, "main")
	}
	if len(mock.createWindowCalls) != 1 || mock.createWindowCalls[0] != "test-branch" {
		t.Errorf("expected a window created for test-branch, got %v", mock.createWindowCalls)
	}

	worktreePath := svc.WorktreePath("test-branch")
	if !dirExists(worktreePath) {
		t.Error("worktree directory should exist after Create")
	}

	// Delete
	if err := svc.Delete("test-branch"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if dirExists(worktreePath) {
		t.Error("worktree directory should not exist after Delete")
	}

	workspaces := svc.state.ListWorkspaces()
	if len(workspaces) != 0 {
		t.Errorf("expected 0 workspaces after Delete, got %d", len(workspaces))
	}
	if !mock.killSessionCalled {
		t.Error("expected KillSession to be called when last workspace deleted")
	}
}

func TestDeleteMultiple(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	// Create two workspaces
	if _, err := svc.Create("branch-a", "main"); err != nil {
		t.Fatalf("Create branch-a: %v", err)
	}
	if _, err := svc.Create("branch-b", "main"); err != nil {
		t.Fatalf("Create branch-b: %v", err)
	}

	// Delete both
	if err := svc.DeleteMultiple([]string{"branch-a", "branch-b"}); err != nil {
		t.Fatalf("DeleteMultiple: %v", err)
	}

	if len(mock.killWindowCalls) != 2 {
		t.Errorf("expected 2 KillWindow calls, got %d", len(mock.killWindowCalls))
	}
	if !mock.killSessionCalled {
		t.Error("expected KillSession after deleting all workspaces")
	}
}

// newWithMock creates a Service with a mock ProcessManager for testing.
func newWithMock(repoRoot string, cfg *config.Config, pm ProcessManager) (*Service, error) {
	wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
	st, err := state.New(repoRoot)
	if err != nil {
		return nil, err
	}
	return NewService(repoRoot, cfg, wt, pm, st, nil), nil
}

// initRepoWithRemote creates a bare "origin" repo, clones it locally, and
// pushes branchName to origin. Returns the local clone directory.
func initRepoWithRemote(t *testing.T, branchName string) string {
	t.Helper()
	remoteDir := t.TempDir()
	localDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	runIn(remoteDir, "git", "init", "--bare")
	runIn(localDir, "git", "clone", remoteDir, ".")
	runIn(localDir, "git", "config", "user.email", "test@example.com")
	runIn(localDir, "git", "config", "user.name", "Test")
	runIn(localDir, "git", "config", "commit.gpgsign", "false")
	runIn(localDir, "git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "init")
	runIn(localDir, "git", "push", "origin", "HEAD:main")
	runIn(localDir, "git", "checkout", "-b", branchName)
	runIn(localDir, "git", "commit", "--allow-empty", "--no-gpg-sign", "-m", "feat commit")
	runIn(localDir, "git", "push", "origin", branchName)
	runIn(localDir, "git", "checkout", "main")
	return localDir
}

func TestCreate_CleansUpWorktreeOnWindowFailure(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{createWindowErr: errors.New("boom")}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	_, err = svc.Create("doomed", "main")
	if err == nil || !strings.Contains(err.Error(), "failed to create tmux window") {
		t.Fatalf("Create error = %v, want tmux window failure", err)
	}

	if _, err := os.Stat(svc.WorktreePath("doomed")); !os.IsNotExist(err) {
		t.Error("worktree directory should be removed after window failure")
	}
	out, err := exec.Command("git", "-C", repoDir, "branch", "--list", "doomed").Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("branch should be deleted after window failure, got %q", out)
	}
}

func TestCreateFromRemoteBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/remote-thing")
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(localDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	ws, err := svc.CreateFromRemoteBranch("feat/remote-thing")
	if err != nil {
		t.Fatalf("CreateFromRemoteBranch: %v", err)
	}

	if ws.Name != "feat/remote-thing" {
		t.Errorf("ws.Name = %q, want %q", ws.Name, "feat/remote-thing")
	}
	// Remote workspaces record the configured default base so diffs and the
	// delete-time lost-work check have a real base to compare against.
	if ws.BaseBranch != cfg.Worktree.DefaultBase {
		t.Errorf("ws.BaseBranch = %q, want %q", ws.BaseBranch, cfg.Worktree.DefaultBase)
	}
	if !ws.BranchPushed {
		t.Error("ws.BranchPushed should be true for a remote branch workspace")
	}
	if len(mock.createWindowCalls) != 1 || mock.createWindowCalls[0] != "feat/remote-thing" {
		t.Errorf("expected CreateWindow called with feat/remote-thing, got %v", mock.createWindowCalls)
	}

	worktreePath := svc.WorktreePath("feat/remote-thing")
	if !dirExists(worktreePath) {
		t.Error("worktree directory should exist after CreateFromRemoteBranch")
	}

}

func TestHasChanges_NoWorkspace(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive

	svc, err := New(repoDir, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// HasChanges on non-existent workspace should return empty string, no error
	diff, err := svc.HasChanges("nonexistent")
	if err != nil {
		t.Errorf("HasChanges on nonexistent: unexpected error: %v", err)
	}
	if diff != "" {
		t.Errorf("HasChanges on nonexistent: expected empty diff, got %q", diff)
	}
}

func TestCreatePR_AutoPushesBranch(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/seed")
	cfg := config.Default() // AutoPush defaults to true
	useAgent(t, cfg)        // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	ghMock := &mockGitHubManager{createPRResult: "https://github.com/acme/repo/pull/7"}
	svc, err := newWithMockFull(localDir, cfg, &mockProcessManager{}, ghMock)
	if err != nil {
		t.Fatalf("newWithMockFull: %v", err)
	}
	ws, err := svc.Create("feat/autopush", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	url, err := svc.CreatePR(ws.Name, "title", "body")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if url != ghMock.createPRResult {
		t.Errorf("CreatePR url = %q, want %q", url, ghMock.createPRResult)
	}

	lsCmd := exec.Command("git", "ls-remote", "--heads", "origin", "feat/autopush")
	lsCmd.Dir = localDir
	out, lsErr := lsCmd.CombinedOutput()
	if lsErr != nil {
		t.Fatalf("ls-remote: %v\n%s", lsErr, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("branch not pushed to origin by CreatePR with auto_push enabled")
	}

	// BranchPushed persisted.
	st, err := state.New(localDir)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	got, err := st.GetWorkspace(ws.Name)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if !got.BranchPushed {
		t.Error("BranchPushed not persisted after CreatePR")
	}
}

func TestCreatePR_NoAutoPushWhenDisabled(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	localDir := initRepoWithRemote(t, "feat/seed")
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"
	off := false
	cfg.GitHub.AutoPush = &off

	ghMock := &mockGitHubManager{createPRResult: "https://github.com/acme/repo/pull/8"}
	svc, err := newWithMockFull(localDir, cfg, &mockProcessManager{}, ghMock)
	if err != nil {
		t.Fatalf("newWithMockFull: %v", err)
	}
	ws, err := svc.Create("feat/nopush", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.CreatePR(ws.Name, "title", "body"); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	lsCmd := exec.Command("git", "ls-remote", "--heads", "origin", "feat/nopush")
	lsCmd.Dir = localDir
	out, lsErr := lsCmd.CombinedOutput()
	if lsErr != nil {
		t.Fatalf("ls-remote: %v\n%s", lsErr, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Error("branch was pushed despite auto_push = false")
	}
}

func TestWindowStatuses(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("active-ws", "main"); err != nil {
		t.Fatalf("Create active-ws: %v", err)
	}
	if _, err := svc.Create("idle-ws", "main"); err != nil {
		t.Fatalf("Create idle-ws: %v", err)
	}
	if _, err := svc.Create("gone-ws", "main"); err != nil {
		t.Fatalf("Create gone-ws: %v", err)
	}
	// A slashed branch: its window is keyed by the sanitized name ("feat-x").
	if _, err := svc.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create feat/x: %v", err)
	}

	mock.windows = []Window{
		{ID: "@1", Name: "active-ws", Active: true},
		{ID: "@2", Name: "idle-ws", Active: false},
		{ID: "@3", Name: "feat-x", Active: true},
	}

	statuses := svc.WindowStatuses()
	want := map[string]string{
		"active-ws": "active",
		"idle-ws":   "idle",
		"gone-ws":   "stopped",
		"feat/x":    "active", // matched via SanitizeBranchName fallback
	}
	for name, exp := range want {
		if statuses[name] != exp {
			t.Errorf("status[%q] = %q, want %q", name, statuses[name], exp)
		}
	}
}

func TestPrune_RemovesStaleEntries(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("keep-me", "main"); err != nil {
		t.Fatalf("Create keep-me: %v", err)
	}
	stale, err := svc.Create("stale-one", "main")
	if err != nil {
		t.Fatalf("Create stale-one: %v", err)
	}

	// Simulate an external `rm -rf` of the worktree.
	if err := os.RemoveAll(stale.WorktreeDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	pruned, err := svc.Prune()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "stale-one" {
		t.Errorf("Prune() = %v, want [stale-one]", pruned)
	}

	st, err := state.New(repoDir)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	if _, err := st.GetWorkspace("keep-me"); err != nil {
		t.Errorf("keep-me should survive prune: %v", err)
	}
	if _, err := st.GetWorkspace("stale-one"); err == nil {
		t.Error("stale-one should be pruned from state")
	}

	// git's stale worktree metadata is gone too.
	wt := worktree.New(repoDir, ".opentree")
	list, err := wt.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, w := range list {
		if strings.Contains(w.Path, "stale-one") {
			t.Errorf("git still lists pruned worktree: %s", w.Path)
		}
	}

	found := false
	for _, name := range mock.killWindowCalls {
		if name == "stale-one" {
			found = true
		}
	}
	if !found {
		t.Error("Prune did not kill the stale workspace's tmux window")
	}
}

func TestHasChanges_ReportsUntrackedFiles(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"

	svc, err := newWithMock(repoDir, cfg, &mockProcessManager{})
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	ws, err := svc.Create("guard-branch", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	diff, err := svc.HasChanges("guard-branch")
	if err != nil {
		t.Fatalf("HasChanges on clean worktree: %v", err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Errorf("HasChanges on clean worktree = %q, want empty", diff)
	}

	if err := os.WriteFile(filepath.Join(ws.WorktreeDir, "untracked.txt"), []byte("unsaved work"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	diff, err = svc.HasChanges("guard-branch")
	if err != nil {
		t.Fatalf("HasChanges with untracked file: %v", err)
	}
	if !strings.Contains(diff, "untracked.txt") {
		t.Errorf("HasChanges = %q, want mention of untracked.txt", diff)
	}
}

func TestNewService_NilFields(t *testing.T) {
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	svc := NewService("/repo", cfg, nil, nil, nil, nil)
	if svc.repoRoot != "/repo" {
		t.Errorf("repoRoot = %q, want %q", svc.repoRoot, "/repo")
	}
	if svc.cfg != cfg {
		t.Error("cfg not set correctly")
	}
}

func TestSanitizeBranchNameInPath(t *testing.T) {
	cfg := config.Default()
	useAgent(t, cfg) // Create validates the agent is one opentree can drive
	cfg.Worktree.BaseDir = ".opentree"
	svc := &Service{repoRoot: "/repo", cfg: cfg}

	// Verify that SanitizeBranchName is applied correctly
	path := svc.WorktreePath("feature/auth:v2")
	expected := filepath.Join("/repo", ".opentree", gitutil.SanitizeBranchName("feature/auth:v2"))
	if path != expected {
		t.Errorf("WorktreePath = %q, want %q", path, expected)
	}
}

func dirExists(path string) bool {
	info, err := exec.Command("test", "-d", path).CombinedOutput()
	_ = info
	return err == nil
}

func TestCreate_RejectsMissingAgentBinary(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	cfg := config.Default()
	cfg.Agent.Command = "definitely-not-a-real-binary-xyz"
	cfg.Worktree.BaseDir = ".opentree"

	mock := &mockProcessManager{}
	svc, err := newWithMockFull(repoDir, cfg, mock, &mockGitHubManager{})
	if err != nil {
		t.Fatalf("newWithMockFull: %v", err)
	}

	_, err = svc.Create("my-branch", "main")
	if err == nil {
		t.Fatal("expected error for missing agent binary")
	}
	if len(mock.createWindowCalls) != 0 {
		t.Error("no window should be created when the agent binary is missing")
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, ".opentree", "my-branch")); statErr == nil {
		t.Error("no worktree should be created when the agent binary is missing")
	}
}

// ---- ACP agent launch ----

// fakeBinary drops an executable stub named cmd into dir, so Create's agent
// validation passes without the real agent installed.
// useAgent points cfg at a registry agent and fakes its binary onto PATH.
// Create validates both, so a test that only wanted a workspace still needs an
// agent opentree could plausibly run.
func useAgent(t *testing.T, cfg *config.Config) {
	t.Helper()
	binDir := t.TempDir()
	fakeBinary(t, binDir, "opencode")
	// Prepended, not replaced: the worktree work still needs git.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg.Agent.Command = "opencode"
}

func fakeBinary(t *testing.T, dir, cmd string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, cmd), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestCreate_ACPAgentLaunchesTheChatView(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)

	binDir := t.TempDir()
	fakeBinary(t, binDir, "opencode")
	// Prepended, not replaced: the worktree work below still needs git.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.Agent.Command = "opencode" // has an ACP spec in the registry
	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	if _, err := svc.Create("acp-branch", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(mock.createWindowArgs) != 1 {
		t.Fatalf("CreateWindow calls = %d, want 1", len(mock.createWindowArgs))
	}
	// The window runs opentree's own chat, not the agent binary.
	if got := mock.createWindowCommands[0]; got == "opencode" {
		t.Errorf("command = %q, want the opentree binary rather than the agent", got)
	}
	// The agent is passed explicitly: the chat runs inside the worktree, whose
	// own opentree.toml may name a different agent than the launcher read.
	wantArgs := []string{"chat", "acp-branch", "--agent", "opencode"}
	if strings.Join(mock.createWindowArgs[0], " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", mock.createWindowArgs[0], wantArgs)
	}
	// Status comes over the control socket now, so the hook env is dead weight.
	if len(mock.createWindowEnvs[0]) != 0 {
		t.Errorf("env = %v, want none for an ACP agent", mock.createWindowEnvs[0])
	}
}

func TestEnsureWindow_ReopensAClosedWindow(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)

	binDir := t.TempDir()
	fakeBinary(t, binDir, "opencode")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.Agent.Command = "opencode"
	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("gone", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// paneCommand "" is the mock's way of saying the window is not there,
	// which is what quitting the chat leaves behind.
	mock.paneCommand = ""
	reopened, err := svc.EnsureWindow("gone")
	if err != nil {
		t.Fatalf("EnsureWindow: %v", err)
	}
	if !reopened {
		t.Fatal("expected the window to be reopened")
	}
	if len(mock.createWindowCalls) != 2 {
		t.Fatalf("CreateWindow calls = %d, want the create plus the reopen", len(mock.createWindowCalls))
	}
	// It must come back as the chat, not the bare agent.
	want := []string{"chat", "gone", "--agent", "opencode"}
	if strings.Join(mock.createWindowArgs[1], " ") != strings.Join(want, " ") {
		t.Errorf("reopen args = %v, want %v", mock.createWindowArgs[1], want)
	}
}

func TestEnsureWindow_LeavesALiveWindowAlone(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	mock := &mockProcessManager{paneCommand: "opentree"}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("alive", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reopened, err := svc.EnsureWindow("alive")
	if err != nil {
		t.Fatalf("EnsureWindow: %v", err)
	}
	if reopened {
		t.Error("a live window must not be relaunched")
	}
	if len(mock.createWindowCalls) != 1 {
		t.Errorf("CreateWindow calls = %d, want only the original create", len(mock.createWindowCalls))
	}
}

func TestEnsureWindow_UnknownWorkspace(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	cfg := config.Default()
	useAgent(t, cfg)
	svc, err := newWithMock(initGitRepo(t), cfg, &mockProcessManager{})
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.EnsureWindow("never-existed"); err == nil {
		t.Error("expected an error for a workspace that is not in state")
	}
}

func TestChatCommand_UsesTheRunningBinary(t *testing.T) {
	// Resolving from PATH would let a window launch a different opentree than
	// the one that created it.
	cmd, env, args := chatCommand("fix-auth", "opencode")
	if cmd == "opentree" {
		t.Error("fell back to PATH; os.Executable should have resolved")
	}
	if !filepath.IsAbs(cmd) {
		t.Errorf("command = %q, want an absolute path", cmd)
	}
	if env != nil {
		t.Errorf("env = %v, want none", env)
	}
	want := []string{"chat", "fix-auth", "--agent", "opencode"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", args, want)
	}
}

// The chat owns its window, so a shell sitting in its place means the chat is
// gone — attaching would drop the user at a prompt instead of the conversation.
func TestEnsureWindow_TreatsAShellInPlaceOfTheChatAsGone(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)

	binDir := t.TempDir()
	fakeBinary(t, binDir, "opencode")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.Agent.Command = "opencode"
	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("shelled", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	mock.paneCommand = "zsh"
	reopened, err := svc.EnsureWindow("shelled")
	if err != nil {
		t.Fatalf("EnsureWindow: %v", err)
	}
	if !reopened {
		t.Fatal("a window sitting at a shell was reported healthy")
	}
}

// A workspace recorded against an agent that has since left the registry cannot
// be reopened — opentree has no way to run it. Saying so beats opening a chat
// that dies on the handshake.
func TestAgentLaunch_RefusesAnAgentTheRegistryDoesNotKnow(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	svc, err := newWithMock(repoDir, cfg, &mockProcessManager{})
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	_, err = svc.agentLaunch("ws", "codex", svc.WorktreePath("ws"))
	if err == nil {
		t.Fatal("expected an error for an agent opentree cannot drive")
	}
	// The message has to name both the dead agent and the live ones, or the
	// only remedy is guesswork.
	for _, want := range []string{"codex", "opencode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// The reason the skills package exists: git carries only what it tracks, so a
// worktree made from a repository whose skills are untracked starts unable to
// see them, and the agent working there is quietly less capable than the same
// agent one directory up.
func TestCreate_LinksTheReposSkillsIntoTheWorktree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	cfg.Worktree.BaseDir = ".opentree"

	// An untracked project skill, which is how most repositories keep them.
	skillDir := filepath.Join(repoDir, ".claude", "skills", "release")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: release\ndescription: Ship it.\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	svc, err := newWithMock(repoDir, cfg, &mockProcessManager{})
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("feature", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(svc.WorktreePath("feature"), ".claude", "skills", "release", "SKILL.md"))
	if err != nil {
		t.Fatalf("the agent in the worktree cannot read the repo's skill: %v", err)
	}
	if !strings.Contains(string(got), "name: release") {
		t.Errorf("skill content = %q", got)
	}
	if missing := skills.Missing(repoDir, svc.WorktreePath("feature")); len(missing) != 0 {
		t.Errorf("Missing = %v after Create, want nothing", missing)
	}
}

// The general case of the same problem: the agent's first turn should not go on
// discovering that the worktree has no .env.
func TestCreate_SeedsTheConfiguredFilesIntoTheWorktree(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	cfg.Workspace.Seed = []string{".env"}

	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("TOKEN=hunter2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	svc, err := newWithMock(repoDir, cfg, &mockProcessManager{})
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("feature", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(svc.WorktreePath("feature"), ".env"))
	if err != nil {
		t.Fatalf("the agent in the worktree cannot read the repo's .env: %v", err)
	}
	if string(got) != "TOKEN=hunter2\n" {
		t.Errorf(".env content = %q, want the repository's own", got)
	}
}

// A seed path that leaves the repository is wrong for every workspace this
// repository will ever make, so it fails before one is created rather than
// leaving a worktree behind that quietly never receives its config.
func TestCreate_RefusesASeedPathOutsideTheRepository(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	cfg.Workspace.Seed = []string{"../../.ssh/id_rsa"}

	svc, err := newWithMock(repoDir, cfg, &mockProcessManager{})
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("feature", "main"); err == nil {
		t.Fatal("Create accepted a seed path outside the repository")
	}
	if _, err := os.Stat(svc.WorktreePath("feature")); err == nil {
		t.Error("a worktree was created despite the invalid config")
	}
}
