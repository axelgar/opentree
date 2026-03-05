package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
	"github.com/spf13/cobra"
)

var DeleteCmd = &cobra.Command{
	Use:   "delete <branch-name>",
	Short: "Delete a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")

		cmdExec := exec.Command("git", "rev-parse", "--show-toplevel")

		output, err := cmdExec.CombinedOutput()
		if err != nil {
			return fmt.Errorf("not in a git repository")
		}
		repoRoot := strings.TrimSpace(string(output))

		cfg, err := config.Load("")

		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		store, err := state.New(repoRoot)

		if err != nil {
			return fmt.Errorf("failed to load state: %w", err)
		}

		tmuxCtrl := tmux.New(cfg.Tmux.SessionPrefix)

		if err := tmuxCtrl.KillWindow(branchName); err != nil {
			fmt.Printf("Warning: failed to kill tmux window: %v\n", err)
		}

		wt := worktree.New()

		if err := wt.Delete(branchName, deleteBranch); err != nil {
			return fmt.Errorf("failed to delete worktree: %w", err)
		}

		if err := store.DeleteWorkspace(branchName); err != nil {

			fmt.Printf("Warning: failed to update state: %v\n", err)
		}

		fmt.Printf("✓ Deleted workspace '%s'\n", branchName)
		if deleteBranch {
			fmt.Printf("✓ Deleted branch '%s'\n", branchName)
		}

		return nil
	},
}

func init() {
	DeleteCmd.Flags().BoolP("delete-branch", "d", false, "Also delete the git branch")
}
