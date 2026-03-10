package config

import "testing"

func TestFindAgent_ByName(t *testing.T) {
	agent := FindAgent("Claude Code")
	if agent == nil {
		t.Fatal("expected to find agent by name")
	}
	if agent.Command != "claude" {
		t.Errorf("Command = %q, want %q", agent.Command, "claude")
	}
}

func TestFindAgent_ByCommand(t *testing.T) {
	agent := FindAgent("opencode")
	if agent == nil {
		t.Fatal("expected to find agent by command")
	}
	if agent.Name != "OpenCode" {
		t.Errorf("Name = %q, want %q", agent.Name, "OpenCode")
	}
}

func TestFindAgent_CaseInsensitive(t *testing.T) {
	agent := FindAgent("CLAUDE CODE")
	if agent == nil {
		t.Fatal("expected case-insensitive match")
	}
	if agent.Command != "claude" {
		t.Errorf("Command = %q, want %q", agent.Command, "claude")
	}
}

func TestFindAgent_NotFound(t *testing.T) {
	agent := FindAgent("nonexistent")
	if agent != nil {
		t.Errorf("expected nil for unknown agent, got %+v", agent)
	}
}

func TestAgentNames(t *testing.T) {
	names := AgentNames()
	if len(names) != len(PredefinedAgents) {
		t.Errorf("AgentNames() returned %d names, want %d", len(names), len(PredefinedAgents))
	}
	if names[0] != "OpenCode" {
		t.Errorf("first name = %q, want %q", names[0], "OpenCode")
	}
}

func TestGitHubCopilot_HasArgs(t *testing.T) {
	agent := FindAgent("GitHub Copilot")
	if agent == nil {
		t.Fatal("expected to find GitHub Copilot")
	}
	if agent.Command != "gh" {
		t.Errorf("Command = %q, want %q", agent.Command, "gh")
	}
	if len(agent.Args) != 1 || agent.Args[0] != "copilot" {
		t.Errorf("Args = %v, want [copilot]", agent.Args)
	}
}

func TestIsActive(t *testing.T) {
	cfg := Default()
	agent := FindAgent("OpenCode")
	if agent == nil {
		t.Fatal("expected to find OpenCode")
	}
	if !agent.IsActive(cfg) {
		t.Error("expected OpenCode to be active with default config")
	}

	claude := FindAgent("Claude Code")
	if claude.IsActive(cfg) {
		t.Error("expected Claude Code to not be active with default config")
	}
}
