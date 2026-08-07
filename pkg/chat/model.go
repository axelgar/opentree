// Package chat is opentree's Agent Client Protocol conversation view: an
// altscreen Bubble Tea program that owns an agent subprocess and renders its
// turn as it happens.
//
// It runs inside a worktree's tmux window, which is what lets a single opentree
// binary be both the workspace list and the thing you attach to.
package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// Options configure one chat session.
type Options struct {
	Workspace string   // display name of the worktree
	Cwd       string   // worktree directory the session is rooted in
	Agent     string   // agent display name, e.g. "OpenCode"
	Command   string   // agent binary
	Args      []string // ACP args, including the cwd flag and its value
	Version   string   // opentree version, sent as clientInfo

	// SessionID is an existing conversation to resume. Empty starts a new one.
	SessionID string

	// SaveSession records a newly created session id so the next launch
	// resumes instead of forgetting.
	SaveSession func(string) error
}

// Run starts the view and owns the agent process for its lifetime.
func Run(ctx context.Context, opts Options) error {
	msgs := make(chan tea.Msg, 64)
	quit := make(chan struct{})

	// send never blocks past the end of the program: a handler still holding a
	// message when the user quits would otherwise pin the read loop forever.
	send := func(m tea.Msg) {
		select {
		case msgs <- m:
		case <-quit:
		case <-ctx.Done():
		}
	}

	client, err := acp.Spawn(ctx, opts.Command, opts.Args, opts.Cwd, acp.Handlers{
		Update: func(u acp.SessionUpdate) { send(acpUpdateMsg(u)) },
		Permission: func(req acp.PermissionRequest) string {
			reply := make(chan string, 1)
			send(permissionMsg{req: req, reply: reply})
			select {
			case id := <-reply:
				return id
			case <-quit:
				return ""
			case <-ctx.Done():
				return ""
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to start %s: %w", opts.Command, err)
	}
	defer func() { _ = client.Close() }()

	info, err := client.Initialize(ctx, "opentree", opts.Version)
	if err != nil {
		return fmt.Errorf("ACP handshake failed: %w", err)
	}

	go func() {
		<-client.Done()
		send(agentGoneMsg{})
	}()

	p := tea.NewProgram(newModel(ctx, client, info, opts, msgs), tea.WithAltScreen())
	_, err = p.Run()
	close(quit)
	return err
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

type acpUpdateMsg acp.SessionUpdate

// permissionMsg carries an escalation and the channel the agent is blocked on.
// Exactly one value must be sent to reply, or the turn hangs.
type permissionMsg struct {
	req   acp.PermissionRequest
	reply chan string
}

type agentGoneMsg struct{}

type sessionReadyMsg struct {
	id      string
	options []acp.ConfigOption
	note    string
}

type promptDoneMsg struct {
	resp *acp.PromptResponse
	err  error
}

type spinnerTickMsg struct{}

type errMsg struct{ err error }

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type entryKind int

const (
	entryUser entryKind = iota
	entryAgent
	entryThought
	entryTool
	entryNotice
)

// entry is one renderable item in the conversation log. Message entries
// accumulate streamed chunks; tool entries are patched in place as the call
// advances through its statuses.
type entry struct {
	kind entryKind
	text string
	tool acp.ToolCall
}

type Model struct {
	ctx    context.Context
	client *acp.Client
	msgs   <-chan tea.Msg
	opts   Options

	agentVersion string
	authMethods  []acp.AuthMethod

	sessionID string
	modelName string

	entries []entry
	toolIdx map[string]int

	input    textarea.Model
	viewport viewport.Model
	help     help.Model
	keys     keyMap

	perm         *permissionMsg
	turn         bool
	spinnerFrame int
	usage        *acp.ContextUsage

	width, height int
	ready         bool
	err           error
}

func newModel(ctx context.Context, client *acp.Client, info *acp.InitializeResponse, opts Options, msgs <-chan tea.Msg) Model {
	ta := textarea.New()
	ta.Placeholder = "Ask the agent…"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.CharLimit = 0
	// Enter sends, so the textarea's own newline binding moves out of the way.
	ta.KeyMap.InsertNewline.SetKeys(keys.Newline.Keys()...)
	ta.Focus()

	m := Model{
		ctx:     ctx,
		client:  client,
		msgs:    msgs,
		opts:    opts,
		toolIdx: make(map[string]int),
		input:   ta,
		help:    help.New(),
		keys:    keys,
	}
	if info != nil {
		m.authMethods = info.AuthMethods
		if info.AgentInfo != nil {
			m.agentVersion = info.AgentInfo.Version
		}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForMsg(m.msgs), m.startSession(), textarea.Blink)
}

// waitForMsg turns the ACP handler channel into a Bubble Tea command. Update
// re-issues it after every delivery, which is what keeps the stream flowing.
func waitForMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startSession resumes the workspace's conversation, or opens one and records
// its id. Resuming replays the whole history through Handlers.Update before it
// returns, so the log fills in while this command is still running.
func (m Model) startSession() tea.Cmd {
	return func() tea.Msg {
		if m.opts.SessionID != "" {
			resp, err := m.client.LoadSession(m.ctx, m.opts.SessionID, m.opts.Cwd)
			if err == nil {
				return sessionReadyMsg{id: m.opts.SessionID, options: resp.ConfigOptions}
			}
			if acp.IsAuthRequired(err) {
				return errMsg{err}
			}
			// A session the agent has forgotten is not worth failing over; the
			// worktree is still the unit of work.
			note := fmt.Sprintf("could not resume %s — started a new conversation", m.opts.SessionID)
			resp2, err2 := m.client.NewSession(m.ctx, m.opts.Cwd)
			if err2 != nil {
				return errMsg{err2}
			}
			if err := m.saveSession(resp2.SessionID); err != nil {
				return errMsg{err}
			}
			return sessionReadyMsg{id: resp2.SessionID, options: resp2.ConfigOptions, note: note}
		}

		resp, err := m.client.NewSession(m.ctx, m.opts.Cwd)
		if err != nil {
			return errMsg{err}
		}
		if err := m.saveSession(resp.SessionID); err != nil {
			return errMsg{err}
		}
		return sessionReadyMsg{id: resp.SessionID, options: resp.ConfigOptions}
	}
}

func (m Model) saveSession(id string) error {
	if m.opts.SaveSession == nil {
		return nil
	}
	if err := m.opts.SaveSession(id); err != nil {
		return fmt.Errorf("failed to record session id: %w", err)
	}
	return nil
}

func (m Model) promptCmd(text string) tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.Prompt(m.ctx, m.sessionID, text)
		return promptDoneMsg{resp: resp, err: err}
	}
}

func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}
