package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/diag"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/notify"
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
		Workspace:  ws.Name,
		Cwd:        ws.WorktreeDir,
		Agent:      agent,
		Version:    version,
		SocketPath: chat.SocketPath(repoRoot, ws.Name),
		SessionID:  resumableSession(ws, agent.Command),
		Setup:      setupPhase(repoRoot, store, ws),
		Notify:     notifier(repoRoot, ws.Name),

		KnownSessions: knownSessions(ws, agent.Command),
		SaveSession: func(s acp.SessionInfo) error {
			return store.Update(name, func(w *state.Workspace) error {
				w.RecordSession(state.ACPSession{
					Agent:     agent.Command,
					ID:        s.SessionID,
					Title:     s.Title,
					UpdatedAt: s.UpdatedAt,
				})
				return nil
			})
		},
		ForgetSession: func(id string) error {
			return store.Update(name, func(w *state.Workspace) error {
				w.ForgetSession(id)
				return nil
			})
		},
	})
}

// notifier is what carries this chat's moments out of the window they happen
// in, or nil for a chat that carries nothing.
//
// It is built here rather than in the chat for the reason setupPhase is: the
// chat is handed answers, not the machinery that produced them. What it gets
// back is one function that takes a state reading — everything about tmux
// panes and cooldowns stays on this side of it.
func notifier(repoRoot, workspace string) func(notify.Signal) {
	// Outside tmux there is no window to carry anything out of, and whoever
	// started this is sitting in front of it.
	pane := notify.Pane()
	if pane == "" {
		return nil
	}

	// A config that will not parse is reported by every other command that
	// reads it; here it means the defaults, rather than a chat that silently
	// stopped notifying. [notify] is global-only, and Load is where the
	// repository's copy of the section is dropped.
	cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
	if err != nil {
		// Deliberately not surfaced here — but a chat that quietly stopped
		// notifying, because a comma is missing three directories away, is
		// undiagnosable without a record of it.
		diag.Log("chat", "config would not parse; using defaults for [notify]",
			"repo", repoRoot, "err", err)
		cfg = config.Default()
	}

	senders := notify.Senders{notify.Bell{}}
	if cfg.Notify.Desktop == nil || *cfg.Notify.Desktop {
		senders = append(senders, notify.Desktop{})
	}

	w := notify.New(notify.Options{
		Workspace: workspace,
		On:        cfg.Notify.On,
		Send:      senders,
		Watched:   func() bool { return notify.Watched(pane) },
	})
	if w == nil {
		return nil
	}
	return w.Observe
}

// setupPhase is the bootstrap work this chat has to do before the agent
// starts, or the zero value when there is none.
//
// The config is read from the repository root rather than the worktree, for the
// reason resolveACPAgent gives: a worktree's own opentree.toml is a checked-out
// file that the branch may have edited, and the commands opentree runs are the
// repository's, not the branch's.
//
// Deciding here rather than in the chat is what keeps that view free of
// workspaces and trust files. It answers three questions the repository knows —
// has this worktree already run these exact commands, has this machine approved
// them, and how is either recorded — and hands over the answers.
func setupPhase(repoRoot string, store *state.Store, ws *state.Workspace) chat.Setup {
	cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
	if err != nil || len(cfg.Workspace.Setup) == 0 {
		return chat.Setup{}
	}

	commands, run := cfg.Workspace.Setup, cfg.Workspace.Run
	hash := bootstrap.Hash(commands, run)
	// Already done, by these exact commands. A chat starts many times per
	// workspace, and running the install on each would make attaching cost a
	// minute.
	if !ws.SetupAt.IsZero() && ws.SetupHash == hash {
		return chat.Setup{}
	}

	return chat.Setup{
		Commands: commands,
		Run:      run,
		Trusted:  bootstrap.Trusted(repoRoot, commands, run),
		Approve:  func() error { return bootstrap.Approve(repoRoot, commands, run) },
		Record: func() error {
			return store.Update(ws.Name, func(w *state.Workspace) error {
				w.SetupAt, w.SetupHash = time.Now(), hash
				return nil
			})
		},
	}
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
		return nil, config.UnknownAgentError(name)
	}
	// A missing adapter is deliberately not an error here: the chat opens in its
	// stopped state instead, where installing it is one key away.
	return agent, nil
}
