package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

func TestFanoutName(t *testing.T) {
	nothing := func(string) bool { return false }

	if got := fanoutName("feat/x", "claude", nothing); got != "feat/x-claude" {
		t.Errorf("name = %q, want feat/x-claude", got)
	}

	taken := map[string]bool{"feat/x-claude": true, "feat/x-claude-2": true}
	if got := fanoutName("feat/x", "claude", func(n string) bool { return taken[n] }); got != "feat/x-claude-3" {
		t.Errorf("name = %q, want the first free suffix", got)
	}
}

// fanoutService is a service over a fresh repo with every named agent faked
// onto PATH, the multi-binary variant of useAgent.
func fanoutService(t *testing.T, agents ...string) (*Service, *mockProcessManager) {
	t.Helper()
	repoDir := initGitRepo(t)

	binDir := t.TempDir()
	for _, a := range agents {
		fakeBinary(t, binDir, a)
	}
	// Prepended, not replaced: the worktree work still needs git.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.Agent.Command = agents[0]
	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	return svc, mock
}

func TestCreateFanout_CreatesOneSiblingPerAgent(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, mock := fanoutService(t, "opencode", "claude", "gemini")

	created, err := svc.CreateFanout("feat/x", "main", []string{"opencode", "claude", "gemini"})
	if err != nil {
		t.Fatalf("CreateFanout: %v", err)
	}
	if len(created) != 3 {
		t.Fatalf("created %d siblings, want 3", len(created))
	}

	wantNames := []string{"feat/x-opencode", "feat/x-claude", "feat/x-gemini"}
	for i, ws := range created {
		if ws.Name != wantNames[i] {
			t.Errorf("sibling %d = %q, want %q", i, ws.Name, wantNames[i])
		}
		if ws.FanoutGroup != "feat/x" {
			t.Errorf("sibling %q FanoutGroup = %q, want feat/x", ws.Name, ws.FanoutGroup)
		}
	}
	// Each window must run its own agent, not the configured default.
	for i, agent := range []string{"opencode", "claude", "gemini"} {
		args := strings.Join(mock.createWindowArgs[i], " ")
		want := "chat " + wantNames[i] + " --agent " + agent
		if args != want {
			t.Errorf("window %d args = %q, want %q", i, args, want)
		}
	}
	// And the group must be on disk, not only on the returned structs.
	for _, name := range wantNames {
		stored, err := svc.state.GetWorkspace(name)
		if err != nil {
			t.Fatalf("GetWorkspace(%q): %v", name, err)
		}
		if stored.FanoutGroup != "feat/x" {
			t.Errorf("stored %q FanoutGroup = %q, want feat/x", name, stored.FanoutGroup)
		}
	}
}

func TestCreateFanout_ValidatesEveryAgentBeforeCreatingAny(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	binDir := t.TempDir()
	fakeBinary(t, binDir, "opencode")

	cfg := config.Default()
	cfg.Agent.Command = "opencode"
	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}

	// PATH is replaced, not prepended: a machine with the real claude
	// installed must not quietly pass the check this test exists to fail.
	// Validation never shells out, so losing git here costs nothing.
	t.Setenv("PATH", binDir)

	_, err = svc.CreateFanout("feat/x", "main", []string{"opencode", "claude"})
	if err == nil {
		t.Fatal("expected an error when one agent is not installed")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("error %q should name the missing agent", err)
	}
	if len(mock.createWindowCalls) != 0 {
		t.Error("no window should be created when any agent is missing")
	}
	if len(svc.state.ListWorkspaces()) != 0 {
		t.Error("no workspace should be created when any agent is missing")
	}
}

func TestCreateFanout_RejectsUnknownAndDuplicateAgents(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, _ := fanoutService(t, "opencode", "claude")

	if _, err := svc.CreateFanout("feat/x", "main", []string{"opencode", "not-an-agent"}); err == nil {
		t.Error("expected an error for an unknown agent")
	}
	// Duplicates are checked after normalization: Claude and claude are one
	// agent wearing two spellings.
	_, err := svc.CreateFanout("feat/x", "main", []string{"claude", "Claude"})
	if err == nil {
		t.Error("expected an error for a duplicated agent")
	} else if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error %q should say the agent is duplicated", err)
	}
}

