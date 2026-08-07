package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// ---- ACP launch specs ----

func TestACPCommand(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		// opencode serves ACP itself.
		{"opencode", "opencode"},
		// Claude Code does not; a separate adapter binary does.
		{"claude", "claude-agent-acp"},
		// No ACP mode at all.
		{"pi", ""},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			a := FindAgent(tt.agent)
			if a == nil {
				t.Fatalf("FindAgent(%q) = nil", tt.agent)
			}
			if got := a.ACPCommand(); got != tt.want {
				t.Errorf("ACPCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestACPArgs(t *testing.T) {
	// An agent with a cwd flag gets the worktree; one without must not be
	// handed a stray empty flag and a bare path.
	if got := FindAgent("opencode").ACPArgs("/work/tree"); strings.Join(got, " ") != "acp --cwd /work/tree" {
		t.Errorf("opencode args = %v, want [acp --cwd /work/tree]", got)
	}
	if got := FindAgent("claude").ACPArgs("/work/tree"); len(got) != 0 {
		t.Errorf("claude args = %v, want none — the adapter takes no flags", got)
	}
	if got := FindAgent("pi").ACPArgs("/work/tree"); got != nil {
		t.Errorf("pi args = %v, want nil for an agent with no ACP mode", got)
	}
}

func TestACPSpec_AdapterIsInstallable(t *testing.T) {
	// An agent reached through an adapter can be installed while the adapter is
	// not, so the registry has to know how to fetch it.
	claude := FindAgent("claude")
	if claude.ACP.Package == "" {
		t.Fatal("Claude Code's adapter needs a package; without one it cannot be installed for the user")
	}
	if claude.ACP.InstallSize == "" {
		t.Error("state the download size — it is large enough that a user should agree to it knowingly")
	}

	got := claude.ACPInstallCommand()
	if len(got) == 0 {
		t.Fatal("expected an install command")
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--prefix") {
		t.Errorf("install = %q, want an explicit prefix so nothing lands in the global npm root", joined)
	}
	if !strings.Contains(joined, claude.ACP.Package) {
		t.Errorf("install = %q, want it to name the package", joined)
	}

	// opencode serves ACP itself, so there is nothing to fetch.
	if opencode := FindAgent("opencode"); opencode.ACPInstallCommand() != nil {
		t.Errorf("opencode install = %v, want none", opencode.ACPInstallCommand())
	}
	if FindAgent("pi").ACPInstallCommand() != nil {
		t.Error("an agent with no ACP mode has no adapter to install")
	}
}

func TestResolveACPCommand_PrefersOpentreesOwnCopy(t *testing.T) {
	// Someone who installed the adapter themselves should not get a second copy.
	home := t.TempDir()
	t.Setenv("HOME", home)

	claude := FindAgent("claude")
	if got := claude.ResolveACPCommand(); got != "claude-agent-acp" {
		t.Errorf("with nothing installed = %q, want the bare name for a PATH lookup", got)
	}

	managed := filepath.Join(home, ".opentree", "tools", "bin")
	if err := os.MkdirAll(managed, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "claude-agent-acp"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := claude.ResolveACPCommand(); got != filepath.Join(managed, "claude-agent-acp") {
		t.Errorf("with a managed copy = %q, want opentree's own", got)
	}
}
