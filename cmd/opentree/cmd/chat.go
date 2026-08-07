package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/acp"
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

	sess := newChatSession()
	args := append(append([]string{}, agent.ACP.Args...), agent.ACP.CwdFlag, ws.WorktreeDir)
	client, err := acp.Spawn(ctx, agent.Command, args, ws.WorktreeDir, acp.Handlers{
		Update:     sess.onUpdate,
		Permission: sess.onPermission,
	})
	if err != nil {
		return fmt.Errorf("failed to start %s: %w", agent.Command, err)
	}
	defer func() { _ = client.Close() }()

	info, err := client.Initialize(ctx, "opentree", version)
	if err != nil {
		return fmt.Errorf("ACP handshake failed: %w", err)
	}

	// Print the header before touching the session: resuming replays the whole
	// conversation through the update handler, so anything printed afterwards
	// would land underneath the history it is meant to introduce.
	fmt.Printf("%s %s · %s\n", agent.Name, agentVersion(info), ws.Name)
	if ws.ACPSessionID != "" {
		fmt.Printf("resuming %s\n\n", ws.ACPSessionID)
	}

	sessionID, opts, err := resumeOrCreate(ctx, client, store, ws)
	if err != nil {
		if acp.IsAuthRequired(err) {
			return fmt.Errorf("%s is not authenticated — %s", agent.Command, authHint(info))
		}
		return err
	}

	fmt.Printf("\nsession %s · %s · ctrl-c to quit\n", sessionID, currentValue(opts, "model"))

	return sess.repl(ctx, client, sessionID)
}

// resumeOrCreate reopens the workspace's existing conversation, or starts one
// and records its id so the next launch resumes instead of forgetting.
func resumeOrCreate(ctx context.Context, client *acp.Client, store *state.Store, ws *state.Workspace) (string, []acp.ConfigOption, error) {
	if ws.ACPSessionID != "" {
		resp, err := client.LoadSession(ctx, ws.ACPSessionID, ws.WorktreeDir)
		if err == nil {
			return ws.ACPSessionID, resp.ConfigOptions, nil
		}
		if acp.IsAuthRequired(err) {
			return "", nil, err
		}
		// A session the agent no longer knows about is not worth failing over;
		// the worktree is still the unit of work, so start a fresh one.
		fmt.Fprintf(os.Stderr, "could not resume session %s (%v) — starting a new one\n", ws.ACPSessionID, err)
	}

	resp, err := client.NewSession(ctx, ws.WorktreeDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to start session: %w", err)
	}
	ws.ACPSessionID = resp.SessionID
	if err := store.UpdateWorkspace(ws); err != nil {
		return "", nil, fmt.Errorf("failed to record session id: %w", err)
	}
	return resp.SessionID, resp.ConfigOptions, nil
}

// chatSession renders one conversation as plain lines and answers permission
// prompts on stdin.
//
// ponytail: line-based on purpose. This exists to make the ACP client
// dogfoodable; the altscreen view replaces it wholesale.
type chatSession struct {
	mu    sync.Mutex // serializes stdout across the read loop and the REPL
	in    *bufio.Scanner
	tools map[string]*toolState
	usage *acp.ContextUsage
}

type toolState struct {
	announced bool
	call      acp.ToolCall
}

func newChatSession() *chatSession {
	return &chatSession{
		in:    bufio.NewScanner(os.Stdin),
		tools: make(map[string]*toolState),
	}
}

func (s *chatSession) repl(ctx context.Context, client *acp.Client, sessionID string) error {
	for {
		fmt.Print("\n> ")
		line, ok := s.readLine()
		if !ok {
			return nil
		}
		if line = strings.TrimSpace(line); line == "" {
			continue
		}

		resp, err := client.Prompt(ctx, sessionID, line)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("prompt failed: %w", err)
		}
		s.printTurnEnd(resp)
	}
}

// readLine is only ever called from one goroutine at a time: the REPL blocks
// inside Prompt for exactly as long as a permission handler might be reading.
func (s *chatSession) readLine() (string, bool) {
	if !s.in.Scan() {
		return "", false
	}
	return s.in.Text(), true
}

