package cmd

import (
	"fmt"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
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

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Look up the base branch from state
		var baseBranch string
		if store, err := state.New(repoRoot); err == nil {
			if ws, err := store.GetWorkspace(branchName); err == nil {
				baseBranch = ws.BaseBranch
			}
		}

		wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
		diff, err := wt.Diff(branchName, baseBranch)
		if err != nil {
			return fmt.Errorf("failed to get diff: %w", err)
		}

		fmt.Println(diff)
		return nil
	},
}
