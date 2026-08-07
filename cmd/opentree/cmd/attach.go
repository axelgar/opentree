package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var AttachCmd = &cobra.Command{
	Use:               "attach <branch-name>",
	Short:             "Attach to a workspace's tmux window",
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]

		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return fmt.Errorf("failed to find repo root: %w", err)
		}

		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize workspace service: %w", err)
		}

		// A closed window is not a dead workspace: reopen it before attaching,
		// the same as the TUI does.
		if reopened, err := svc.EnsureWindow(branchName); err != nil {
			return err
		} else if reopened {
			fmt.Printf("Reopened %s\n", branchName)
		}

		if err := svc.Process().AttachWindow(branchName); err != nil {
			return fmt.Errorf("failed to attach to workspace: %w", err)
		}

		return nil
	},
}
