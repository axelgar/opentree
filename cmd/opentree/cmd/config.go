package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
)

// configKey is one setting as the CLI sees it: how to describe it, how to read
// it back, where its value came from, and what `config set` may do with it.
//
// It is one table on purpose. This used to be four hand-kept registries — a
// name/description map, a list-valued map, a global-only map and a switch in
// getConfigValue — plus the help text and two print blocks in `config list`.
// They agreed only for as long as whoever added a key remembered all six, and
// nothing failed when they did not: workspace.setup and workspace.run were
// carried by config.WorkspaceConfig and approved by the trust gate, yet
// `config get workspace.setup` answered "unknown config key". A key that is
// added here is listed, gettable, settable, documented and completable by
// construction.
type configKey struct {
	name string
	desc string

	// get reads the resolved value in the spelling opentree.toml uses, so what
	// `config list` prints is what you would type back into the file.
	get func(*config.Config) string

	// source names the layer the value came from, for `config list`.
	source func(config.ConfigSource) string

	// listOf is what a list-valued setting holds — "paths", "commands" — and is
	// empty for the scalars. `config set` cannot write these: it takes one
	// string and would have to invent a splitting syntax, one nobody would
	// guess right first time, and that would quote wrongly the first time a
	// path contained a comma. They are read from here and edited in the file.
	listOf string

	// globalOnly marks the settings a repository may not carry. `config set`
	// refuses them without --global rather than writing a key that would be
	// stripped on the next read, which is a setting that saves, prints back,
	// and does nothing.
	globalOnly bool

	// parse converts the CLI string into the TOML type to write. Nil writes the
	// value as the string it arrived as.
	parse func(string) (any, error)
}

var configKeys = []configKey{
	{
		name:   "agent.command",
		desc:   "Command to run as the coding agent",
		get:    func(c *config.Config) string { return c.Agent.Command },
		source: func(s config.ConfigSource) string { return s.AgentCommand },
	},
	{
		name:   "worktree.base_dir",
		desc:   "Directory to store worktrees",
		get:    func(c *config.Config) string { return c.Worktree.BaseDir },
		source: func(s config.ConfigSource) string { return s.WorktreeBaseDir },
	},
	{
		name:   "worktree.default_base",
		desc:   "Default base branch for new workspaces",
		get:    func(c *config.Config) string { return c.Worktree.DefaultBase },
		source: func(s config.ConfigSource) string { return s.WorktreeDefaultBase },
	},
	{
		name:   "workspace.setup",
		desc:   "Commands that build what linking cannot copy",
		get:    func(c *config.Config) string { return formatList(c.Workspace.Setup) },
		source: func(s config.ConfigSource) string { return s.WorkspaceSetup },
		listOf: "commands",
	},
	{
		name:   "workspace.seed",
		desc:   "Untracked files to link into each new worktree",
		get:    func(c *config.Config) string { return formatList(c.Workspace.Seed) },
		source: func(s config.ConfigSource) string { return s.WorkspaceSeed },
		listOf: "paths",
	},
	{
		// Settable, unlike its two neighbours, only because it is one string —
		// there is nothing to split. Writing it does not approve it: the trust
		// gate hashes the exact text, so the next `opentree new` asks about the
		// new command, which is the right answer for a line that has not run
		// yet even though you typed it yourself.
		name:   "workspace.run",
		desc:   "Dev server to start on demand",
		get:    func(c *config.Config) string { return c.Workspace.Run },
		source: func(s config.ConfigSource) string { return s.WorkspaceRun },
	},
	{
		// The same one-string argument as workspace.run, and the same trust
		// consequence: writing it does not approve it — the gate hashes the
		// exact text, so autopilot asks about the new command before its first
		// run even though you typed it yourself.
		name:   "workspace.check",
		desc:   "Command autopilot runs after each agent turn to judge the work",
		get:    func(c *config.Config) string { return c.Workspace.Check },
		source: func(s config.ConfigSource) string { return s.WorkspaceCheck },
	},
	{
		name:   "tmux.session_prefix",
		desc:   "Prefix for tmux session names",
		get:    func(c *config.Config) string { return c.Tmux.SessionPrefix },
		source: func(s config.ConfigSource) string { return s.TmuxSessionPrefix },
	},
	{
		name: "github.auto_push",
		desc: "Auto-push branch before creating PR (true/false)",
		get: func(c *config.Config) string {
			return strconv.FormatBool(c.GitHub.AutoPush != nil && *c.GitHub.AutoPush)
		},
		source: func(s config.ConfigSource) string { return s.GitHubAutoPush },
		parse:  parseBoolValue,
	},
	{
		name:       "notify.on",
		desc:       "Events worth an interruption: blocked, done, stopped, pr_ready",
		get:        func(c *config.Config) string { return formatList(c.Notify.On) },
		source:     func(s config.ConfigSource) string { return s.NotifyOn },
		listOf:     "event names",
		globalOnly: true,
	},
	{
		name:       "notify.desktop",
		desc:       "OS banner as well as the tmux bell (true/false)",
		get:        func(c *config.Config) string { return strconv.FormatBool(c.Notify.Desktop == nil || *c.Notify.Desktop) },
		source:     func(s config.ConfigSource) string { return s.NotifyDesktop },
		globalOnly: true,
		parse:      parseBoolValue,
	},
}

