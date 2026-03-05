package cmd

import (
	"fmt"

	"os/exec"
	"strings"

	"github.com/axelgar/opentree/pkg/state"
	"github.com/spf13/cobra"
)

var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmdExec := exec.Command("git", "rev-parse", "--show-toplevel")

		output, err := cmdExec.CombinedOutput()
		if err != nil {
			return fmt.Errorf("not in a git repository")
		}
		repoRoot := strings.TrimSpace(string(output))

		store, err := state.New(repoRoot)

		if err != nil {
			return fmt.Errorf("failed to load state: %w", err)
		}

		workspaces := store.ListWorkspaces()
		if len(workspaces) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		fmt.Printf("%-30s %-15s %-15s %-10s\n", "NAME", "BRANCH", "BASE", "STATUS")
		fmt.Println(strings.Repeat("-", 70))
		for _, ws := range workspaces {
			fmt.Printf("%-30s %-15s %-15s %-10s\n", ws.Name, ws.Branch, ws.BaseBranch, ws.Status)
		}

		return nil
	},
}
