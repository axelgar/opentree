package config

import (
	"os/exec"
	"strings"
)

// PredefinedAgent describes a known coding agent that opentree can orchestrate.
//
// The registry is deliberately short: opentree talks to agents over the Agent
// Client Protocol and nothing else, so an agent that does not serve ACP has no
// way in. Adding one back is a single entry here — everything downstream
// already assumes the protocol.
type PredefinedAgent struct {
	Name        string // display name: "Claude Code"
	Command     string // binary: "claude"
	Description string // short description for list display
	ACP         ACPSpec
	Skills      SkillsSpec

	// Colour and Mark are the agent's identity in a crowded line. A worktree
	// row has no space for a drawing, but it has space for one glyph, and a
	// colour tells the eye which agent it is before the name is read.
	//
	// The glyphs are deliberately plain geometry: anything from the emoji
	// range renders double-width in some terminals and single in others, which
	// shifts every column after it.
	Colour string
	Mark   string

	// Logo is the agent's mark drawn large, for the one screen with room for
	// it: the opening of a chat.
	Logo []string
}

// ACPSpec describes how to start an agent as an Agent Client Protocol server on
// stdio.
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

// SkillsSpec is where an agent reads its skills from. Skills are a filesystem
// convention rather than anything ACP models — every agent that has them looks
// for <dir>/<skill-name>/SKILL.md with the same YAML frontmatter — so opentree
// manages them by reading directories and needs no cooperation from the agent.
//
// The zero value means the agent has no skills concept, and it is left alone.
//
// Every field is a list because agents accept more than one spelling of the
// same tree, and because reading another agent's tree is normal: the same
// SKILL.md is frequently visible to several agents at once, which is why a
// skill records the agents that can see it rather than one owner.
type SkillsSpec struct {
	// UserDirs are the machine-wide trees, shared by every repository. A
	// leading "~/" is expanded at scan time so tests can point HOME elsewhere.
	// The first entry is the canonical one — where opentree puts a new skill.
	UserDirs []string

	// RepoDirs are the per-repository trees, relative to a repo or worktree
	// root. These are the ones worktrees miss when they are not committed.
	// The first entry is the canonical one.
	RepoDirs []string

	// ExternalDirs are trees this agent loads that another agent owns. opencode
	// reads Claude Code's global skills directly, so a skill installed once is
	// usable from both without being copied.
	ExternalDirs []string

	// SettingsFiles are the agent's settings documents in precedence order,
	// lowest first, each optionally holding a per-skill override map under
	// OverridesKey. A "~/" prefix is expanded against the home directory;
	// anything else is relative to the repository root.
	//
	// Installed is not the same as available: an agent can be told to ignore a
	// skill that is sitting right there on disk, and a list that cannot see
	// that is telling the user something untrue.
	SettingsFiles []string

	// OverridesKey is the object mapping skill name to state. Empty for an
	// agent with no way to switch a skill off.
	OverridesKey string

	// ConfigFiles are the agent's own configuration documents, in the order it
	// reads them, each optionally naming extra skills directories the user
	// registered. Same path rules as SettingsFiles.
	//
	// A user who keeps their skills somewhere else entirely is exactly the user
	// a list of hardcoded directories fails, and silently: opentree would show
	// a short list rather than an error.
	ConfigFiles []string
}

