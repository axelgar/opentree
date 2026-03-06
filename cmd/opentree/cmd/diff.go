package cmd

import (
	"fmt"

	"github.com/axelgar/opentree/pkg/worktree"
	"github.com/spf13/cobra"
)

var DiffCmd = &cobra.Command{
	Use:               "diff <branch-name>",
	Short:             "Show diff for a workspace",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]

		wt := worktree.New()
		diff, err := wt.Diff(branchName)
		if err != nil {
			return fmt.Errorf("failed to get diff: %w", err)
		}

		fmt.Println(diff)
		return nil
	},
}
