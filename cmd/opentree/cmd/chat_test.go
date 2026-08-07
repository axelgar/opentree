package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	writeConfig(t, repo, "pi") // no ACP mode in the registry

	agentFlag = ""
	_, err := resolveACPAgent(repo)
	if err == nil {
		t.Fatal("expected an error for an agent with no ACP mode")
	}
	if !strings.Contains(err.Error(), "pi") || !strings.Contains(err.Error(), "opencode") {
		t.Errorf("error = %q, want it to name the rejected agent and the ones that work", err)
	}
}

func TestResolveACPAgent_MissingAdapterExplainsItself(t *testing.T) {
	// Claude Code is reached through a separate adapter binary, so the agent
	// can be installed while the thing that serves ACP is not. "executable file
	// not found in $PATH" would be true and useless.
	repo := t.TempDir()
	writeConfig(t, repo, "claude")
	t.Setenv("PATH", t.TempDir()) // no adapter anywhere

	agentFlag = ""
	_, err := resolveACPAgent(repo)
	if err == nil {
		t.Skip("claude-agent-acp is installed here, so there is nothing to miss")
	}
	for _, want := range []string{"claude-agent-acp", "npm i -g"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestResolveACPAgent_RejectsUnknownAgent(t *testing.T) {
	agentFlag = "not-a-real-agent"
	t.Cleanup(func() { agentFlag = "" })

	if _, err := resolveACPAgent(t.TempDir()); err == nil {
		t.Fatal("expected an error for an agent that is not in the registry")
	}
}

func TestACPCapableAgents(t *testing.T) {
	got := acpCapableAgents()
	if !strings.Contains(got, "opencode") {
		t.Errorf("acpCapableAgents() = %q, want it to list opencode", got)
	}
	if !strings.Contains(got, "claude") {
		t.Errorf("acpCapableAgents() = %q, want claude listed now that it has an adapter", got)
	}
	if strings.Contains(got, "pi") {
		t.Errorf("acpCapableAgents() = %q, should not list agents without an ACP spec", got)
	}
}
