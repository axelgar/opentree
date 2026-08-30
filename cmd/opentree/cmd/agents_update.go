package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/registry"
)

var agentsUpdateYes bool

var agentsUpdateCmd = &cobra.Command{
	Use:   "update [id]",
	Short: "Update agents installed from the ACP Registry",
	Long: `Re-resolve installed registry agents against a fresh index and replace the
ones it moved past. With no id, every install is checked; each update is
confirmed on its own, so one agent's new version never smuggles in another's.

The new version is built beside the old and swapped in only once complete —
a failed update leaves the old agent exactly as it was.`,
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: installedRegistryCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		records, problems := registry.Installed()
		for _, p := range problems {
			fmt.Printf("⚠ %s\n", p)
		}
		if len(args) == 1 {
			id := args[0]
			kept := records[:0]
			for _, r := range records {
				if r.Entry.ID == id {
					kept = append(kept, r)
				}
			}
			records = kept
			if len(records) == 0 {
				return fmt.Errorf("%s is not installed from the registry", id)
			}
		}
		if len(records) == 0 {
			fmt.Println("nothing is installed from the registry — `opentree agents add <id>` installs an agent")
			return nil
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

		entries := map[string]registry.Entry{}
		for _, e := range index.Agents {
			entries[e.ID] = e
		}

		var failed int
		for _, rec := range records {
			entry, listed := entries[rec.Entry.ID]
			switch {
			case !listed:
				// Not an error and not a removal: the install still works, and
				// deleting somebody's agent because an index dropped it would
				// be the index deciding what runs on this machine.
				fmt.Printf("⚠ %s %s is no longer in the registry — it stays installed; `opentree agents remove %s` deletes it\n",
					rec.Entry.ID, rec.Entry.Version, rec.Entry.ID)
			case entry.Version == rec.Entry.Version:
				fmt.Printf("✓ %s %s — up to date\n", rec.Entry.ID, rec.Entry.Version)
			default:
				fmt.Printf("\n%s %s → %s\n", rec.Entry.ID, rec.Entry.Version, entry.Version)
				plan, err := registry.PlanUpdate(entry, registry.DefaultIndexURL)
				if err != nil {
					fmt.Printf("✗ %s: %v\n", rec.Entry.ID, err)
					failed++
					continue
				}
				fmt.Printf("%s\n", plan.Describe())
				if !agentsUpdateYes && !confirm(cmd.InOrStdin(), fmt.Sprintf("Update %s?", rec.Entry.ID)) {
					fmt.Printf("skipped %s\n", rec.Entry.ID)
					continue
				}
				if _, err := plan.Run(cmd.Context()); err != nil {
					// The swap only happens on success, so the old install is
					// still whole — worth saying, or a failed update reads as
					// a broken agent.
					fmt.Printf("✗ %s: %v — the installed %s is untouched\n", rec.Entry.ID, err, rec.Entry.Version)
					failed++
					continue
				}
				fmt.Printf("✓ %s updated to %s\n", rec.Entry.ID, entry.Version)
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d %s failed", failed, plural(failed, "update"))
		}
		return nil
	},
}
