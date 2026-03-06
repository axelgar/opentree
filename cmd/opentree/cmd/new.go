package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
	"github.com/spf13/cobra"
)

var NewCmd = &cobra.Command{
	Use:   "new <branch-name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		baseBranch, _ := cmd.Flags().GetString("base")
		// Load config
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if baseBranch == "" {
			baseBranch = cfg.Worktree.DefaultBase
		}
		
		// Step 1: Create git worktree
		wt := worktree.New()
		if err := wt.Create(branchName, baseBranch); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}
		
		// Get repo root and worktree path
		cmdExec := exec.Command("git", "rev-parse", "--show-toplevel")
		output, err := cmdExec.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to get repo root: %w", err)
		}
		repoRoot := strings.TrimSpace(string(output))
		
		dirName := strings.ReplaceAll(branchName, "/", "-")
		worktreePath := filepath.Join(repoRoot, cfg.Worktree.BaseDir, dirName)
		
		// Step 2: Initialize state store
		store, err := state.New(repoRoot)
		if err != nil {
			return fmt.Errorf("failed to initialize state: %w", err)
		}
		
		// Step 3: Create tmux window and launch agent
		tmuxCtrl := tmux.New(cfg.Tmux.SessionPrefix)
		agentCmd := cfg.Agent.Command
		if err := tmuxCtrl.CreateWindow(branchName, worktreePath, agentCmd, cfg.Agent.Args...); err != nil {
			return fmt.Errorf("failed to create tmux window: %w", err)
		}
		
		// Step 4: Save workspace to state
		ws := &state.Workspace{
			Name:        branchName,
			Branch:      branchName,
			BaseBranch:  baseBranch,
			CreatedAt:   time.Now(),
			Status:      "active",
			Agent:       agentCmd,
			WorktreeDir: worktreePath,
		}
		if err := store.AddWorkspace(ws); err != nil {
			return fmt.Errorf("failed to save workspace state: %w", err)
		}
		fmt.Printf("✓ Created workspace '%s' based on '%s'\n", branchName, baseBranch)
		fmt.Printf("✓ Launched %s in tmux window\n", agentCmd)
		fmt.Printf("\nTo attach: opentree attach %s\n", branchName)
		return nil
	},
}

func init() {
	NewCmd.Flags().StringP("base", "b", "", "Base branch to create worktree from (default: config default)")
}
