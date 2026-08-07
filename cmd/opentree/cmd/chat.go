package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var ChatCmd = &cobra.Command{
	Use:               "chat <branch-name>",
	Short:             "Talk to a workspace's agent over the Agent Client Protocol",
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer stop()
		return runChat(ctx, args[0], cmd.Root().Version)
	},
}

func runChat(ctx context.Context, name, version string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return fmt.Errorf("failed to find repo root: %w", err)
	}

	store, err := state.New(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to open state: %w", err)
	}

	ws, err := store.GetWorkspace(name)
	if err != nil {
		return fmt.Errorf("failed to find workspace %q: %w", name, err)
	}

	agent := config.FindAgent(cfg.Agent.Command)
	if agent == nil || agent.ACP == nil {
		return fmt.Errorf("agent %q has no ACP mode; only %s does",
			cfg.Agent.Command, acpCapableAgents())
	}

	return chat.Run(ctx, chat.Options{
		Workspace:   ws.Name,
		Cwd:         ws.WorktreeDir,
		Agent:       agent.Name,
		Command:     agent.Command,
		Args:        append(append([]string{}, agent.ACP.Args...), agent.ACP.CwdFlag, ws.WorktreeDir),
		Version:     version,
		AuthCommand: agent.ACP.AuthCommand,
		SocketPath:  chat.SocketPath(repoRoot, ws.Name),
		SessionID:   ws.ACPSessionID,
		SaveSession: func(id string) error {
			ws.ACPSessionID = id
			return store.UpdateWorkspace(ws)
		},
	})
}

func acpCapableAgents() string {
	var names []string
	for i := range config.PredefinedAgents {
		if config.PredefinedAgents[i].ACP != nil {
			names = append(names, config.PredefinedAgents[i].Command)
		}
	}
	return strings.Join(names, ", ")
}
