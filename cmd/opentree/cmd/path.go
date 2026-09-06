package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

// PathCmd prints where a workspace's worktree is, for a shell to use.
var PathCmd = &cobra.Command{
	Use:   "path <branch-name>",
	Short: "Print where a workspace's worktree is",
	Long: `Print the directory a workspace's worktree is in, and nothing else, so a
shell can use it:

  cd "$(opentree path feat/x)"

Worktrees live under ~/.opentree/worktrees/<repo> unless base_dir says
otherwise, and a branch's directory is not its name — feat/x lives at feat-x —
so the path is worth asking for rather than guessing at.`,
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := workspaceService()
		if err != nil {
			return err
		}
		name := args[0]
		if _, err := svc.Workspace(name); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), svc.WorktreePath(name))
		return nil
	},
}

// workspaceService is the service every per-workspace command starts from:
// the repository this runs in, its config, and the state under it.
func workspaceService() (*workspace.Service, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return nil, err
	}
	return workspace.New(repoRoot, cfg)
}
