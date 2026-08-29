package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/workspace"
)

var PromoteCmd = &cobra.Command{
	Use:   "promote <branch-name>",
	Short: "Keep one fan-out sibling and delete the rest",
	Long: `Pick the winner of a fan-out: the named workspace stays, every other
sibling of its group is deleted — worktree, branch and window — and the
group dissolves.

The winner keeps its suffixed branch name (feat/x-claude stays
feat/x-claude): its worktree, chat and any open PR are all keyed on that
name, and a rename would invalidate every one of them under a live agent.

The same action is on W in the dashboard.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: fanoutCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		winner := args[0]
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return err
		}

		losers := svc.FanoutSiblings(winner)

		// The same are-you-sure delete has, but over the whole losing side at
		// once: each dirty sibling's diff, then one question. Clean losers ask
		// nothing — their branches carry nothing that is not also on origin or
		// in the winner's favour already judged worse.
		var dirty []string
		for _, name := range losers {
			diff, err := svc.HasChanges(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to check '%s' for changes: %v\n", name, err)
				diff = "(could not verify — the worktree may contain unsaved work)"
			}
			if strings.TrimSpace(diff) != "" {
				fmt.Printf("\nChanges detected in '%s':\n", name)
				fmt.Println(diff)
				dirty = append(dirty, name)
			}
		}
		if len(dirty) > 0 {
			fmt.Printf("\nThis will delete %d sibling workspace(s) and their branches: %s. Continue? [y/N]: ", len(losers), strings.Join(losers, ", "))
			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read input: %w", err)
			}
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("Promotion cancelled")
				return nil
			}
		}

		deleted, err := svc.Promote(winner)
		for _, name := range deleted {
			fmt.Printf("✓ Deleted sibling '%s'\n", name)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Run 'opentree promote %s' again to retry what is left.\n", winner)
			return err
		}

		if len(deleted) == 0 {
			fmt.Printf("✓ Promoted '%s' — no siblings left to delete\n", winner)
		} else {
			fmt.Printf("✓ Promoted '%s' — kept branch '%s'\n", winner, winner)
		}
		return nil
	},
}

// fanoutCompletions offers only workspaces a promote can act on: the members
// of fan-out groups. Completing the others would only teach the command to
// refuse.
func fanoutCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	store, err := state.New(repoRoot)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, ws := range store.ListWorkspaces() {
		if ws.FanoutGroup != "" {
			names = append(names, fmt.Sprintf("%s\t%s · %s", ws.Name, ws.FanoutGroup, ws.Agent))
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