// settable is whether `config set` can write this key at all — with --global
// for the global-only ones, but write it. The list-valued keys are the ones it
// never can, whatever flags you pass.
func (k configKey) settable() bool { return k.listOf == "" }

// help is the description plus whatever `config set` will refuse to do with it,
// so the answer to "why did that not work" sits in the same line that
// advertised the key.
func (k configKey) help() string {
	switch {
	case k.globalOnly && k.listOf != "":
		return k.desc + " — global only, edit the file"
	case k.globalOnly:
		return k.desc + " — global only"
	case k.listOf != "":
		return k.desc + " — edit the file"
	default:
		return k.desc
	}
}

// lookupConfigKey finds a key by name. The registry is a slice rather than a
// map because `config list` and the help text print in its order, and an order
// that shuffles between runs is one nobody can scan twice.
func lookupConfigKey(name string) (configKey, bool) {
	for _, k := range configKeys {
		if k.name == name {
			return k, true
		}
	}
	return configKey{}, false
}

func unknownConfigKey(key string) error {
	return fmt.Errorf("unknown config key %q\nRun 'opentree config list' to see available keys", key)
}

// configKeyHelp is the "Available keys" block, built from the registry so a key
// cannot be taught to the CLI and left out of the CLI's own documentation.
func configKeyHelp() string {
	width := 0
	for _, k := range configKeys {
		if len(k.name) > width {
			width = len(k.name)
		}
	}
	var b strings.Builder
	for _, k := range configKeys {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, k.name, k.help())
	}
	return strings.TrimRight(b.String(), "\n")
}

var ConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage opentree configuration",
	Long: `View and modify opentree configuration.

Configuration is loaded in order of precedence (highest wins):
  1. Repo config:   opentree.toml in the repository root
  2. Global config: ~/.config/opentree/opentree.toml
  3. Defaults:      built-in defaults

Use --global to read/write the global config instead of the repo config.

Available keys:
` + configKeyHelp() + `

notify.* is read from the global config only: how you like to be interrupted is
a property of you, not of a repository you cloned.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var configListGlobal bool

var configListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all configuration values",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if configListGlobal {
			cfg, err := config.LoadGlobal()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}
			// No source column here: there is only one file in play, and
			// naming it on every line would say the same word ten times.
			for _, k := range configKeys {
				fmt.Printf("%s = %s\n", k.name, k.get(cfg))
			}
			return nil
		}

		cfg, sources, err := config.LoadWithSources("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		for _, k := range configKeys {
			fmt.Printf("%s = %s  (%s)\n", k.name, k.get(cfg), k.source(sources))
		}
		// A value the repository asked for and did not get is worth a sentence.
		// Every line above it says where its value came from; this is the one
		// that came from nowhere on purpose, and without saying so the reader
		// is looking at "(default)" beside a file that plainly sets it.
		if rejected := sources.RejectedRepoBaseDir; rejected != "" {
			fmt.Printf("\nnote: this repository's opentree.toml asks for base_dir = %q, which is outside\n"+
				"      the repository, so it is ignored — a cloned repository does not get to point\n"+
				"      opentree at the rest of your filesystem. It is yours to set for yourself:\n"+
				"        opentree config set --global worktree.base_dir %s\n", rejected, rejected)
		}
		return nil
	},
}

var configGetGlobal bool

var configGetCmd = &cobra.Command{
	Use:               "get <key>",
	Short:             "Get a configuration value",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: configKeyCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		var cfg *config.Config
		var err error
		if configGetGlobal {
			cfg, err = config.LoadGlobal()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}
		} else {
			cfg, err = config.Load("")
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
		}

		val, err := getConfigValue(cfg, args[0])
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	},
}

var configSetGlobal bool

var configSetCmd = &cobra.Command{
	Use:               "set <key> <value>",
	Short:             "Set a configuration value",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: configSetKeyCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]

		k, ok := lookupConfigKey(key)
		if !ok {
			return unknownConfigKey(key)
		}

		// The list check comes first because it is the refusal with nowhere to
		// go: `config set notify.on blocked` used to say "run with --global",
		// and running it with --global then said "edit it in the file" — a
		// flag offered as the answer to a question it cannot answer.
		if k.listOf != "" {
			return fmt.Errorf("%s is a list of %s — edit it in %s", key, k.listOf, configFileFor(k))
		}
		if k.globalOnly && !configSetGlobal {
			return fmt.Errorf("%s is global only — run with --global (a cloned repository does not get to decide how you are interrupted)", key)
		}
		// Refused rather than written, for the same reason globalOnly exists: a
		// value that saves, prints back and does nothing is the worst of the
		// three outcomes. Only for the repository's file — the point is that
		// this is a setting you may have, in a place a clone cannot reach.
		if key == "worktree.base_dir" && !configSetGlobal && !filepath.IsLocal(value) {
			return fmt.Errorf("%s must stay inside the repository — a cloned repository does not get to point opentree at the rest of your filesystem\n\nIt is yours to set for yourself:\n  opentree config set --global %s %s", key, key, value)
		}

		parsed, err := parseConfigValue(key, value)
		if err != nil {
			return err
		}

		// Write only the one key into the raw target file: saving a merged
		// Config would freeze every default and global value into it.
		if configSetGlobal {
			path := config.GlobalConfigPath()
			if path == "" {
				return fmt.Errorf("could not determine global config path: home directory not found")
			}
			if err := config.SetKeys(path, map[string]any{key: parsed}); err != nil {
				return fmt.Errorf("failed to save global config: %w", err)
			}
			fmt.Printf("%s = %s  (global)\n", key, value)
			return nil
		}

		// The path is printed because there is more than one it could be, and
		// which one is not obvious from where you are standing: run from
		// inside a worktree this is the repository's file, not the branch's
		// checked-out copy sitting in the current directory.
		path := config.FindConfigFile()
		if err := config.SetKeys(path, map[string]any{key: parsed}); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("%s = %s  (%s)\n", key, value, path)
		return nil
	},
}

// configFileFor is the file a key is edited in: the global one for the keys a
// repository may not carry, and this repository's for everything else.
func configFileFor(k configKey) string {
	if k.globalOnly {
		return config.GlobalConfigPath()
	}
	return config.FindConfigFile()
}

// parseConfigValue converts a CLI string value into the TOML type for key.
func parseConfigValue(key, value string) (any, error) {
	k, ok := lookupConfigKey(key)
	if !ok {
		return nil, unknownConfigKey(key)
	}
	if k.parse == nil {
		return value, nil
	}
	parsed, err := k.parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid value for %s: %w", key, err)
	}
	return parsed, nil
}

// parseBoolValue's error names no key because parseConfigValue prefixes it with
// the one that was asked for, and a message carrying it twice reads as a
// stutter.
func parseBoolValue(value string) (any, error) {
	b, err := strconv.ParseBool(value)
	if err != nil {
		return nil, errors.New("must be true or false")
	}
	return b, nil
}

func getConfigValue(cfg *config.Config, key string) (string, error) {
	k, ok := lookupConfigKey(key)
	if !ok {
		return "", unknownConfigKey(key)
	}
	return k.get(cfg), nil
}

// formatList renders a list-valued setting the way the config file spells it,
// so what `config list` prints is what you would type back into opentree.toml.
func formatList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// configKeyCompletions offers every key, which is the whole registry: anything
// opentree.toml can hold can be read back.
func configKeyCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeConfigKeys(args, false)
}

// configSetKeyCompletions offers only what `set` can write. Completing a key
// the command is certain to refuse is the shell recommending a mistake, and
// the list-valued keys are refused every time, with or without --global.
func configSetKeyCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeConfigKeys(args, true)
}

func completeConfigKeys(args []string, settableOnly bool) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	keys := make([]string, 0, len(configKeys))
	for _, k := range configKeys {
		if settableOnly && !k.settable() {
			continue
		}
		keys = append(keys, fmt.Sprintf("%s\t%s", k.name, k.desc))
	}
	return keys, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	configListCmd.Flags().BoolVar(&configListGlobal, "global", false, "List values from the global config only")
	configGetCmd.Flags().BoolVar(&configGetGlobal, "global", false, "Get value from the global config")
	configSetCmd.Flags().BoolVar(&configSetGlobal, "global", false, "Set value in the global config (~/.config/opentree/opentree.toml)")

	ConfigCmd.AddCommand(configListCmd)
	ConfigCmd.AddCommand(configGetCmd)
	ConfigCmd.AddCommand(configSetCmd)
}
