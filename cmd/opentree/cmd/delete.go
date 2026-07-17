package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var DeleteCmd = &cobra.Command{
	Use:               "delete <branch-name>",
	Short:             "Delete a workspace",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		if err := gitutil.ValidateBranchName(branchName); err != nil {
			return err
		}
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

		// Check for work that would be lost and prompt user
		diff, err := svc.HasChanges(branchName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to check for changes: %v\n", err)
			diff = "(could not verify — the worktree may contain unsaved work)"
		}
		if strings.TrimSpace(diff) != "" {
			fmt.Printf("\nChanges detected in '%s':\n", branchName)
			fmt.Println(diff)
			fmt.Printf("\nThis will delete the worktree and branch '%s'. Continue? [y/N]: ", branchName)

			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read input: %w", err)
			}

			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("Deletion cancelled")
				return nil
			}
		}

		if err := svc.Delete(branchName); err != nil {
			return err
		}

		fmt.Printf("✓ Deleted workspace '%s'\n", branchName)
		fmt.Printf("✓ Deleted branch '%s'\n", branchName)
		return nil
	},
}
