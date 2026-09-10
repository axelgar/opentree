package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

// SyncCmd brings a workspace's base branch into its branch.
var SyncCmd = &cobra.Command{
	Use:   "sync <branch-name>",
	Short: "Merge the base branch into a workspace",
	Long: `Bring the workspace's base branch into its own: fetch origin's copy of the
base when origin can be reached, and merge it into the worktree.

A merge rather than a rebase — the branch may already be pushed and under
review, and a rebase rewrites what the PR has. Conflicts are not a failure:
they are listed, the merge is left in progress in the worktree with its
markers, and --ask hands them to the workspace's agent to resolve.`,
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		noFetch, _ := cmd.Flags().GetBool("no-fetch")
		ask, _ := cmd.Flags().GetBool("ask")

		svc, err := workspaceService()
		if err != nil {
			return err
		}
		res, err := svc.Sync(name, noFetch)
		if err != nil {
			return err
		}
		if note := res.Note(); note != "" {
			fmt.Printf("  %s\n", note)
		}

		switch {
		case len(res.Conflicts) > 0:
			fmt.Printf("✗ Merging %s into %s stopped on %d conflict(s):\n", res.Ref, name, len(res.Conflicts))
			for _, f := range res.Conflicts {
				fmt.Printf("  - %s\n", f)
			}
			fmt.Printf("  The merge is in progress in %s.\n", svc.WorktreePath(name))
			if !ask {
				fmt.Printf("  opentree sync %s --ask hands them to the agent\n", name)
				return nil
			}
			repoRoot, err := gitutil.RepoRoot()
			if err != nil {
				return err
			}
			ws, err := svc.Workspace(name)
			if err != nil {
				return err
			}
			if err := chat.Send(chat.SocketPath(repoRoot, name), name, chat.Command{
				Type: chat.CommandPrompt, Text: workspace.SyncConflictPrompt(ws.Branch, res),
			}); err != nil {
				fmt.Fprintf(os.Stderr, "could not reach %s's agent: %v\n", name, err)
				return err
			}
			fmt.Printf("✓ Asked %s's agent to resolve them\n", name)
		case res.Updated:
			fmt.Printf("✓ Merged %s into %s\n", res.Ref, name)
		default:
			fmt.Printf("✓ %s already has everything %s has\n", name, res.Ref)
		}
		return nil
	},
}

func init() {
	SyncCmd.Flags().Bool("no-fetch", false, "Merge the base as it is here, without fetching origin's first")
	SyncCmd.Flags().Bool("ask", false, "On conflicts, hand the files to the workspace's agent to resolve")
}