func (s *chatSession) printf(format string, a ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf(format, a...)
}

func (s *chatSession) onUpdate(u acp.SessionUpdate) {
	switch u.Type {
	case acp.UpdateAgentMessage:
		// Chunks are fragments of one message; print them as they land.
		s.printf("%s", u.Message.Content.Text)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		s.onToolCall(u)
	case acp.UpdateUsage:
		s.mu.Lock()
		s.usage = u.Usage
		s.mu.Unlock()
	}
}

func (s *chatSession) onToolCall(u acp.SessionUpdate) {
	s.mu.Lock()
	st, ok := s.tools[u.ToolCall.ToolCallID]
	if !ok {
		st = &toolState{}
		s.tools[u.ToolCall.ToolCallID] = st
	}
	st.call.Merge(*u.ToolCall)
	call := st.call
	announced := st.announced

	switch call.Status {
	case acp.StatusInProgress:
		st.announced = true
	case acp.StatusCompleted, acp.StatusFailed:
		st.announced = true
	}
	s.mu.Unlock()

	switch call.Status {
	case acp.StatusInProgress:
		if !announced {
			s.printf("\n  · %s\n", toolLabel(call))
		}
	case acp.StatusCompleted:
		s.printf("\n  ✓ %s\n", toolLabel(call))
	case acp.StatusFailed:
		s.printf("\n  ✗ %s\n", toolLabel(call))
	}
}

func (s *chatSession) onPermission(req acp.PermissionRequest) string {
	s.mu.Lock()
	fmt.Printf("\n  permission: %s\n", toolLabel(req.ToolCall))
	// Options come from the wire: agents disagree on which they offer, and
	// opencode has no reject_always.
	for i, o := range req.Options {
		fmt.Printf("    [%d] %s\n", i+1, o.Name)
	}
	fmt.Print("    choose: ")
	s.mu.Unlock()

	line, ok := s.readLine()
	if !ok {
		return ""
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(req.Options) {
		s.printf("    cancelled\n")
		return ""
	}
	return req.Options[n-1].OptionID
}

func (s *chatSession) printTurnEnd(resp *acp.PromptResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := []string{resp.StopReason}
	if resp.Usage != nil {
		parts = append(parts, fmt.Sprintf("%d in / %d out", resp.Usage.InputTokens, resp.Usage.OutputTokens))
	}
	if s.usage != nil && s.usage.Cost != nil {
		parts = append(parts, fmt.Sprintf("$%.4f", s.usage.Cost.Amount))
	}
	fmt.Printf("\n  %s\n", strings.Join(parts, " · "))
}

// toolLabel is the one-line description of a tool call. Titles are already
// human-facing (agents rewrite them from "bash" to the actual command as the
// call resolves), so the kind only earns a place when it adds something.
func toolLabel(call acp.ToolCall) string {
	label := call.Title
	if label == "" {
		label = call.Kind
	}
	if paths := diffPaths(call); len(paths) > 0 {
		return fmt.Sprintf("%s (%s)", label, strings.Join(paths, ", "))
	}
	return label
}

func diffPaths(call acp.ToolCall) []string {
	var paths []string
	for _, c := range call.Content {
		if c.Type == "diff" && c.Path != "" {
			paths = append(paths, shortPath(c.Path))
		}
	}
	return paths
}

func shortPath(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel := strings.TrimPrefix(path, wd+"/"); rel != path {
			return rel
		}
	}
	return path
}

func currentValue(opts []acp.ConfigOption, id string) string {
	for _, o := range opts {
		if o.ID == id {
			return o.CurrentValue
		}
	}
	return "default model"
}

func agentVersion(info *acp.InitializeResponse) string {
	if info.AgentInfo == nil {
		return ""
	}
	return info.AgentInfo.Version
}

// authHint surfaces the agent's own login instructions rather than guessing at
// a command opentree cannot run for the user.
func authHint(info *acp.InitializeResponse) string {
	for _, m := range info.AuthMethods {
		if m.Description != "" {
			return m.Description
		}
	}
	return "authenticate with the agent and try again"
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
