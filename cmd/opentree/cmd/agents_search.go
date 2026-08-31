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
				e.ID, e.Version, distributionKinds(e), searchStatus(e), trim(e.Description, 40))
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

// distributionKinds names how an entry arrives, in opentree's order of
// preference. uvx is listed but marked: hiding those entries would make the
// registry look smaller than it is, and the honest answer is "exists, not
// supported yet".
func distributionKinds(e registry.Entry) string {
	var kinds []string
	if e.Distribution.Npx != nil {
		kinds = append(kinds, "npm")
	}
	if len(e.Distribution.Binary) > 0 {
		kinds = append(kinds, "binary")
	}
	if e.Distribution.Uvx != nil {
		kinds = append(kinds, "uvx*")
	}
	return strings.Join(kinds, "+")
}

// searchStatus is what this machine already has under the entry's name: the
// built-in agent that shadows it, the installed version, or nothing. FindAgent
// sees registry installs too — the loader ran before this command — so one
// lookup answers for both kinds.
func searchStatus(e registry.Entry) string {
	agent := config.FindAgent(e.ID)
	if agent == nil {
		agent = config.FindAgent(e.Name)
	}
	switch {
	case agent == nil:
		return ""
	case agent.Origin != nil:
		return "installed " + agent.Origin.Version
	default:
		return "built-in"
	}
}
