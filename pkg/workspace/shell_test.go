package workspace

import (
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

func shellService(t *testing.T) (*Service, *mockProcessManager) {
	t.Helper()
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("feat/x", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return svc, mock
}

// One shell per workspace: the second request finds the first, half-typed
// command and all, rather than opening a second window beside it.
func TestOpenShell_OpensOnceInTheWorktreeAndThenReuses(t *testing.T) {
	svc, mock := shellService(t)

	created, err := svc.OpenShell("feat/x")
	if err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	if !created {
		t.Error("the first OpenShell reported reusing a window that did not exist")
	}
	if len(mock.shellWindowCalls) != 1 || mock.shellWindowCalls[0] != "feat-x:sh" {
		t.Fatalf("shell windows created = %v, want [feat-x:sh]", mock.shellWindowCalls)
	}
	if got := mock.windows[len(mock.windows)-1].Path; got != svc.WorktreePath("feat/x") {
		t.Errorf("the shell opened in %q, want the worktree %q", got, svc.WorktreePath("feat/x"))
	}

	created, err = svc.OpenShell("feat/x")
	if err != nil {
		t.Fatalf("second OpenShell: %v", err)
	}
	if created || len(mock.shellWindowCalls) != 1 {
		t.Errorf("a second OpenShell made another window: created=%v, calls=%v", created, mock.shellWindowCalls)
	}
}

func TestOpenShell_RefusesAWorkspaceThatDoesNotExist(t *testing.T) {
	svc, mock := shellService(t)
	if _, err := svc.OpenShell("feat/nope"); err == nil {
		t.Fatal("OpenShell opened a shell for a workspace that does not exist")
	}
	if len(mock.shellWindowCalls) != 0 {
		t.Errorf("a window was made for it anyway: %v", mock.shellWindowCalls)
	}
}

// Deleting the workspace takes its shell with it: a prompt whose directory
// has gone fails every command typed into it.
func TestDelete_KillsTheShellWindow(t *testing.T) {
	svc, mock := shellService(t)
	if _, err := svc.OpenShell("feat/x"); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	if err := svc.Delete("feat/x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !contains(mock.killWindowCalls, "feat-x:sh") {
		t.Errorf("killed %v, want the shell window among them", mock.killWindowCalls)
	}
}
