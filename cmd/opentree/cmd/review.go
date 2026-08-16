package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var ReviewCmd = &cobra.Command{
	Use:   "review <branch-name>",
	Short: "Send PR review comments to the workspace's agent",
	Long: `Fetches all open PR review comments for the given workspace and sends
them to its agent as a prompt.

The comments go over the chat's control socket, so the workspace's chat has to
be running — it does not have to be the window you are looking at. Requires the
workspace to have an open PR on GitHub.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

		store, err := state.New(repoRoot)
		if err != nil {
			return fmt.Errorf("failed to open state: %w", err)
		}
		ws, err := store.GetWorkspace(branchName)
		if err != nil {
			return fmt.Errorf("failed to find workspace %q: %w", branchName, err)
		}

		comments, err := github.New().FetchPRReviews(ws.Branch)
		// Partial results (top-level reviews fetched, inline-thread fetch
		// failed) are still sent rather than discarded.
		if err != nil && len(comments) == 0 {
			return fmt.Errorf("failed to fetch PR reviews: %w", err)
		}
		if len(comments) == 0 {
			fmt.Println("No review comments found for this workspace.")
			return nil
		}

		// The chat refuses a prompt it cannot honour — mid-turn, or not running
		// at all — so reaching here means the agent really has the comments.
		if err := chat.Send(chat.SocketPath(repoRoot, branchName), chat.Command{
			Type: chat.CommandPrompt, Text: github.FormatReviewsPrompt(comments),
		}); err != nil {
			return fmt.Errorf("failed to send reviews to %s: %w", branchName, err)
		}

		fmt.Printf("✓ Sent %d review comment(s) to the agent for workspace %q.\n", len(comments), branchName)
		return nil
	},
}
