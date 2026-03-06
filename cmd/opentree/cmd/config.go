package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/spf13/cobra"
)

// configKeys maps dot-notation keys to their description and section.
var configKeys = map[string]string{
	"agent.command":        "Command to run as the coding agent",
	"agent.args":           "Extra arguments passed to the agent (comma-separated)",
	"worktree.base_dir":    "Directory to store worktrees",
	"worktree.default_base": "Default base branch for new workspaces",
	"tmux.session_prefix":  "Prefix for tmux session names",
	"github.auto_push":     "Auto-push branch before creating PR (true/false)",
}

var ConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage opentree configuration",
	Long: `View and modify opentree configuration.

Available keys:
  agent.command          Command to run as the coding agent
  agent.args             Extra arguments passed to the agent (comma-separated)
  worktree.base_dir      Directory to store worktrees
  worktree.default_base  Default base branch for new workspaces
  tmux.session_prefix    Prefix for tmux session names
  github.auto_push       Auto-push branch before creating PR (true/false)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configuration values",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		fmt.Printf("agent.command = %s\n", cfg.Agent.Command)
		fmt.Printf("agent.args = %s\n", strings.Join(cfg.Agent.Args, ","))
		fmt.Printf("worktree.base_dir = %s\n", cfg.Worktree.BaseDir)
		fmt.Printf("worktree.default_base = %s\n", cfg.Worktree.DefaultBase)
		fmt.Printf("tmux.session_prefix = %s\n", cfg.Tmux.SessionPrefix)
		fmt.Printf("github.auto_push = %t\n", cfg.GitHub.AutoPush)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a configuration value",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: configKeyCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		val, err := getConfigValue(cfg, args[0])
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	ValidArgsFunction: configKeyCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]

		if _, ok := configKeys[key]; !ok {
			return fmt.Errorf("unknown config key %q\nRun 'opentree config list' to see available keys", key)
		}

		cfgPath := config.FindConfigFile()
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if err := setConfigValue(cfg, key, value); err != nil {
			return err
		}

		if err := config.Save(cfg, cfgPath); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("%s = %s\n", key, value)
		return nil
	},
}

func getConfigValue(cfg *config.Config, key string) (string, error) {
	switch key {
	case "agent.command":
		return cfg.Agent.Command, nil
	case "agent.args":
		return strings.Join(cfg.Agent.Args, ","), nil
	case "worktree.base_dir":
		return cfg.Worktree.BaseDir, nil
	case "worktree.default_base":
		return cfg.Worktree.DefaultBase, nil
	case "tmux.session_prefix":
		return cfg.Tmux.SessionPrefix, nil
	case "github.auto_push":
		return strconv.FormatBool(cfg.GitHub.AutoPush), nil
	default:
		return "", fmt.Errorf("unknown config key %q\nRun 'opentree config list' to see available keys", key)
	}
}

func setConfigValue(cfg *config.Config, key, value string) error {
	switch key {
	case "agent.command":
		cfg.Agent.Command = value
	case "agent.args":
		if value == "" {
			cfg.Agent.Args = []string{}
		} else {
			cfg.Agent.Args = strings.Split(value, ",")
		}
	case "worktree.base_dir":
		cfg.Worktree.BaseDir = value
	case "worktree.default_base":
		cfg.Worktree.DefaultBase = value
	case "tmux.session_prefix":
		cfg.Tmux.SessionPrefix = value
	case "github.auto_push":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid value for github.auto_push: must be true or false")
		}
		cfg.GitHub.AutoPush = b
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func configKeyCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	keys := make([]string, 0, len(configKeys))
	for k, desc := range configKeys {
		keys = append(keys, fmt.Sprintf("%s\t%s", k, desc))
	}
	return keys, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	ConfigCmd.AddCommand(configListCmd)
	ConfigCmd.AddCommand(configGetCmd)
	ConfigCmd.AddCommand(configSetCmd)
}
