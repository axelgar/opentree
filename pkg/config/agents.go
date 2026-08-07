package config

import (
	"os/exec"
	"strings"
)

// PredefinedAgent describes a known coding agent that opentree can orchestrate.
type PredefinedAgent struct {
	Name        string   // display name: "Claude Code"
	Command     string   // binary: "claude"
	Args        []string // default args
	Description string   // short description for list display
	ACP         *ACPSpec // how to run it as an ACP server; nil means no ACP mode
}

// ACPSpec describes how to start an agent as an Agent Client Protocol server on
// stdio. Agents without one keep the plain launch path, where opentree types
// the command into a shell and the agent draws its own TUI.
type ACPSpec struct {
	// Command overrides the agent's binary when its ACP server is a separate
	// program. opencode serves ACP itself; Claude Code does not, and is reached
	// through an adapter binary of its own.
	Command string

	Args []string // subcommand and flags that select ACP mode

	// CwdFlag roots the session in a worktree. Optional: ACP already carries
	// cwd on session/new, so an adapter that takes no flags needs none, and
	// appending an empty flag would hand it a stray argument.
	CwdFlag string

	// Package is the npm package providing the ACP server, when it is a
	// separate program. Empty when the agent serves ACP itself.
	Package string

	// InstallSize is what the user is agreeing to download, stated because it
	// is large enough to matter on a slow connection.
	InstallSize string

	// AuthCommand logs the agent in interactively. ACP reports that
	// authentication is required but leaves the remedy to the agent, whose own
	// answer is "run this in a terminal" — which opentree happens to own.
	AuthCommand []string
}

// PredefinedAgents is the built-in registry of known agents.
var PredefinedAgents = []PredefinedAgent{
	{Name: "OpenCode", Command: "opencode", Description: "AI coding agent with TUI",
		ACP: &ACPSpec{Args: []string{"acp"}, CwdFlag: "--cwd", AuthCommand: []string{"auth", "login"}}},
	{Name: "Claude Code", Command: "claude", Description: "Anthropic's CLI coding agent",
		// Claude Code has no ACP mode of its own; claude-agent-acp bridges it,
		// reusing the same login. Install with
		// `npm i -g @agentclientprotocol/claude-agent-acp`.
		ACP: &ACPSpec{
			Command:     "claude-agent-acp",
			Package:     "@agentclientprotocol/claude-agent-acp",
			InstallSize: "303MB",
			AuthCommand: []string{"auth", "login"},
		}},
	{Name: "Codex", Command: "codex", Description: "OpenAI Codex CLI agent"},
	{Name: "GitHub Copilot", Command: "gh", Args: []string{"copilot"}, Description: "GitHub Copilot in the CLI"},
	{Name: "Gemini CLI", Command: "gemini", Description: "Google Gemini CLI agent"},
	{Name: "Pi", Command: "pi", Description: "Pi.dev CLI agent"},
}

// ACPCommand is the binary that serves ACP for this agent: its own, unless the
// spec names a separate adapter.
func (a PredefinedAgent) ACPCommand() string {
	if a.ACP == nil {
		return ""
	}
	if a.ACP.Command != "" {
		return a.ACP.Command
	}
	return a.Command
}

// ACPArgs is the full argument list for the ACP server, including the worktree
// when the agent wants it as a flag.
func (a PredefinedAgent) ACPArgs(worktree string) []string {
	if a.ACP == nil {
		return nil
	}
	args := append([]string{}, a.ACP.Args...)
	if a.ACP.CwdFlag != "" {
		args = append(args, a.ACP.CwdFlag, worktree)
	}
	return args
}

// FindAgent performs a case-insensitive lookup by Name or Command, falling back
// to a match on any single word of the Name (so "copilot" resolves to
// "GitHub Copilot"). Returns nil if no match is found.
func FindAgent(name string) *PredefinedAgent {
	lower := strings.ToLower(name)
	for i := range PredefinedAgents {
		if strings.ToLower(PredefinedAgents[i].Name) == lower ||
			strings.ToLower(PredefinedAgents[i].Command) == lower {
			return &PredefinedAgents[i]
		}
	}
	for i := range PredefinedAgents {
		for _, word := range strings.Fields(strings.ToLower(PredefinedAgents[i].Name)) {
			if word == lower {
				return &PredefinedAgents[i]
			}
		}
	}
	return nil
}

// FirstInstalledAgent returns the first predefined agent whose binary is on
// PATH, in registry order (opencode is the preferred pick). Nil if none is.
func FirstInstalledAgent() *PredefinedAgent {
	for i := range PredefinedAgents {
		if PredefinedAgents[i].IsInstalled() {
			return &PredefinedAgents[i]
		}
	}
	return nil
}

// knownAgentCommands returns the registry's commands as a comma-separated
// list for error messages.
func knownAgentCommands() string {
	cmds := make([]string, len(PredefinedAgents))
	for i, a := range PredefinedAgents {
		cmds[i] = a.Command
	}
	return strings.Join(cmds, ", ")
}

// AgentNames returns display names of all predefined agents.
func AgentNames() []string {
	names := make([]string, len(PredefinedAgents))
	for i, a := range PredefinedAgents {
		names[i] = a.Name
	}
	return names
}

// IsInstalled checks whether the agent's command binary is on PATH.
func (a PredefinedAgent) IsInstalled() bool {
	_, err := exec.LookPath(a.Command)
	return err == nil
}

// IsActive returns true if this agent matches the given config.
func (a PredefinedAgent) IsActive(cfg *Config) bool {
	return cfg.Agent.Command == a.Command
}
