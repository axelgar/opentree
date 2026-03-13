package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var AttachCmd = &cobra.Command{
	Use:               "attach <branch-name>",
	Short:             "Interact with a workspace (use TUI)",
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("The 'attach' command is not available with the native terminal backend.")
		fmt.Println("Use the TUI to interact with agents: opentree")
		return nil
	},
}
