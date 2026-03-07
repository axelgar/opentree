package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	ghpkg "github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
	"github.com/spf13/cobra"
)

var IssueCmd = &cobra.Command{
	Use:   "issue <number>",
	Short: "Create a workspace from a GitHub issue",
	Long: `Fetches a GitHub issue and creates a workspace pre-loaded with its context.

The branch name is auto-generated from the issue number and title
(e.g. issue #42 "Add dark mode" → issue-42-add-dark-mode).

A TASK.md file containing the issue title, labels, and description is written
into the new worktree so the AI agent can start working immediately.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueNum, err := strconv.Atoi(args[0])
		if err != nil || issueNum <= 0 {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}
		baseBranch, _ := cmd.Flags().GetString("base")

		// Fetch issue details
		ghMgr := ghpkg.New()
		if !ghMgr.IsInstalled() {
			return fmt.Errorf("gh CLI is not installed — install it from https://cli.github.com/")
		}
		issue, err := ghMgr.GetIssue(issueNum)
		if err != nil {
			return fmt.Errorf("failed to fetch issue: %w", err)
		}

		branchName := ghpkg.IssueBranchName(issue.Number, issue.Title)
		fmt.Printf("Issue #%d: %s\n", issue.Number, issue.Title)
		fmt.Printf("Branch:   %s\n\n", branchName)

		// Load config
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if baseBranch == "" {
			baseBranch = cfg.Worktree.DefaultBase
		}

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

		// Create git worktree
		wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
		if err := wt.Create(branchName, baseBranch); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		dirName := gitutil.SanitizeBranchName(branchName)
		worktreePath := filepath.Join(repoRoot, cfg.Worktree.BaseDir, dirName)

		// Write TASK.md with issue context for the AI agent
		taskContent := ghpkg.IssueTaskContent(issue)
		taskFile := filepath.Join(worktreePath, "TASK.md")
		if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
			fmt.Printf("Warning: could not write TASK.md: %v\n", err)
		}

		// Initialize state store
		store, err := state.New(repoRoot)
		if err != nil {
			return fmt.Errorf("failed to initialize state: %w", err)
		}

		// Create tmux window and launch agent
		tmuxCtrl := tmux.New(cfg.Tmux.SessionPrefix)
		agentCmd := cfg.Agent.Command
		if err := tmuxCtrl.CreateWindow(branchName, worktreePath, agentCmd, cfg.Agent.Args...); err != nil {
			return fmt.Errorf("failed to create tmux window: %w", err)
		}

		// Persist workspace with issue metadata
		ws := &state.Workspace{
			Name:        branchName,
			Branch:      branchName,
			BaseBranch:  baseBranch,
			CreatedAt:   time.Now(),
			Status:      "active",
			Agent:       agentCmd,
			WorktreeDir: worktreePath,
			IssueNumber: issue.Number,
			IssueTitle:  issue.Title,
		}
		if err := store.AddWorkspace(ws); err != nil {
			return fmt.Errorf("failed to save workspace state: %w", err)
		}

		fmt.Printf("✓ Created workspace '%s'\n", branchName)
		fmt.Printf("✓ Wrote issue context to TASK.md\n")
		fmt.Printf("✓ Launched %s in tmux window\n", agentCmd)
		fmt.Printf("\nTo attach: opentree attach %s\n", branchName)
		return nil
	},
}

func init() {
	IssueCmd.Flags().StringP("base", "b", "", "Base branch to create worktree from (default: config default)")
}
