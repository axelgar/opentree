package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/spf13/cobra"
)

var PrCmd = &cobra.Command{
	Use:   "pr <branch-name>",
	Short: "Create a GitHub PR for a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		title, _ := cmd.Flags().GetString("title")
		body, _ := cmd.Flags().GetString("body")

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

		ws, err := store.GetWorkspace(branchName)
		if err != nil {
			return fmt.Errorf("workspace not found: %w", err)
		}

		gh := github.New()
		if !gh.IsInstalled() {
			return fmt.Errorf("gh CLI is not installed — install it from https://cli.github.com/")
		}

		prURL, err := gh.CreatePR(branchName, ws.BaseBranch, title, body)
		if err != nil {
			return fmt.Errorf("failed to create PR: %w", err)
		}

		fmt.Printf("✓ Created PR: %s\n", prURL)
		return nil
	},
}

func init() {
	PrCmd.Flags().StringP("title", "t", "", "PR title")
	PrCmd.Flags().StringP("body", "b", "", "PR body")
}
