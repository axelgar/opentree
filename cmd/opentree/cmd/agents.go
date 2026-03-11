package cmd

import (
	"fmt"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/spf13/cobra"
)

var AgentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Manage predefined coding agents",
	Long: `View and select from predefined coding agents.

Available agents: OpenCode, Claude Code, Codex, GitHub Copilot, Gemini CLI.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var agentsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all predefined agents",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			cfg = config.Default()
		}

		fmt.Printf("%-18s %-12s %-10s %s\n", "NAME", "COMMAND", "STATUS", "DESCRIPTION")
		fmt.Printf("%-18s %-12s %-10s %s\n", "----", "-------", "------", "-----------")

		for _, agent := range config.PredefinedAgents {
			status := "not found"
			if agent.IsInstalled() {
				status = "installed"
			}

			name := agent.Name
			if agent.IsActive(cfg) {
				name += " *"
			}

			cmdStr := agent.Command
			if len(agent.Args) > 0 {
				cmdStr += " " + strings.Join(agent.Args, " ")
			}

			fmt.Printf("%-18s %-12s %-10s %s\n", name, cmdStr, status, agent.Description)
		}
		return nil
	},
}

var agentsUseGlobal bool

var agentsUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active coding agent",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []string
		for _, a := range config.PredefinedAgents {
			completions = append(completions, fmt.Sprintf("%s\t%s", a.Name, a.Description))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := config.FindAgent(args[0])
		if agent == nil {
			fmt.Printf("Unknown agent %q. Available agents:\n", args[0])
			for _, a := range config.PredefinedAgents {
				fmt.Printf("  - %s (%s)\n", a.Name, a.Command)
			}
			return fmt.Errorf("agent %q not found", args[0])
		}

		if agentsUseGlobal {
			cfg, err := config.LoadGlobal()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}
			cfg.Agent.Command = agent.Command
			cfg.Agent.Args = agent.Args
			if cfg.Agent.Args == nil {
				cfg.Agent.Args = []string{}
			}
			if err := config.SaveGlobal(cfg); err != nil {
				return fmt.Errorf("failed to save global config: %w", err)
			}
			fmt.Printf("Agent set to %q (%s)  (global)\n", agent.Name, agent.Command)
			return nil
		}

		cfgPath := config.FindConfigFile()
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg.Agent.Command = agent.Command
		cfg.Agent.Args = agent.Args
		if cfg.Agent.Args == nil {
			cfg.Agent.Args = []string{}
		}
		if err := config.Save(cfg, cfgPath); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("Agent set to %q (%s)\n", agent.Name, agent.Command)
		return nil
	},
}

func init() {
	agentsUseCmd.Flags().BoolVar(&agentsUseGlobal, "global", false, "Set agent in the global config (~/.config/opentree/opentree.toml)")

	AgentsCmd.AddCommand(agentsListCmd)
	AgentsCmd.AddCommand(agentsUseCmd)
}
