package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var PruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove workspaces whose worktree was deleted outside opentree",
	RunE: func(cmd *cobra.Command, args []string) error {
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

		pruned, err := svc.Prune()
		if err != nil {
			return err
		}
		if len(pruned) == 0 {
			fmt.Println("Nothing to prune.")
			return nil
		}
		for _, name := range pruned {
			fmt.Printf("✓ Pruned workspace '%s' (branch left intact)\n", name)
		}
		return nil
	},
}
