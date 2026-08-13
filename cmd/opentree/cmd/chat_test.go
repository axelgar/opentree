package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/state"
)

// A session id is the agent's own bookkeeping, so a workspace opened with a
// second agent must not be handed the first one's conversation: the load fails
// and the chat reports a conversation that could not be resumed, when the
// truthful answer is that this agent has not had one here yet.
func TestResumableSession_OnlyTheAgentThatMadeIt(t *testing.T) {
	ws := &state.Workspace{
		ACPSessionID: "s1",
		ACPSessions:  []state.ACPSession{{Agent: "opencode", ID: "s1"}},
	}
	if got := resumableSession(ws, "copilot"); got != "" {
		t.Errorf("resumableSession(copilot) = %q, want none — that conversation is OpenCode's", got)
	}
	if got := resumableSession(ws, "opencode"); got != "s1" {
		t.Errorf("resumableSession(opencode) = %q, want s1", got)
	}
	if got := knownSessions(ws, "copilot"); len(got) != 0 {
		t.Errorf("knownSessions(copilot) = %v, want none to resume", got)
	}

	// An id recorded before opentree tracked which agent made it belongs to
	// whichever agent the workspace was already using.
	old := &state.Workspace{ACPSessionID: "s0"}
	if got := resumableSession(old, "gemini"); got != "s0" {
		t.Errorf("resumableSession on a pre-ledger workspace = %q, want s0", got)
	}
}

// resolveACPAgent decides which agent a chat window runs. It is the seam where
// the launcher's decision and the chat's own lookup used to disagree.

func writeConfig(t *testing.T, dir, agentCommand string) {
	t.Helper()
	body := "[agent]\ncommand = '" + agentCommand + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "opentree.toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveACPAgent_FlagWins(t *testing.T) {
	// The regression: the launcher opens a window for opencode, but the
	// worktree's own checked-out opentree.toml still says claude. Without the
	// flag the chat refuses to start, having re-decided from the wrong file.
	repo := t.TempDir()
	writeConfig(t, repo, "claude")

	agentFlag = "opencode"
	t.Cleanup(func() { agentFlag = "" })

	agent, err := resolveACPAgent(repo)
	if err != nil {
		t.Fatalf("resolveACPAgent: %v", err)
	}
	if agent.Command != "opencode" {
		t.Errorf("agent = %q, want the one the launcher chose", agent.Command)
	}
}

func TestResolveACPAgent_FallsBackToRepoConfig(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, "opencode")

	agentFlag = ""
	agent, err := resolveACPAgent(repo)
	if err != nil {
		t.Fatalf("resolveACPAgent: %v", err)
	}
	if agent.Command != "opencode" {
		t.Errorf("agent = %q, want opencode", agent.Command)
	}
}

func TestResolveACPAgent_RejectsNonACPAgent(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, "pi") // dropped from the registry — no ACP mode

	agentFlag = ""
	_, err := resolveACPAgent(repo)
	if err == nil {
		t.Fatal("expected an error for an agent with no ACP mode")
	}
	if !strings.Contains(err.Error(), "pi") || !strings.Contains(err.Error(), "opencode") {
		t.Errorf("error = %q, want it to name the rejected agent and the ones that work", err)
	}
}

// A missing adapter is deliberately not resolved as an error: the chat opens in
// its stopped state instead, where installing it is one key away. Failing here
// would turn a one-keystroke fix into a command that refuses to run.
func TestResolveACPAgent_MissingAdapterIsNotAnError(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, "claude")
	t.Setenv("PATH", t.TempDir()) // no adapter anywhere
	t.Setenv("HOME", t.TempDir()) // ...and none in opentree's own prefix either

	agentFlag = ""
	agent, err := resolveACPAgent(repo)
	if err != nil {
		t.Fatalf("resolveACPAgent = %v, want the agent back so the chat can offer to install it", err)
	}
	if agent.ACPInstalled() {
		t.Fatal("expected the adapter to be missing in this environment")
	}
	if len(agent.ACPInstallCommand()) == 0 {
		t.Error("the chat needs a way to install the adapter it just found missing")
	}
}

func TestResolveACPAgent_RejectsUnknownAgent(t *testing.T) {
	agentFlag = "not-a-real-agent"
	t.Cleanup(func() { agentFlag = "" })

	if _, err := resolveACPAgent(t.TempDir()); err == nil {
		t.Fatal("expected an error for an agent that is not in the registry")
	}
}

func TestAgentCommands(t *testing.T) {
	got := config.AgentCommands()
	for _, want := range []string{"opencode", "claude", "copilot", "gemini"} {
		if !slices.Contains(got, want) {
			t.Errorf("AgentCommands() = %q, want it to list %q", got, want)
		}
	}
	// The registry is the list of drivable agents now, so a name that left it
	// must not still be advertised as one. Matched whole rather than as a
	// substring: "pi" is inside "copilot".
	for _, gone := range []string{"codex", "pi"} {
		if slices.Contains(got, gone) {
			t.Errorf("AgentCommands() = %q, still lists %q, which has no ACP mode", got, gone)
		}
	}
}
