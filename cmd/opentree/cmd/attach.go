package cmd

import (
	"fmt"



	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/spf13/cobra"
)

var AttachCmd = &cobra.Command{
	Use:   "attach <branch-name>",
	Short: "Attach to a workspace's tmux window",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]

		cfg, err := config.Load("")

		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		tmuxCtrl := tmux.New(cfg.Tmux.SessionPrefix)

		if err := tmuxCtrl.AttachWindow(branchName); err != nil {
			return fmt.Errorf("failed to attach to workspace: %w", err)
		}

		return nil
	},
}
