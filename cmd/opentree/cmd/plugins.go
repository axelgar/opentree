package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/plugins"
)

var PluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Install Agent Plugins and hand what they bundle to every agent",
	Long: `Install Agent Plugins and hand what they bundle to every agent.

A plugin is the open Agent Plugins format (agent-plugins.org): a directory
with a plugin.json manifest, skills under skills/, and optionally an mcp.json
naming MCP servers. opentree installs one per machine, into its own store,
and every agent in every worktree can use the skills it bundles.

The MCP servers a plugin declares are listed and nothing more — opentree
neither launches them nor writes them into any agent's own configuration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var pluginsAddCmd = &cobra.Command{
	Use:   "add <git-url>",
	Short: "Install a plugin from a git repository",
	Long: `Install a plugin from a git repository.

The repository is cloned once for the whole machine, validated against the
Agent Plugins 1.0.0 specification, and kept under the name its own manifest
declares. A package that fails the manifest is refused whole — that is the
spec's rule, so a plugin that cannot state its own contract never gets any
component loaded — while a broken skill or server entry costs only itself
and is reported here.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := plugins.Install(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("✓ Installed %s\n", pluginTitle(p))
		for _, problem := range p.Problems {
			fmt.Printf("⚠ %s\n", problem)
		}
		fmt.Printf("  %s, %s\n", plural(len(p.Skills), "skill"), plural(len(p.Servers), "MCP server"))
		return nil
	},
}

var pluginsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List the installed plugins and what each declares",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		found := plugins.Installed()

		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			out, err := json.MarshalIndent(found, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal plugins: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		if len(found) == 0 {
			fmt.Println("No plugins installed.")
			return nil
		}

		fmt.Printf("%-24s %-10s %-7s %-4s %s\n", "NAME", "VERSION", "SKILLS", "MCP", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 100))
		for _, p := range found {
			fmt.Printf("%-24s %-10s %-7d %-4d %s\n",
				trim(p.Name, 24), trim(p.Version, 10), len(p.Skills), len(p.Servers), trim(p.Description, 46))
			// The counts say how much; these lines say what. An MCP server is
			// configuration somebody may later be asked to trust, so the listing
			// shows where each one would point — with env and header values
			// already masked by the loader, never printed here or anywhere.
			for _, s := range p.Servers {
				fmt.Printf("  mcp %s · %s · %s\n", s.Name, s.Type, serverTarget(s))
			}
			for _, problem := range p.Problems {
				fmt.Printf("  ⚠ %s\n", problem)
			}
		}
		return nil
	},
}

var pluginsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed plugin",
	Long: `Remove an installed plugin.

Deletes the plugin from the machine store. Skills the plugin provided stop
being offered everywhere at once, because every agent was reading the store's
copy rather than holding one of its own.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: pluginCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := plugins.Remove(args[0]); err != nil {
			return err
		}
		fmt.Printf("✓ Removed %s\n", args[0])
		return nil
	},
}

// pluginTitle names a plugin the way a person would say it: with its version
// when the manifest states one, bare when it does not.
func pluginTitle(p plugins.Plugin) string {
	if p.Version == "" {
		return p.Name
	}
	return p.Name + " " + p.Version
}

// serverTarget is where a server entry points — the command for a local one,
// the URL for a remote one. The one line of an entry that is safe and useful
// to show unmasked.
func serverTarget(s plugins.Server) string {
	if s.Type == "stdio" {
		return s.Command
	}
	return s.URL
}

// plural spells a count with its unit, because "1 skills" reads like a bug in
// a line whose whole job is reassurance.
func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// pluginCompletions offers the installed plugin names, each with its
// description, in the name\tdescription shape the shell renders as a menu.
func pluginCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, p := range plugins.Installed() {
		if p.Description == "" {
			out = append(out, p.Name)
			continue
		}
		out = append(out, p.Name+"\t"+p.Description)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	pluginsListCmd.Flags().BoolP("json", "j", false, "Output plugins as JSON")

	PluginsCmd.AddCommand(pluginsAddCmd)
	PluginsCmd.AddCommand(pluginsListCmd)
	PluginsCmd.AddCommand(pluginsRemoveCmd)
}
