package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

// agentFlag names the agent to run, bypassing config lookup. The launcher
// always sets it: it has already decided which agent this workspace uses, and
// re-deciding here would consult the worktree's own checked-out opentree.toml,
// which can name a different agent than the one that opened the window.
var agentFlag string

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

func init() {
	ChatCmd.Flags().StringVar(&agentFlag, "agent", "",
		"agent to run (defaults to the repository's configured agent)")
}

func runChat(ctx context.Context, name, version string) error {
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

	agent, err := resolveACPAgent(repoRoot)
	if err != nil {
		return err
	}

	return chat.Run(ctx, chat.Options{
		Workspace: ws.Name,
		Cwd:       ws.WorktreeDir,
		Agent:     agent.Name,
		Command:   agent.ResolveACPCommand(),
		Args:      agent.ACPArgs(ws.WorktreeDir),
		Install:   agent.ACPInstallCommand(),
		InstallLabel: fmt.Sprintf("install %s%s", agent.ACPCommand(),
			sizeSuffix(agent.ACP.InstallSize)),
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

// resolveACPAgent picks the agent to run. The --agent flag wins; otherwise the
// config is read from the repository root rather than the working directory,
// because `opentree chat` runs inside a worktree whose own opentree.toml is a
// checked-out file and may disagree with the repository's.
func resolveACPAgent(repoRoot string) (*config.PredefinedAgent, error) {
	name := agentFlag
	if name == "" {
		cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		name = cfg.Agent.Command
	}

	agent := config.FindAgent(name)
	if agent == nil || agent.ACP == nil {
		return nil, fmt.Errorf("agent %q has no ACP mode; only %s does", name, acpCapableAgents())
	}
	// A missing adapter is deliberately not an error here: the chat opens in its
	// stopped state instead, where installing it is one key away.
	return agent, nil
}

// sizeSuffix renders the download size when the registry states one.
func sizeSuffix(size string) string {
	if size == "" {
		return ""
	}
	return " (" + size + ", needs node)"
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
