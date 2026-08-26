package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var PrCmd = &cobra.Command{
	Use:               "pr <branch-name>",
	Short:             "Publish a workspace as a GitHub PR: push what is missing, create or update, never duplicate",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: workspaceCompletions,
	Long: `Publish a workspace's branch as a GitHub pull request.

Publishing is a comparison first and a mutation second: the branch is pushed
only if origin is behind, a PR is created only if none exists, and an existing
open PR is simply brought up to date. Running it twice is safe — the second
run says "already up to date" instead of opening a duplicate.

With no --title/--body the content is generated the way the dashboard's PR
dialog prefills it: from the issue that started the workspace, or the branch
name, and the commits since base.`,
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

		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return err
		}

		out, err := svc.PublishPR(branchName, title, body)
		if err != nil {
			return err
		}

		switch {
		case out.Skipped != "":
			fmt.Printf("Nothing published: %s\n", out.Skipped)
		case out.Created:
			fmt.Printf("✓ Created PR: %s\n", out.PRURL)
		case out.Pushed:
			fmt.Printf("✓ Pushed and updated PR: %s\n", out.PRURL)
		default:
			fmt.Printf("✓ PR already up to date: %s\n", out.PRURL)
		}
		return nil
	},
}

func init() {
	PrCmd.Flags().StringP("title", "t", "", "PR title")
	PrCmd.Flags().StringP("body", "b", "", "PR body")
}
