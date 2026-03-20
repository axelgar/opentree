package cmd

import (
	"github.com/spf13/cobra"
)

var AttachCmd = &cobra.Command{
	Use:               "attach <branch-name>",
	Short:             "Interact with a workspace (use TUI)",
	Deprecated:        "use the TUI to interact with agents: run 'opentree'",
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
