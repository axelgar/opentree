package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
)

var PrCmd = &cobra.Command{
	Use:               "pr <branch-name>",
	Short:             "Create a GitHub PR for a workspace",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		title, _ := cmd.Flags().GetString("title")
		body, _ := cmd.Flags().GetString("body")

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		svc, err := newService(repoRoot, cfg)
		if err != nil {
			return err
		}

		prURL, err := svc.CreatePR(branchName, title, body)
		if err != nil {
			return err
		}

		fmt.Printf("✓ Created PR: %s\n", prURL)
		return nil
	},
}

func init() {
	PrCmd.Flags().StringP("title", "t", "", "PR title")
	PrCmd.Flags().StringP("body", "b", "", "PR body")
}
