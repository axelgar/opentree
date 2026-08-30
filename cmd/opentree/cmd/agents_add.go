package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/registry"
)

var agentsAddYes bool

var agentsAddCmd = &cobra.Command{
	Use:   "add <id>",
	Short: "Install an agent from the ACP Registry",
	Long: `Install an agent the ACP Registry lists, into ~/.opentree/registry.

Installing executes code, so nothing is fetched before you have seen exactly
what will run: the pinned package and the command, or the archive URL and its
checksum. Once installed, the agent is first-class — 'agents use', the
dashboard picker, --agents fan-outs and per-workspace overrides all take it
by its registry id.

  opentree agents search        what the registry has
  opentree agents add --yes     answer the confirmation from a script`,
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: registryAddCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if err := registry.ValidateID(id); err != nil {
			return err
		}

		// Resolved before anything is fetched: both answers below make the
		// network round trip pointless, and one of them is the collision rule
		// — the built-in four stay canonical under their own names.
		if agent := config.FindAgent(id); agent != nil {
			if agent.Origin != nil {
				fmt.Printf("%s %s is already installed — `opentree agents update %s` refreshes it\n",
					agent.Origin.ID, agent.Origin.Version, agent.Origin.ID)
				return nil
			}
			return fmt.Errorf("%q is built into opentree — `opentree agents setup %s` manages its adapter", id, agent.Command)
		}

		fmt.Printf("fetching %s\n", registry.DefaultIndexURL)
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		index, note, err := registry.FetchOrCached(ctx, registry.DefaultIndexURL)
		if err != nil {
			return err
		}
		if note != "" {
			fmt.Println(note)
		}

		var entry *registry.Entry
		for i := range index.Agents {
			if index.Agents[i].ID == id {
				entry = &index.Agents[i]
				break
			}
		}
		if entry == nil {
			return fmt.Errorf("the registry lists no agent %q — `opentree agents search %s` looks for close matches", id, id)
		}
		// The loader will skip an install whose display name a built-in
		// answers to, so refusing here beats installing something that can
		// never load.
		if config.FindAgent(entry.Name) != nil {
			return fmt.Errorf("%q is named %q, which opentree's built-in %s already answers to", id, entry.Name, entry.Name)
		}

		plan, err := registry.NewPlan(*entry, registry.DefaultIndexURL)
		if err != nil {
			return err
		}

		fmt.Printf("\n%s\n", plan.Describe())
		if !agentsAddYes && !confirm(cmd.InOrStdin(), fmt.Sprintf("Install into %s?", plan.Dir)) {
			fmt.Println("Cancelled — nothing was fetched. Pass --yes to answer this from a script.")
			return nil
		}

		// The install runs on the command's own context, not the fetch
		// timeout: a cold npm install is however long it is.
		rec, err := plan.Run(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("\n✓ %s %s installed — `opentree agents use %s` makes it the default\n",
			rec.Entry.ID, rec.Entry.Version, rec.Entry.ID)
		return nil
	},
}

// registryAddCompletions completes ids from the cached index only — a tab
// press must never open a network connection. No cache, no completions; the
// first `agents search` or `add` fills it.
func registryAddCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	for _, e := range registry.CachedEntries() {
		if config.FindAgent(e.ID) != nil {
			continue // installed already, or shadowed by a built-in
		}
		completions = append(completions, fmt.Sprintf("%s\t%s", e.ID, trim(e.Description, 60)))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// installedRegistryCompletions completes the ids `remove` and `update` act
// on: what the store actually holds, broken installs included — those are
// exactly the ones remove exists for.
func installedRegistryCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	records, problems := registry.Installed()
	var completions []string
	for _, r := range records {
		completions = append(completions, fmt.Sprintf("%s\t%s", r.Entry.ID, trim(r.Entry.Description, 60)))
	}
	for _, p := range problems {
		if id, _, ok := strings.Cut(p, ":"); ok && registry.ValidateID(id) == nil {
			completions = append(completions, id+"\tbroken install")
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
