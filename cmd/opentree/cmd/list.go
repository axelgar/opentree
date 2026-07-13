package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
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

		workspaces := svc.ListWorkspaces()

		// Live status derived from tmux windows: active / idle / stopped.
		// Degrades to "unknown" when the window list is unavailable.
		statuses := svc.WindowStatuses()
		liveStatus := func(name string) string {
			if s := statuses[name]; s != "" {
				return s
			}
			return "unknown"
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			// Overwrite the persisted (never-updated) Status field with the
			// live value so scripts see real state, not a constant "active".
			for _, ws := range workspaces {
				ws.Status = liveStatus(ws.Name)
			}
			out, err := json.MarshalIndent(workspaces, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal workspaces: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		if len(workspaces) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		fmt.Printf("%-30s %-15s %-15s %-10s\n", "NAME", "BRANCH", "BASE", "STATUS")
		fmt.Println(strings.Repeat("-", 70))
		for _, ws := range workspaces {
			fmt.Printf("%-30s %-15s %-15s %-10s\n", ws.Name, ws.Branch, ws.BaseBranch, liveStatus(ws.Name))
		}

		return nil
	},
}

func init() {
	ListCmd.Flags().BoolP("json", "j", false, "Output workspaces as JSON")
}