func TestCreateFanout_SidestepsSanitizedNameCollision(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, _ := fanoutService(t, "opencode", "claude")

	// feat-x-claude sanitizes to the same worktree directory, tmux window and
	// socket as feat/x-claude would; the sibling must step aside rather than
	// fight it for the directory.
	if _, err := svc.Create("feat-x-claude", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	created, err := svc.CreateFanout("feat/x", "main", []string{"claude"})
	if err != nil {
		t.Fatalf("CreateFanout: %v", err)
	}
	if created[0].Name != "feat/x-claude-2" {
		t.Errorf("sibling = %q, want feat/x-claude-2 (bumped past the sanitized collision)", created[0].Name)
	}
}

func TestCreateFanout_KeepsEarlierSiblingsOnFailure(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, mock := fanoutService(t, "opencode", "claude", "gemini")

	// Occupy the third sibling's worktree directory directly on disk, the
	// kind of mid-loop failure validation cannot see coming.
	if err := os.MkdirAll(filepath.Join(svc.repoRoot, ".opentree", "feat-x-gemini"), 0o755); err != nil {
		t.Fatal(err)
	}

	created, err := svc.CreateFanout("feat/x", "main", []string{"opencode", "claude", "gemini"})
	if err == nil {
		t.Fatal("expected an error when the third sibling cannot be created")
	}
	if len(created) != 2 {
		t.Fatalf("created = %d siblings, want the 2 from before the failure", len(created))
	}
	if !strings.Contains(err.Error(), "kept") {
		t.Errorf("error %q should say the earlier siblings are kept", err)
	}
	if got := len(svc.state.ListWorkspaces()); got != 2 {
		t.Errorf("state holds %d workspaces, want 2 — the failure must not roll back live siblings", got)
	}
	if len(mock.killWindowCalls) != 0 {
		t.Errorf("killed windows %v, want none — live siblings stay up", mock.killWindowCalls)
	}
}

// ---- promote ----

func TestPromote_DeletesLosersAndClearsWinner(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, mock := fanoutService(t, "opencode", "claude", "gemini")
	if _, err := svc.CreateFanout("feat/x", "main", []string{"opencode", "claude", "gemini"}); err != nil {
		t.Fatalf("CreateFanout: %v", err)
	}

	deleted, err := svc.Promote("feat/x-claude")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	wantDeleted := []string{"feat/x-gemini", "feat/x-opencode"}
	if strings.Join(deleted, " ") != strings.Join(wantDeleted, " ") {
		t.Errorf("deleted = %v, want %v", deleted, wantDeleted)
	}

	remaining := svc.state.ListWorkspaces()
	if len(remaining) != 1 || remaining[0].Name != "feat/x-claude" {
		t.Fatalf("remaining = %v, want only the winner", remaining)
	}
	if remaining[0].FanoutGroup != "" {
		t.Errorf("winner still wears group %q, want it cleared", remaining[0].FanoutGroup)
	}
	// The winner keeps its suffixed branch: no rename, ever.
	if remaining[0].Branch != "feat/x-claude" {
		t.Errorf("winner branch = %q, want feat/x-claude untouched", remaining[0].Branch)
	}
	// The losers' windows must go with them; the winner's must not.
	for _, name := range wantDeleted {
		if !contains(mock.killWindowCalls, name) {
			t.Errorf("window %q not killed; kills = %v", name, mock.killWindowCalls)
		}
	}
	if contains(mock.killWindowCalls, "feat/x-claude") {
		t.Errorf("winner's window was killed; kills = %v", mock.killWindowCalls)
	}
}

func TestPromote_RefusesNonGroupWorkspace(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, _ := fanoutService(t, "opencode")
	if _, err := svc.Create("loner", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Promote("loner"); err == nil || !strings.Contains(err.Error(), "not part of a fan-out group") {
		t.Errorf("err = %v, want the not-a-group refusal", err)
	}
	if _, err := svc.Promote("never-existed"); err == nil {
		t.Error("expected an error for an unknown workspace")
	}
}

// A group of one is what a crash between the deletes and the clear leaves
// behind; promoting again must finish the job rather than refuse it.
func TestPromote_GroupOfOneJustClears(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, _ := fanoutService(t, "opencode")
	if _, err := svc.CreateFanout("feat/x", "main", []string{"opencode"}); err != nil {
		t.Fatalf("CreateFanout: %v", err)
	}

	deleted, err := svc.Promote("feat/x-opencode")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted = %v, want none", deleted)
	}
	ws, err := svc.state.GetWorkspace("feat/x-opencode")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if ws.FanoutGroup != "" {
		t.Errorf("group = %q, want cleared", ws.FanoutGroup)
	}
}

func TestPromote_KeepsGroupOnPartialDeleteFailure(t *testing.T) {
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	svc, _ := fanoutService(t, "opencode", "claude", "gemini")
	if _, err := svc.CreateFanout("feat/x", "main", []string{"opencode", "claude", "gemini"}); err != nil {
		t.Fatalf("CreateFanout: %v", err)
	}

	// Make one loser undeletable: its worktree directory holds a different
	// checkout than its branch, the guard worktree.Delete refuses on.
	brokenDir := svc.WorktreePath("feat/x-gemini")
	if err := os.RemoveAll(brokenDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(brokenDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	deleted, err := svc.Promote("feat/x-claude")
	if err == nil {
		t.Fatal("expected the partial-delete error")
	}
	if !contains(deleted, "feat/x-opencode") {
		t.Errorf("deleted = %v, want the healthy loser gone", deleted)
	}

	// The winner keeps its mark so a second promote retries the straggler.
	ws, getErr := svc.state.GetWorkspace("feat/x-claude")
	if getErr != nil {
		t.Fatalf("GetWorkspace: %v", getErr)
	}
	if ws.FanoutGroup != "feat/x" {
		t.Errorf("winner group = %q, want feat/x kept for the retry", ws.FanoutGroup)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
