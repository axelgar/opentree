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

// The registry is the list of agents opentree can drive, so every entry has to
// carry a way to start it over ACP. An entry without one would be offered in
// the picker and then fail to open a chat.
func TestEveryAgentServesACP(t *testing.T) {
	for _, a := range PredefinedAgents {
		if a.ACPCommand() == "" {
			t.Errorf("%s has no ACP command — opentree has no other way to run it", a.Name)
		}
		if a.Brand.Logo == nil {
			t.Errorf("%s has no logo; every agent opens a chat, and the chat draws one", a.Name)
		}
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

func TestBrand_ResolvesHoweverTheAgentWasRecorded(t *testing.T) {
	// A workspace stores whatever the user configured, which may be the binary
	// rather than the display name.
	mark, colour, display := Brand("claude")
	if display != "Claude Code" || mark == "" || colour == "" {
		t.Errorf("Brand(claude) = %q %q %q, want Claude Code fully branded", mark, colour, display)
	}

	mark, colour, display = Brand("aider")
	if display != "aider" || colour != "" {
		t.Errorf("Brand(aider) = %q %q %q, want the name back and no invented colour", mark, colour, display)
	}
	if mark == "" {
		t.Error("an unregistered agent still needs a mark, or its row loses a column")
	}
}

// Every agent needs a mark and a colour or the list is inconsistent.
func TestPredefinedAgents_AllBranded(t *testing.T) {
	for _, a := range PredefinedAgents {
		if a.Brand.Mark == "" || a.Brand.Colour == "" {
			t.Errorf("%s has no mark or colour", a.Name)
		}
	}
}

func TestACPEnv_NilWhenTheSpecAddsNothing(t *testing.T) {
	a := PredefinedAgent{Command: "opencode"}
	// nil rather than a copy of os.Environ(): exec.Cmd treats nil as "inherit",
	// which keeps tracking the parent instead of freezing a snapshot.
	if env := a.ACPEnv(); env != nil {
		t.Errorf("ACPEnv() = %d entries, want nil for an empty spec", len(env))
	}
}

func TestACPEnv_ExtendsTheCallersEnvironmentSorted(t *testing.T) {
	t.Setenv("OPENTREE_TEST_MARKER", "kept")
	a := PredefinedAgent{ACP: ACPSpec{Env: map[string]string{
		"ZZ_LAST": "z", "AA_FIRST": "a",
	}}}
	env := a.ACPEnv()
	if len(env) < 3 {
		t.Fatalf("ACPEnv() = %d entries, want the caller's environment plus two", len(env))
	}
	// The spec's variables land after the inherited ones, in sorted order, so
	// they win over an inherited duplicate and appear deterministically in logs.
	if env[len(env)-2] != "AA_FIRST=a" || env[len(env)-1] != "ZZ_LAST=z" {
		t.Errorf("tail = %v, want AA_FIRST=a then ZZ_LAST=z", env[len(env)-2:])
	}
	var kept bool
	for _, kv := range env {
		if kv == "OPENTREE_TEST_MARKER=kept" {
			kept = true
		}
	}
	if !kept {
		t.Error("the caller's own environment was dropped")
	}
}

func TestIsInstalled_AbsoluteACPCommandIsStatted(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agent-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := PredefinedAgent{Command: "no-such-command-on-path", ACP: ACPSpec{Command: bin}}
	if !a.IsInstalled() {
		t.Error("an existing absolute ACP command should count as installed without a PATH lookup")
	}

	// A deleted install must stop counting the moment it is gone — the record
	// naming the path is not the install.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if a.IsInstalled() {
		t.Error("a missing absolute ACP command still reported installed")
	}

	a.ACP.Command = dir
	if a.IsInstalled() {
		t.Error("a directory is not a launchable command")
	}
}

func TestResolveACPCommand_AbsolutePassesThrough(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	abs := filepath.Join(t.TempDir(), "tree", "bin", "agent")
	a := PredefinedAgent{Command: "reg-agent", ACP: ACPSpec{Command: abs}}
	if got := a.ResolveACPCommand(); got != abs {
		t.Errorf("ResolveACPCommand() = %q, want the absolute path back untouched", got)
	}
}

func TestACPInstalled_AbsolutePathMustExist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "agent")
	a := PredefinedAgent{Command: "reg-agent", ACP: ACPSpec{Command: bin}}
	if a.ACPInstalled() {
		t.Error("ACPInstalled() true for a path nothing ever wrote to")
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !a.ACPInstalled() {
		t.Error("ACPInstalled() false for an existing install")
	}
}
