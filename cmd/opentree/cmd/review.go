package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var ReviewCmd = &cobra.Command{
	Use:   "review <branch-name>",
	Short: "Send PR review comments to the workspace's agent",
	Long: `Fetches all open PR review comments for the given workspace and sends
them as a formatted prompt to the running agent in the tmux window.

The agent will receive the review comments and be asked to address them.
Requires the workspace to have an open PR on GitHub.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]

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

		count, err := svc.SendReviewsToAgent(branchName)
		if err != nil {
			return err
		}

		if count == 0 {
			fmt.Println("No review comments found for this workspace.")
			return nil
		}

		fmt.Printf("✓ Sent %d review comment(s) to the agent for workspace %q.\n", count, branchName)
		return nil
	},
}
