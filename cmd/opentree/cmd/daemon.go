package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/daemon"
	"github.com/axelgar/opentree/pkg/gitutil"
)

// DaemonCmd is the parent command for daemon management.
var DaemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the background daemon process",
	Long:  "The daemon owns PTY sessions so they persist across TUI restarts.",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon in the foreground",
	Long:  "Starts the daemon in the foreground (this command blocks). Use a process supervisor or '&' to run it in the background.",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, _ := cmd.Flags().GetString("repo-root")
		if repoRoot == "" {
			var err error
			repoRoot, err = gitutil.RepoRoot()
			if err != nil {
				return fmt.Errorf("could not determine repo root: %w", err)
			}
		}

		srv := daemon.NewServer(repoRoot)
		return srv.Start()
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return fmt.Errorf("could not determine repo root: %w", err)
		}

		if daemon.DaemonStatus(repoRoot) == "stopped" {
			fmt.Fprintln(os.Stdout, "Daemon was not running.")
			return nil
		}
		if err := daemon.StopDaemon(repoRoot); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Daemon stopped.")
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the daemon is running",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return fmt.Errorf("could not determine repo root: %w", err)
		}

		status := daemon.DaemonStatus(repoRoot)
		fmt.Fprintf(os.Stdout, "Daemon: %s\n", status)
		return nil
	},
}

func init() {
	daemonStartCmd.Flags().String("repo-root", "", "Path to the git repository root")
	DaemonCmd.AddCommand(daemonStartCmd)
	DaemonCmd.AddCommand(daemonStopCmd)
	DaemonCmd.AddCommand(daemonStatusCmd)
}
