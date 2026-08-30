package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/registry"
)

var agentsRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove an agent installed from the ACP Registry",
	Long: `Remove a registry install: the one directory under ~/.opentree/registry
that 'opentree agents add' created for it. The built-in agents cannot be
removed — they are part of opentree — and nothing outside the store is
touched: an agent you installed yourself stays yours.

A broken install — one whose record no longer loads — is still removable by
its id; that is half the point of the command.`,
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: installedRegistryCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if agent := config.FindAgent(id); agent != nil && agent.Origin == nil {
			return fmt.Errorf("%q is built into opentree and cannot be removed", id)
		}
		if err := registry.Remove(id); err != nil {
			return err
		}
		fmt.Printf("✓ removed %s\n", id)

		// The config may still name what was just removed. Not an error — the
		// workspace-side message covers running state — but saying it now
		// beats the user discovering it at the next `opentree new`.
		if cfg, err := config.Load(""); err == nil && cfg.Agent.Command == id {
			fmt.Printf("your config still names %s — `opentree agents use <name>` picks another\n", id)
		}
		return nil
	},
}
