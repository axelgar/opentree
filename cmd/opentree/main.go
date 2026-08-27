package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/cmd/opentree/cmd"
	"github.com/axelgar/opentree/pkg/diag"
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
	rootCmd.AddCommand(cmd.ChatCmd)
	rootCmd.AddCommand(cmd.DeleteCmd)
	rootCmd.AddCommand(cmd.DiffCmd)
	rootCmd.AddCommand(cmd.PrCmd)
	rootCmd.AddCommand(cmd.AutoCmd)
	rootCmd.AddCommand(cmd.DispatchCmd)
	rootCmd.AddCommand(cmd.IssueCmd)
	rootCmd.AddCommand(cmd.InstallCompletionCmd)
	rootCmd.AddCommand(cmd.UninstallCmd)
	rootCmd.AddCommand(cmd.ConfigCmd)
	rootCmd.AddCommand(cmd.AgentsCmd)
	rootCmd.AddCommand(cmd.ReviewCmd)
	rootCmd.AddCommand(cmd.CICmd)
	rootCmd.AddCommand(cmd.PruneCmd)
	rootCmd.AddCommand(cmd.TrustCmd)
	rootCmd.AddCommand(cmd.SetupCmd)
	rootCmd.AddCommand(cmd.SeedCmd)
	rootCmd.AddCommand(cmd.SkillsCmd)
	rootCmd.AddCommand(cmd.NotifyCmd)
	rootCmd.AddCommand(cmd.DoctorCmd)
}
func main() {
	// Before anything else, so a failure during startup is in the log too.
	// Named for the command being run rather than "opentree": a dashboard and
	// one chat per workspace write to the same file at once.
	component := "opentree"
	if len(os.Args) > 1 {
		component = os.Args[1]
	}
	diag.Init(component)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		// dispatch --headless promises codes a script can branch on; every
		// other failure keeps the generic 1.
		var coded cmd.ExitCodeError
		if errors.As(err, &coded) {
			os.Exit(coded.Code)
		}
		os.Exit(1)
	}
}
