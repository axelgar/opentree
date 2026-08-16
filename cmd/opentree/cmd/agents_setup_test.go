package cmd

import (
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

// `agents setup <agent>` now means "install this agent's ACP adapter", so the
// only agents it can be asked about are the ones in the registry.
func TestAgentsSetup_RejectsUnknownAgent(t *testing.T) {
	err := agentsSetupCmd.RunE(agentsSetupCmd, []string{"codex"})
	if err == nil {
		t.Fatal("expected an error for an agent outside the registry")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error = %q, want it to name the agent asked for", err)
	}
}

// An agent that serves ACP itself has nothing to fetch, and setup must be a
// no-op rather than an error — running it is how a user checks.
func TestAgentsSetup_SelfServingAgentNeedsNothing(t *testing.T) {
	agent := config.FindAgent("opencode")
	if agent == nil {
		t.Fatal("opencode missing from the registry")
	}
	if len(agent.ACPInstallCommand()) != 0 {
		t.Fatal("opencode should have nothing to install")
	}
	if err := setupACPAgent(agent); err != nil {
		t.Errorf("setupACPAgent(opencode) = %v, want nil", err)
	}
}

// The adapter install must land in opentree's own prefix. Without --prefix, npm
// -g writes into the user's global root, which opentree has no business owning.
func TestAgentsSetup_AdapterInstallsIntoOpentreesPrefix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	claude := config.FindAgent("claude")
	install := claude.ACPInstallCommand()
	if len(install) == 0 {
		t.Fatal("Claude Code is reached through an adapter and needs an install command")
	}
	joined := strings.Join(install, " ")
	if !strings.Contains(joined, "--prefix "+config.ToolsDir()) {
		t.Errorf("install = %q, want it prefixed into %s", joined, config.ToolsDir())
	}
}