// PredefinedAgents is the built-in registry of known agents.
var PredefinedAgents = []PredefinedAgent{
	{Name: "OpenCode", Command: "opencode", Description: "AI coding agent with TUI",
		// opencode's own wordmark and its brand grey, transcribed from its
		// splash screen. The stray ▄ is the ascender on the d.
		Colour: "#CFCECD", Mark: "◆",
		Logo: []string{
			"                                 ▄",
			"█▀▀█ █▀▀█ █▀▀█ █▀▀▄ █▀▀▀ █▀▀█ █▀▀█ █▀▀█",
			"█  █ █  █ █▀▀▀ █  █ █    █  █ █  █ █▀▀▀",
			"▀▀▀▀ █▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀",
		},
		ACP: ACPSpec{Args: []string{"acp"}, CwdFlag: "--cwd", AuthCommand: []string{"auth", "login"}},
		// opencode spells its own trees "skill(s)" — both singular and plural are
		// read — and auto-loads two it does not own.
		//
		// It loads those two at *repository* scope as well, which its own
		// documentation does not say: the table in its embedded docs lists the
		// external trees under `~/` only. Established by asking a running
		// opencode what it had loaded from a directory holding nothing but
		// .claude/skills, which is the same check `v` performs in the tab.
		Skills: SkillsSpec{
			UserDirs: []string{"~/.config/opencode/skills", "~/.config/opencode/skill"},
			// Canonical first: it is where opentree puts a new skill. The rest
			// are read, not written to.
			RepoDirs:     []string{".opencode/skills", ".opencode/skill", ".claude/skills", ".agents/skills"},
			ExternalDirs: []string{"~/.claude/skills", "~/.agents/skills"},
			// opencode registers extra skills directories under "skills" in its
			// own config, global first and then the project's, and reads either
			// spelling of the file.
			ConfigFiles: []string{
				"~/.config/opencode/opencode.json",
				"~/.config/opencode/opencode.jsonc",
				"opencode.json",
				"opencode.jsonc",
			},
		}},
	{Name: "Claude Code", Command: "claude", Description: "Anthropic's CLI coding agent",
		// Claude Code's own mark, transcribed from its welcome banner, in
		// Anthropic's clay.
		Colour: "#D97757", Mark: "✻",
		Logo: []string{
			" ▐▛███▜▌",
			"▝▜█████▛▘",
			"  ▘▘ ▝▝",
		},
		// Claude Code has no ACP mode of its own; claude-agent-acp bridges it,
		// reusing the same login. Install with
		// `npm i -g @agentclientprotocol/claude-agent-acp`.
		ACP: ACPSpec{
			Command:     "claude-agent-acp",
			Package:     "@agentclientprotocol/claude-agent-acp",
			InstallSize: "303MB",
			AuthCommand: []string{"auth", "login"},
		},
		Skills: SkillsSpec{
			UserDirs: []string{"~/.claude/skills"},
			RepoDirs: []string{".claude/skills"},
			// The three sources `claude --setting-sources` names, in its own
			// precedence order.
			SettingsFiles: []string{
				"~/.claude/settings.json",
				".claude/settings.json",
				".claude/settings.local.json",
			},
			OverridesKey: "skillOverrides",
		}},
}

// Brand is the agent's mark and colour, resolved from whatever name a
// workspace recorded — which may be a command, a display name, or an agent
// opentree has never heard of. The fallback is deliberately unstyled rather
// than absent: an unknown agent should still be named in the list.
func Brand(name string) (mark, colour, display string) {
	if a := FindAgent(name); a != nil {
		return a.Mark, a.Colour, a.Name
	}
	return "·", "", name
}

// ACPCommand is the binary that serves ACP for this agent: its own, unless the
// spec names a separate adapter.
func (a PredefinedAgent) ACPCommand() string {
	if a.ACP.Command != "" {
		return a.ACP.Command
	}
	return a.Command
}

// ACPArgs is the full argument list for the ACP server, including the worktree
// when the agent wants it as a flag.
func (a PredefinedAgent) ACPArgs(worktree string) []string {
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

// AgentCommands returns the registry's commands, which is also the full list of
// agents opentree can drive.
func AgentCommands() []string {
	cmds := make([]string, len(PredefinedAgents))
	for i, a := range PredefinedAgents {
		cmds[i] = a.Command
	}
	return cmds
}

// knownAgentCommands is AgentCommands as one comma-separated line, for error
// messages.
func knownAgentCommands() string {
	return strings.Join(AgentCommands(), ", ")
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
