package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/workspace"
)

var DeleteCmd = &cobra.Command{
	Use:   "delete <branch-name>",
	Short: "Delete a workspace",
	Long: `Delete a workspace: its worktree, its tmux windows and its branch. Work that
would be lost — commits ahead of the base, uncommitted changes — is shown and
asked about first.

With --merged, every workspace whose PR has merged is deleted instead, one
question each for any that still hold something. The dashboard marks those
rows "merged · ready to delete"; this is how they are all cleared at once.`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		merged, _ := cmd.Flags().GetBool("merged")
		switch {
		case merged && len(args) > 0:
			return fmt.Errorf("--merged deletes every merged workspace; it does not take a name")
		case !merged && len(args) == 0:
			return fmt.Errorf("a workspace name is required (or --merged for every merged one)")
		}

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

		if !merged {
			branchName := args[0]
			if err := gitutil.ValidateBranchName(branchName); err != nil {
				return err
			}
			return deleteWorkspace(svc, branchName)
		}

		names := mergedWorkspaces(svc.ListWorkspaces())
		if len(names) == 0 {
			fmt.Println("No workspace has a merged PR.")
			return nil
		}
		for _, name := range names {
			if err := deleteWorkspace(svc, name); err != nil {
				return err
			}
		}
		return nil
	},
}

// mergedWorkspaces is every workspace whose PR the dashboard has seen merge,
// in the order they are listed. The status is what the dashboard recorded on
// its last look, which is as fresh as the badge the reader saw.
func mergedWorkspaces(all []*state.Workspace) []string {
	var names []string
	for _, ws := range all {
		if ws.PRStatus == "merged" {
			names = append(names, ws.Name)
		}
	}
	return names
}

// deleteWorkspace removes one workspace, asking first when it holds work that
// would be lost. A declined question is not an error: the workspace stays,
// and with --merged the next one is still asked about.
func deleteWorkspace(svc *workspace.Service, branchName string) error {
	// Check for work that would be lost and prompt user
	diff, err := svc.HasChanges(branchName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to check for changes: %v\n", err)
		diff = "(could not verify — the worktree may contain unsaved work)"
	}
	if strings.TrimSpace(diff) != "" {
		fmt.Printf("\nChanges detected in '%s':\n", branchName)
		fmt.Println(diff)
		fmt.Printf("\nThis will delete the worktree and branch '%s'. Continue? [y/N]: ", branchName)

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Printf("Kept '%s'\n", branchName)
			return nil
		}
	}

	if err := svc.Delete(branchName); err != nil {
		return err
	}

	fmt.Printf("✓ Deleted workspace '%s'\n", branchName)
	fmt.Printf("✓ Deleted branch '%s'\n", branchName)
	return nil
}

func init() {
	DeleteCmd.Flags().Bool("merged", false, "Delete every workspace whose PR has merged")
}
