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
	"os/exec"
	"strings"
	"sync/atomic"
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

	// AuthCommand logs the agent in interactively, run in this terminal when
	// the agent reports it needs credentials.
	AuthCommand []string

	// SocketPath is the control socket the workspace list connects to. Empty
	// disables it.
	SocketPath string

	// SessionID is an existing conversation to resume. Empty starts a new one.
	SessionID string

	// SaveSession records a newly created session id so the next launch
	// resumes instead of forgetting.
	SaveSession func(string) error
}

// launcher starts a fresh agent process and completes its handshake. Restart
// needs this, which is why spawning is a closure rather than inline in Run.
type launcher func() (*acp.Client, *acp.InitializeResponse, int, error)

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

	handlers := acp.Handlers{
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
	}

	// generation distinguishes a restarted agent from the one it replaced, so
	// the old process's death notice does not mark the new one as gone.
	var generation atomic.Int64
	launch := func() (*acp.Client, *acp.InitializeResponse, int, error) {
		gen := int(generation.Add(1))
		client, err := acp.Spawn(ctx, opts.Command, opts.Args, opts.Cwd, handlers)
		if err != nil {
			return nil, nil, gen, fmt.Errorf("failed to start %s: %w", opts.Command, err)
		}
		info, err := client.Initialize(ctx, "opentree", opts.Version)
		if err != nil {
			_ = client.Close()
			return nil, nil, gen, fmt.Errorf("ACP handshake failed: %w", err)
		}
		go func() {
			<-client.Done()
			send(agentGoneMsg{generation: gen})
		}()
		return client, info, gen, nil
	}

	client, info, gen, err := launch()
	if err != nil {
		return err
	}

	m := newModel(ctx, client, info, opts, msgs)
	m.generation = gen
	m.launch = launch

	// Best-effort: a chat with no control socket still works, it is just
	// invisible to the workspace list.
	if opts.SocketPath != "" {
		onCommand := func(c Command) Result {
			reply := make(chan Result, 1)
			send(socketCommandMsg{cmd: c, reply: reply})
			select {
			case res := <-reply:
				return res
			case <-quit:
				return Result{Reason: "chat closed"}
			case <-ctx.Done():
				return Result{Reason: "chat closed"}
			}
		}
		if srv, err := serve(opts.SocketPath, onCommand); err == nil {
			defer func() { _ = srv.Close() }()
			m.publish = srv.publish
		}
	}

	final, err := p(m).Run()
	close(quit)
	if fm, ok := final.(Model); ok && fm.client != nil {
		_ = fm.client.Close()
	}
	return err
}

func p(m Model) *tea.Program { return tea.NewProgram(m, tea.WithAltScreen()) }

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

type agentGoneMsg struct{ generation int }

type clientReadyMsg struct {
	client     *acp.Client
	info       *acp.InitializeResponse
	generation int
}

type sessionReadyMsg struct {
	id      string
	options []acp.ConfigOption
	note    string
}

type promptDoneMsg struct {
	resp *acp.PromptResponse
	err  error
}

type authDoneMsg struct{ err error }

type filesLoadedMsg struct{ files []string }

// socketCommandMsg is an instruction from the workspace list, together with the
// channel the sender is blocked on. Exactly one Result must be sent to reply.
type socketCommandMsg struct {
	cmd   Command
	reply chan Result
}

type spinnerTickMsg struct{}

type errMsg struct {
	err  error
	auth bool
}

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
	ctx     context.Context
	client  *acp.Client
	launch  launcher
	msgs    <-chan tea.Msg
	opts    Options
	publish func(Status)

	generation   int
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

	commands   []acp.Command
	files      []string
	completion completionState

	// queued holds a prompt from the workspace list that arrived while the
	// agent was busy or still starting. At most one waits at a time.
	queued string

	perm         *permissionMsg
	turn         bool
	spinnerFrame int
	usage        *acp.ContextUsage

	// hideThoughts collapses the agent's reasoning, which is noise when you
	// are following what it did rather than why.
	hideThoughts bool

	// dead means the agent process is gone and only restart or quit apply.
	dead     bool
	authNeed bool

	width, height int
	ready         bool
	err           error
}

func newModel(ctx context.Context, client *acp.Client, info *acp.InitializeResponse, opts Options, msgs <-chan tea.Msg) Model {
	ta := textarea.New()
	ta.Placeholder = "Ask the agent…"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(inputHeight)
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
	return m.withAgentInfo(info)
}

func (m Model) withAgentInfo(info *acp.InitializeResponse) Model {
	if info == nil {
		return m
	}
	m.authMethods = info.AuthMethods
	if info.AgentInfo != nil {
		m.agentVersion = info.AgentInfo.Version
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForMsg(m.msgs), m.startSession(), loadFilesCmd(m.opts.Cwd), textarea.Blink)
}

// loadFilesCmd lists the worktree's tracked files for @-mention completion.
// git already knows which files matter, which beats walking the tree and
// reimplementing ignore rules.
func loadFilesCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", cwd, "ls-files").Output()
		if err != nil {
			return filesLoadedMsg{}
		}
		var files []string
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" {
				files = append(files, line)
			}
		}
		return filesLoadedMsg{files: files}
	}
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
	client, cwd, want := m.client, m.opts.Cwd, m.resumeID()
	return func() tea.Msg {
		if want != "" {
			resp, err := client.LoadSession(m.ctx, want, cwd)
			if err == nil {
				return sessionReadyMsg{id: want, options: resp.ConfigOptions}
			}
			if acp.IsAuthRequired(err) {
				return errMsg{err: err, auth: true}
			}
			// A session the agent has forgotten is not worth failing over; the
			// worktree is still the unit of work.
			note := fmt.Sprintf("could not resume %s — started a new conversation", want)
			return m.freshSession(client, cwd, note)
		}
		return m.freshSession(client, cwd, "")
	}
}

// resumeID is the conversation to reopen: whichever one this view already has,
// falling back to the one recorded for the workspace.
func (m Model) resumeID() string {
	if m.sessionID != "" {
		return m.sessionID
	}
	return m.opts.SessionID
}

func (m Model) freshSession(client *acp.Client, cwd, note string) tea.Msg {
	resp, err := client.NewSession(m.ctx, cwd)
	if err != nil {
		return errMsg{err: err, auth: acp.IsAuthRequired(err)}
	}
	if m.opts.SaveSession != nil {
		if err := m.opts.SaveSession(resp.SessionID); err != nil {
			return errMsg{err: fmt.Errorf("failed to record session id: %w", err)}
		}
	}
	return sessionReadyMsg{id: resp.SessionID, options: resp.ConfigOptions, note: note}
}

func (m Model) promptCmd(text string) tea.Cmd {
	client, sessionID := m.client, m.sessionID
	blocks := composePrompt(text, m.opts.Cwd, m.trackedFiles())
	return func() tea.Msg {
		resp, err := client.Prompt(m.ctx, sessionID, blocks)
		return promptDoneMsg{resp: resp, err: err}
	}
}

func (m Model) trackedFiles() map[string]bool {
	known := make(map[string]bool, len(m.files))
	for _, f := range m.files {
		known[f] = true
	}
	return known
}

// restartCmd replaces a dead agent with a fresh process.
func (m Model) restartCmd() tea.Cmd {
	old, launch := m.client, m.launch
	return func() tea.Msg {
		if old != nil {
			_ = old.Close()
		}
		client, info, gen, err := launch()
		if err != nil {
			return errMsg{err: err}
		}
		return clientReadyMsg{client: client, info: info, generation: gen}
	}
}

func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}
