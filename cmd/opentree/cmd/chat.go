package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/acp"
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
		Workspace:   ws.Name,
		Cwd:         ws.WorktreeDir,
		Agent:       agent.Name,
		Command:     agent.Command,
		Binary:      agent.ResolveACPCommand,
		Args:        agent.ACPArgs(ws.WorktreeDir),
		InstallHint: installHint(agent),
		Version:     version,
		AuthCommand: agent.ACP.AuthCommand,
		SocketPath:  chat.SocketPath(repoRoot, ws.Name),
		SessionID:   resumableSession(ws, agent.Command),

		KnownSessions: knownSessions(ws, agent.Command),
		SaveSession: func(s acp.SessionInfo) error {
			ws.RecordSession(state.ACPSession{
				Agent:     agent.Command,
				ID:        s.SessionID,
				Title:     s.Title,
				UpdatedAt: s.UpdatedAt,
			})
			return store.UpdateWorkspace(ws)
		},
	})
}

// knownSessions is what opentree recorded for this workspace, narrowed to the
// agent about to run: a session id is that agent's own bookkeeping, and
// offering one to a different agent gets a failed load rather than somebody
// else's conversation.
//
// It is the floor under /resume. An agent that serves session/list keeps a
// better directory than this one and it is merged over the top; an agent that
// does not still has something to offer.
func knownSessions(ws *state.Workspace, agentCommand string) []acp.SessionInfo {
	var out []acp.SessionInfo
	for _, s := range ws.ACPSessions {
		// An entry from before opentree recorded which agent made it belongs to
		// whichever agent the workspace was already using.
		if s.Agent != "" && s.Agent != agentCommand {
			continue
		}
		out = append(out, acp.SessionInfo{
			SessionID: s.ID,
			Cwd:       ws.WorktreeDir,
			Title:     s.Title,
			UpdatedAt: s.UpdatedAt,
		})
	}

	// A workspace that predates the ledger has only its current session id. It
	// is still a conversation, and it is the one most likely to be wanted.
	if id := resumableSession(ws, agentCommand); id != "" && !slices.ContainsFunc(out,
		func(s acp.SessionInfo) bool { return s.SessionID == id }) {
		out = append(out, acp.SessionInfo{SessionID: id, Cwd: ws.WorktreeDir})
	}
	return out
}

// resumableSession is the conversation to reopen on launch: the workspace's
// current one, unless a different agent made it. A session id is that agent's
// own bookkeeping, so handing OpenCode's to Copilot fails the load and reports
// a conversation that could not be resumed — when the truthful answer is that
// this agent has not had one here yet.
//
// An id recorded before opentree tracked which agent made it belongs to
// whichever agent the workspace was already using, same as in knownSessions.
func resumableSession(ws *state.Workspace, agentCommand string) string {
	for _, s := range ws.ACPSessions {
		if s.ID != ws.ACPSessionID {
			continue
		}
		if s.Agent != "" && s.Agent != agentCommand {
			return ""
		}
		break
	}
	return ws.ACPSessionID
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
	if agent == nil {
		return nil, fmt.Errorf("opentree cannot drive %q — it speaks the Agent Client Protocol, and only these agents do: %s",
			name, strings.Join(config.AgentCommands(), ", "))
	}
	// A missing adapter is deliberately not an error here: the chat opens in its
	// stopped state instead, where installing it is one key away.
	return agent, nil
}

// installHint points at where the adapter is installed from. Not a key to press
// here: installing belongs with choosing an agent.
func installHint(agent *config.PredefinedAgent) string {
	if len(agent.ACPInstallCommand()) == 0 {
		return ""
	}
	size := ""
	if agent.ACP.InstallSize != "" {
		size = " (" + agent.ACP.InstallSize + ")"
	}
	return fmt.Sprintf("install it%s from opentree's agent list — press A, then i", size)
}
