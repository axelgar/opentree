package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var SeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Manage the untracked files opentree links into worktrees",
	Long: `Manage the [workspace] seed list.

Seeded files are symlinks to the repository's own copy, so one credential set is
shared by every worktree. Detaching swaps one for a copy, for a branch that has
to change it.

  opentree seed detach <branch> <path>   give this worktree its own copy

Seeding itself happens when a workspace is created, and again on
` + "`opentree setup <branch>`" + `, which also reports what is linked with --dry-run.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var seedDetachCmd = &cobra.Command{
	Use:               "detach <branch-name> <path>",
	Short:             "Replace a seeded link with this worktree's own copy",
	Args:              cobra.ExactArgs(2),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, path := args[0], args[1]

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		store, err := state.New(repoRoot)
		if err != nil {
			return fmt.Errorf("failed to open state: %w", err)
		}
		ws, err := store.GetWorkspace(name)
		if err != nil {
			return err
		}

		if err := bootstrap.Detach(repoRoot, workspaceWorktree(repoRoot, cfg, ws), path); err != nil {
			return err
		}
		fmt.Printf("✓ %s is now %s's own copy — edits here no longer reach the repository's\n", path, name)
		return nil
	},
}

func init() {
	SeedCmd.AddCommand(seedDetachCmd)
}
