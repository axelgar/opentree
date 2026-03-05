package main

import (
	"fmt"
	"os"

	"github.com/axelgar/opentree/pkg/tui"
	"github.com/spf13/cobra"
	"github.com/axelgar/opentree/cmd/opentree/cmd"
)


var rootCmd = &cobra.Command{
	Use:   "opentree",
	Short: "Orchestrate parallel AI coding sessions in isolated git worktrees",
	Long: `opentree is a CLI tool that manages multiple AI coding agent sessions.
Each session runs in an isolated git worktree, managed via tmux.

Think Conductor, but for the terminal.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Launch TUI dashboard
		if err := tui.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(cmd.NewCmd)
	rootCmd.AddCommand(cmd.ListCmd)
	rootCmd.AddCommand(cmd.AttachCmd)
	rootCmd.AddCommand(cmd.DeleteCmd)
	rootCmd.AddCommand(cmd.DiffCmd)
	rootCmd.AddCommand(cmd.PrCmd)
}
func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
