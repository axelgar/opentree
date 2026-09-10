package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ShellCmd opens a shell in a workspace's worktree, in a tmux window of its
// own beside the chat.
var ShellCmd = &cobra.Command{
	Use:   "shell <branch-name>",
	Short: "Open a shell in a workspace's worktree",
	Long: `Open a shell in the workspace's worktree — a tmux window of its own beside the
chat, named <branch>:sh — and go to it. The window is reused while it lives:
a second shell is the same shell.

The chat's window is opentree's, holding the conversation. This one is yours,
for running the tests the agent asked about and pasting it the answer. From
inside the chat, /shell does the same.`,
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := workspaceService()
		if err != nil {
			return err
		}
		name := args[0]
		created, err := svc.OpenShell(name)
		if err != nil {
			return err
		}
		if created {
			fmt.Printf("Opened a shell in %s\n", svc.WorktreePath(name))
		}
		if err := svc.Process().AttachWindow(svc.ShellWindow(name)); err != nil {
			return fmt.Errorf("failed to attach to %s's shell: %w", name, err)
		}
		return nil
	},
}
