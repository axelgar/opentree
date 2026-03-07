package cmd

import (
	"fmt"
	"strconv"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
	"github.com/spf13/cobra"
)

var IssueCmd = &cobra.Command{
	Use:   "issue <number>",
	Short: "Create a workspace from a GitHub issue",
	Long: `Fetches a GitHub issue and creates a workspace pre-loaded with its context.

The branch name is auto-generated from the issue number and title
(e.g. issue #42 "Add dark mode" → issue-42-add-dark-mode).

A TASK.md file containing the issue title, labels, and description is written
into the new worktree so the AI agent can start working immediately.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueNum, err := strconv.Atoi(args[0])
		if err != nil || issueNum <= 0 {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}
		baseBranch, _ := cmd.Flags().GetString("base")

		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return err
		}

		ws, err := svc.CreateFromIssue(issueNum, baseBranch)
		if err != nil {
			return err
		}

		fmt.Printf("Issue #%d: %s\n", ws.IssueNumber, ws.IssueTitle)
		fmt.Printf("Branch:   %s\n\n", ws.Branch)
		fmt.Printf("✓ Created workspace '%s'\n", ws.Name)
		fmt.Printf("✓ Wrote issue context to TASK.md\n")
		fmt.Printf("✓ Launched %s in tmux window\n", ws.Agent)
		fmt.Printf("\nTo attach: opentree attach %s\n", ws.Name)
		return nil
	},
}

func init() {
	IssueCmd.Flags().StringP("base", "b", "", "Base branch to create worktree from (default: config default)")
}
