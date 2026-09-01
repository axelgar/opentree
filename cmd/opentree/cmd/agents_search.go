package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/registry"
)

// agentsSearchCmd is one of the three commands allowed to touch the network
// (add and update are the others); everything else answers from disk. It says
// which index it is asking before it asks, for the same reason the install
// prompts print their command in full: what opentree reaches for should never
// have to be taken on faith.
var agentsSearchCmd = &cobra.Command{
	Use:   "search [term]",
	Short: "Search the ACP Registry for agents to install",
	Long: `List the agents the ACP Registry knows, newest index first.

With a term, only entries whose id, name or description contain it are shown.
When the registry cannot be reached, the last index this machine saw answers
instead, with its age noted.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("fetching %s\n", registry.DefaultIndexURL)
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		index, note, err := registry.FetchOrCached(ctx, registry.DefaultIndexURL)
		if err != nil {
			return err
		}
		if note != "" {
			fmt.Println(note)
		}

		term := ""
		if len(args) == 1 {
			term = strings.ToLower(args[0])
		}

		fmt.Printf("\n%-18s %-10s %-14s %-16s %s\n", "ID", "VERSION", "VIA", "STATUS", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 100))
		matched, sawUvx := 0, false
		for _, e := range index.Agents {
			if term != "" && !strings.Contains(strings.ToLower(e.ID), term) &&
				!strings.Contains(strings.ToLower(e.Name), term) &&
				!strings.Contains(strings.ToLower(e.Description), term) {
				continue
			}
			matched++
			sawUvx = sawUvx || e.Distribution.Uvx != nil
			fmt.Printf("%-18s %-10s %-14s %-16s %s\n",
				e.ID, e.Version, e.Via(), registry.Status(e), trim(e.Description, 40))
		}
		if matched == 0 {
			fmt.Printf("nothing in the registry matches %q\n", args[0])
			return nil
		}
		if sawUvx {
			fmt.Println("* uvx — a distribution opentree does not support yet")
		}
		fmt.Printf("\n%d %s — `opentree agents add <id>` installs one\n", matched, plural(matched, "agent"))
		return nil
	},
}
