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
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/config"
)

// Options configure one chat session.
type Options struct {
	Workspace string   // display name of the worktree
	Cwd       string   // worktree directory the session is rooted in
	Agent     string   // agent display name, e.g. "OpenCode"
	Command   string   // the agent's own binary — what to log in with, and what to name
	Args      []string // ACP args, including the cwd flag and its value
	Version   string   // opentree version, sent as clientInfo

	// Binary returns the program that serves ACP, which is not always the
	// agent's own: Claude Code is reached through an adapter. Resolved per
	// launch rather than once, because installing the adapter moves it — a
	// path resolved before the install is stale immediately after it.
	Binary func() string

	// AuthCommand logs the agent in interactively, run in this terminal when
	// the agent reports it needs credentials.
	AuthCommand []string

	// InstallHint says where to get the agent's ACP adapter. The chat states
	// the problem; installing belongs with choosing an agent, not inside a
	// conversation that cannot start.
	InstallHint string

	// SocketPath is the control socket the workspace list connects to. Empty
	// disables it.
	SocketPath string

	// SessionID is an existing conversation to resume. Empty starts a new one.
	SessionID string

	// SaveSession records a newly created session id so the next launch
	// resumes instead of forgetting.
	SaveSession func(string) error
}

// acpBinary is the program to spawn: the resolver when one is set, otherwise
// the agent's own binary.
func (o Options) acpBinary() string {
	if o.Binary != nil {
		if b := o.Binary(); b != "" {
			return b
		}
	}
	return o.Command
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
		client, err := acp.Spawn(ctx, opts.acpBinary(), opts.Args, opts.Cwd, handlers)
		if err != nil {
			// Spawn already names the command; wrapping again produced
			// "failed to start X: start X: exec: ...".
			return nil, nil, gen, err
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

	// A failed first launch is not fatal: the view opens in its stopped state,
	// where restarting or installing the adapter is one key away. Returning an
	// error here would print it to a tmux window that then closes.
	client, info, gen, err := launch()

	m := newModel(ctx, client, info, opts, msgs)
	m.generation = gen
	m.launch = launch
	if err != nil {
		m.dead, m.err = true, err
	}

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

// Mouse cell motion is not about clicking — it is what makes the terminal hand
// scroll events to the program instead of scrolling its own buffer. Without it
// the wheel walks back out of the alt screen into whatever the shell printed
// before opentree started, which is the opposite of feeling like an app. The
// dashboard captures the mouse for the same reason.
func p(m Model) *tea.Program {
	return tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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

// configChangedMsg is the agent's answer to a settings change. It carries the
// whole set back, since changing one option can change another.
type configChangedMsg struct {
	configID string
	value    string
	options  []acp.ConfigOption
	err      error
}

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

	sessionID     string
	configOptions []acp.ConfigOption
	settings      settings

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

	// perms holds escalations in arrival order; the first is the one on screen.
	// More than one can be in flight — a subagent asks independently of the turn
	// that spawned it — and each is a separate request blocked on its own reply
	// channel, so a second must not replace the first. A dropped one is never
	// answered, and ACP has no outcome meaning "lost".
	perms []permissionMsg

	turn         bool
	turnStart    time.Time
	spinnerFrame int
	usage        *acp.ContextUsage

	// hideThoughts collapses the agent's reasoning, which is noise when you
	// are following what it did rather than why.
	hideThoughts bool

	// showHelp swaps the input for the full key list.
	showHelp bool

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

// brand is an agent's identity on screen: its glyph, its colour, the name it
// prefers over whatever the workspace recorded ("claude" is stored, "Claude
// Code" is shown), and its drawing.
type brand struct {
	mark, colour, name string
	logo               []string
}

// brand resolves the agent's identity on demand rather than caching it on the
// Model. The lookup is a six-entry scan, and a cached copy would be one more
// field every path that builds a Model has to remember to fill.
func (m Model) brand() brand {
	var b brand
	b.mark, b.colour, b.name = config.Brand(m.opts.Agent)
	if a := config.FindAgent(m.opts.Agent); a != nil {
		b.logo = a.Logo
	}
	if len(b.logo) == 0 {
		// An agent with no drawing falls back to its one glyph, so the opening
		// screen's layout never has a hole where the logo goes.
		b.logo = []string{"", "  " + b.mark, ""}
	}
	return b
}

// paint applies the agent's colour, and leaves the style alone for an agent
// opentree does not know — inventing a colour for one would make the colours
// stop meaning anything.
func (b brand) paint(s lipgloss.Style) lipgloss.Style {
	if b.colour == "" {
		return s
	}
	return s.Foreground(lipgloss.Color(b.colour))
}

// adapterMissing reports whether the agent has never started, which for an
// agent reached through an adapter is what a missing one looks like. An agent
// that started and later died wants a restart, not an install.
func (m Model) adapterMissing() bool { return m.client == nil }

// canLogIn reports whether logging in is a remedy for the current state, which
// takes both an agent asking for credentials and a command to give them with.
func (m Model) canLogIn() bool { return m.authNeed && len(m.opts.AuthCommand) > 0 }

// perm is the escalation on screen: the oldest one still unanswered.
func (m Model) perm() *permissionMsg {
	if len(m.perms) == 0 {
		return nil
	}
	return &m.perms[0]
}

// overlay is the panel that owns both the footer and the keyboard.
type overlay int

const (
	overlayNone overlay = iota
	overlayPermission
	overlayStopped
	overlaySettings
	overlayHelp
)

// overlay decides which panel is up. It exists because the view and the key
// handler each used to decide for themselves, in different orders: with the
// settings picker open when an escalation arrived, the picker stayed on screen
// while the keyboard answered the permission — so a digit, which is how you
// pick a setting, silently allowed a tool call nobody was shown.
//
// A permission outranks everything: an agent is blocked on the answer, and the
// dialog says how to say no.
func (m Model) overlay() overlay {
	switch {
	case m.perm() != nil:
		return overlayPermission
	case m.stopped():
		return overlayStopped
	case m.settings.open:
		return overlaySettings
	case m.showHelp:
		return overlayHelp
	}
	return overlayNone
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
	if client == nil {
		return nil // nothing started; the stopped panel is already showing why
	}
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
			// worktree is still the unit of work. The id is left out: it is the
			// agent's bookkeeping, and nothing the reader can act on.
			return m.freshSession(client, cwd,
				"the previous conversation could not be resumed — this is a new one")
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
