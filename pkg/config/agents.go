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
	ConfigDir   string   // informational, for future use
}

// PredefinedAgents is the built-in registry of known agents.
var PredefinedAgents = []PredefinedAgent{
	{Name: "OpenCode", Command: "opencode", Description: "AI coding agent with TUI", ConfigDir: "~/.opencode"},
	{Name: "Claude Code", Command: "claude", Description: "Anthropic's CLI coding agent", ConfigDir: "~/.claude"},
	{Name: "Codex", Command: "codex", Description: "OpenAI Codex CLI agent", ConfigDir: ""},
	{Name: "GitHub Copilot", Command: "gh", Args: []string{"copilot"}, Description: "GitHub Copilot in the CLI", ConfigDir: ""},
	{Name: "Gemini CLI", Command: "gemini", Description: "Google Gemini CLI agent", ConfigDir: "~/.gemini"},
}

// FindAgent performs a case-insensitive lookup by Name or Command.
// Returns nil if no match is found.
func FindAgent(name string) *PredefinedAgent {
	lower := strings.ToLower(name)
	for i := range PredefinedAgents {
		if strings.ToLower(PredefinedAgents[i].Name) == lower ||
			strings.ToLower(PredefinedAgents[i].Command) == lower {
			return &PredefinedAgents[i]
		}
	}
	return nil
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
