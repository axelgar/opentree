package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

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
