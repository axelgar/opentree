package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var CICmd = &cobra.Command{
	Use:   "ci <branch-name>",
	Short: "Send the PR's failing CI checks to the workspace's agent",
	Long: `Fetches the failing checks on the workspace's PR — names, links, and the
tail of each GitHub Actions log — and sends them to its agent as a prompt.

The failures go over the chat's control socket, so the workspace's chat has to
be running — it does not have to be the window you are looking at. Requires the
workspace to have a PR on GitHub. The dashboard's badge says that CI is red;
this is how the agent learns why.`,
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

		_, failures, err := github.New().FetchFailingChecks(ws.Branch, ws.WorktreeDir)
		// The same partial-results rule as review: names without logs are
		// still worth sending rather than discarding.
		if err != nil && len(failures) == 0 {
			return fmt.Errorf("failed to fetch CI status: %w", err)
		}
		if len(failures) == 0 {
			fmt.Println("No failing checks on this workspace's PR.")
			return nil
		}

		if err := chat.Send(chat.SocketPath(repoRoot, branchName), branchName, chat.Command{
			Type: chat.CommandPrompt, Text: github.FormatCIPrompt(failures),
		}); err != nil {
			return fmt.Errorf("failed to send CI failures to %s: %w", branchName, err)
		}

		fmt.Printf("✓ Sent %d failing check(s) to the agent for workspace %q.\n", len(failures), branchName)
		return nil
	},
}
