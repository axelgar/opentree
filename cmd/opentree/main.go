package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/cmd/opentree/cmd"
	"github.com/axelgar/opentree/pkg/tui"
)

// version is set at release time via -ldflags "-X main.version=...".
// For `go install` builds it falls back to the module version (see resolveVersion).
var version = "dev"

// resolveVersion returns the release version, or the module version embedded by
// `go install`, or "dev" for a plain local build.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

var rootCmd = &cobra.Command{
	Use:   "opentree",
	Short: "Orchestrate parallel AI coding sessions in isolated git worktrees",
	Long: `opentree is a CLI tool that manages multiple AI coding agent sessions.
Each session runs in an isolated git worktree, managed via tmux.

Think Conductor, but for the terminal.`,
	SilenceErrors: true, // main prints the error once itself
	SilenceUsage:  true,
	Run: func(cmd *cobra.Command, args []string) {
		// Launch TUI dashboard
		if err := tui.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.Version = resolveVersion()
	rootCmd.SetVersionTemplate("opentree {{.Version}}\n")
	rootCmd.Flags().BoolP("version", "v", false, "print the opentree version and exit")

	rootCmd.AddCommand(cmd.NewCmd)
	rootCmd.AddCommand(cmd.ListCmd)
	rootCmd.AddCommand(cmd.AttachCmd)
	rootCmd.AddCommand(cmd.DeleteCmd)
	rootCmd.AddCommand(cmd.DiffCmd)
	rootCmd.AddCommand(cmd.PrCmd)
	rootCmd.AddCommand(cmd.IssueCmd)
	rootCmd.AddCommand(cmd.InstallCompletionCmd)
	rootCmd.AddCommand(cmd.ConfigCmd)
	rootCmd.AddCommand(cmd.AgentsCmd)
	rootCmd.AddCommand(cmd.ReviewCmd)
	rootCmd.AddCommand(cmd.PruneCmd)
}
func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
