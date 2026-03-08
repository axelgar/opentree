package cmd

import (
	"fmt"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
	"github.com/spf13/cobra"
)

var NewCmd = &cobra.Command{
	Use:   "new <branch-name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		fromRemote, _ := cmd.Flags().GetBool("remote")

		// Load config
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

		if fromRemote {
			ws, err := svc.CreateFromRemoteBranch(branchName)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Checked out remote branch '%s' into new workspace\n", ws.Name)
			fmt.Printf("✓ Launched %s in tmux window\n", ws.Agent)
			fmt.Printf("\nTo attach: opentree attach %s\n", ws.Name)
			return nil
		}

		baseBranch, _ := cmd.Flags().GetString("base")
		if baseBranch == "" {
			baseBranch = cfg.Worktree.DefaultBase
		}

		ws, err := svc.Create(branchName, baseBranch)
		if err != nil {
			return err
		}

		fmt.Printf("✓ Created workspace '%s' based on '%s'\n", ws.Name, ws.BaseBranch)
		fmt.Printf("✓ Launched %s in tmux window\n", ws.Agent)
		fmt.Printf("\nTo attach: opentree attach %s\n", ws.Name)
		return nil
	},
}

func init() {
	NewCmd.Flags().StringP("base", "b", "", "Base branch to create worktree from (default: config default)")
	NewCmd.Flags().BoolP("remote", "r", false, "Check out an existing remote branch instead of creating a new one")
}
