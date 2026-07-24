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
}

// PredefinedAgents is the built-in registry of known agents.
var PredefinedAgents = []PredefinedAgent{
	{Name: "OpenCode", Command: "opencode", Description: "AI coding agent with TUI"},
	{Name: "Claude Code", Command: "claude", Description: "Anthropic's CLI coding agent"},
	{Name: "Codex", Command: "codex", Description: "OpenAI Codex CLI agent"},
	{Name: "GitHub Copilot", Command: "gh", Args: []string{"copilot"}, Description: "GitHub Copilot in the CLI"},
	{Name: "Gemini CLI", Command: "gemini", Description: "Google Gemini CLI agent"},
	{Name: "Pi", Command: "pi", Description: "Pi.dev CLI agent"},
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
