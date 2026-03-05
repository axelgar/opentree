package cmd

import (
	"bufio"
	"fmt"
	"os"
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

		// Get workspace to retrieve base branch
		ws, err := store.GetWorkspace(branchName)
		if err != nil {
			fmt.Printf("Warning: workspace not in state, proceeding anyway\n")
		}

		wt := worktree.New()

		// Check for diff between base branch and worktree branch
		var baseBranch string
		if ws != nil && ws.BaseBranch != "" {
			baseBranch = ws.BaseBranch
		} else {
			baseBranch = "main" // fallback default
		}

		diff, err := wt.DiffBranches(branchName, baseBranch)
		if err != nil {
			fmt.Printf("Warning: failed to check diff: %v\n", err)
		} else if strings.TrimSpace(diff) != "" {
			// There are differences, ask for confirmation
			fmt.Printf("\nChanges detected between '%s' and '%s':\n", branchName, baseBranch)
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

		tmuxCtrl := tmux.New(cfg.Tmux.SessionPrefix)
		if err := tmuxCtrl.KillWindow(branchName); err != nil {
			fmt.Printf("Warning: failed to kill tmux window: %v\n", err)
		}

		// Always delete the branch along with worktree
		if err := wt.Delete(branchName, true); err != nil {
			return fmt.Errorf("failed to delete worktree: %w", err)
		}

		if err := store.DeleteWorkspace(branchName); err != nil {
			fmt.Printf("Warning: failed to update state: %v\n", err)
		}
		fmt.Printf("✓ Deleted workspace '%s'\n", branchName)
		fmt.Printf("✓ Deleted branch '%s'\n", branchName)
		return nil
	},
}

