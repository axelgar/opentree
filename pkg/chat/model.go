// Package chat is opentree's Agent Client Protocol conversation view: an
// altscreen Bubble Tea program that owns an agent subprocess and renders its
// turn as it happens.
//
// It runs inside a worktree's tmux window, which is what lets a single opentree
// binary be both the workspace list and the thing you attach to.
package chat

import (
	"context"
	"encoding/base64"
	"errors"
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
	Workspace string // display name of the worktree
	Cwd       string // worktree directory the session is rooted in
	Version   string // opentree version, sent as clientInfo

	// Agent is the registry entry this chat drives — never nil, because the
	// caller refuses to open a chat for an agent outside the registry.
	// Everything the chat needs about the agent — its binary, its ACP args,
	// its login command, its branding — is read from here, rather than
	// travelling as six separate fields that were all projections of it.
	Agent *config.PredefinedAgent

	// SocketPath is the control socket the workspace list connects to. Empty
	// disables it.
	SocketPath string

	// SessionID is an existing conversation to resume. Empty starts a new one.
	SessionID string

	// KnownSessions are the conversations opentree has already opened in this
	// worktree. They are what /resume offers an agent that cannot enumerate its
	// own — with one that can, the agent's list is merged over them.
	KnownSessions []acp.SessionInfo

	// SaveSession records the workspace's current conversation: so the next
	// launch resumes it rather than forgetting, and so it can be found again by
	// name once it is no longer the current one.
	SaveSession func(acp.SessionInfo) error

	// ForgetSession drops one from that record, for a conversation the agent
	// itself no longer has. Offering it again would resume into "not found".
	ForgetSession func(id string) error
}

// acpBinary is the program that serves ACP, which is not always the agent's
// own: Claude Code is reached through an adapter. Resolved per launch rather
// than once, because installing the adapter moves it — a path resolved before
// the install is stale immediately after it.
func (o Options) acpBinary() string {
	if b := o.Agent.ResolveACPCommand(); b != "" {
		return b
	}
	return o.Agent.Command
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
		client, err := acp.Spawn(ctx, opts.acpBinary(), opts.Agent.ACPArgs(opts.Cwd), opts.Cwd, handlers)
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
		fm.endSession()
		_ = fm.client.Close()
	}
	return err
}

// closeTimeout bounds the goodbye. The process is killed immediately after, so
// an agent that will not answer costs nothing worth waiting on.
const closeTimeout = 2 * time.Second

// endSession tells the agent the conversation is over, for the agents that take
// being told. Everything opentree holds is on disk by now; this is the agent's
// own bookkeeping, and the alternative is a SIGKILL with no warning.
//
// A conversation nobody had is dropped from the ledger as well. Copilot deletes
// an empty session when it is closed — reasonably, there is nothing in it — and
// an id offered afterwards resumes into "not found", which reads to the user as
// a conversation that was lost rather than one that never existed.
func (m Model) endSession() {
	say, forget := m.closeOnExit()
	if !say {
		return
	}
	// A context of its own: the chat's is a parent of everything that has just
	// been torn down, and may already be cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	if err := m.client.CloseSession(ctx, m.sessionID); err != nil {
		return
	}
	if forget && m.opts.ForgetSession != nil {
		_ = m.opts.ForgetSession(m.sessionID)
	}
}

// closeOnExit is what leaving the chat owes the agent: whether there is a
// conversation to say goodbye to, and whether its id survives the goodbye.
func (m Model) closeOnExit() (say, forget bool) {
	if m.sessionID == "" || !m.canCloseSession || m.client == nil {
		return false, false
	}
	return true, !m.spoken()
}

// spoken reports whether this conversation has anything in it — a prompt sent,
// or a history resumed. It rides on titled because the two answer the same
// question: a conversation is named after the first thing said to it.
func (m Model) spoken() bool { return m.titled }

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

	// resumed distinguishes a conversation that was reopened from one that was
	// just created. A reopened one is already named — by the agent, or by what
	// was first said to it — and naming it again after today's prompt would
	// rename it every time somebody attached.
	resumed bool
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

// pastedImageMsg is an image lifted off the system clipboard.
type pastedImageMsg struct {
	block acp.ContentBlock
	err   string
}

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

	// fatal means no session was ever created, so there is nothing to send to
	// and only a fresh agent can change that. Without it the chat sat on
	// "starting…" forever with the restart key unreachable, because the panel
	// offering it only appears for an agent that is stopped.
	fatal bool
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
	entryPlan
)

// entry is one renderable item in the conversation log. Message entries
// accumulate streamed chunks; tool entries are patched in place as the call
// advances through its statuses.
type entry struct {
	kind entryKind
	text string
	tool acp.ToolCall
	plan []acp.PlanEntry
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

	// canResume is whether the agent can reopen a conversation at all, by
	// either session/load or session/resume. An agent that cannot should be
	// told so rather than asked and refused.
	canResume bool

	// canCloseSession is the agent's sessionCapabilities.close, which decides
	// whether leaving the chat says goodbye or just kills the process.
	canCloseSession bool

	// canListSessions is the agent's sessionCapabilities.list. Without it
	// /resume falls back to the conversations opentree recorded itself, which
	// is why the command survives an agent that keeps no directory.
	canListSessions bool

	// canSendImages is the agent's promptCapabilities.image. ACP says a client
	// must restrict what it sends to what the agent declared, so without this an
	// image would be a protocol violation rather than a feature.
	canSendImages bool

	sessionID     string
	configOptions []acp.ConfigOption
	settings      settings
	sessions      sessions
	login         login

	// titled is whether the current conversation already has a name in the
	// ledger, which stops the first prompt of a resumed session from renaming
	// it after whatever was said today.
	titled bool

	entries []entry
	toolIdx map[string]int

	input    textarea.Model
	viewport viewport.Model
	help     help.Model
	keys     keyMap

	commands   []acp.Command
	files      []string
	completion completionState

	// pending holds images pasted but not yet sent. They cannot go into the
	// textarea — there is no path to type, the bytes came off the clipboard —
	// so they wait beside it and lead the next prompt.
	pending []acp.ContentBlock

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

	// loggingIn is set while the agent is authenticating itself over the
	// protocol, which for a browser flow is as long as the browser takes. The
	// panel says so rather than offering [l] again: nothing on screen would
	// otherwise change between pressing it and the browser opening.
	loggingIn bool

	// restarting is set between asking for a new agent and getting one. Bubble
	// Tea runs every command on its own goroutine, so without it a second press
	// of r launches a second agent into this same chat — both replaying history
	// into one log, and both loading a session that allows exactly one client.
	restarting bool

	// newBelow is whether the log grew while the reader was scrolled up. The
	// scroll position alone does not say that: reading back through history is
	// not the same situation as the agent answering somewhere off screen, and
	// only the second one is worth interrupting the read for.
	newBelow bool

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
// Model — a cached copy would be one more field every path that builds a
// Model has to remember to fill.
func (m Model) brand() brand {
	a := m.opts.Agent
	b := brand{mark: a.Brand.Mark, colour: a.Brand.Colour, name: a.Name, logo: a.Brand.Logo}
	if b.mark == "" {
		// An agent with no branding is still named, in grey — inventing a
		// colour for one would make the colours stop meaning anything.
		b.mark = "·"
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
// takes both an agent asking for credentials and a way to give them: a command
// to run, or a login the agent will perform on request.
func (m Model) canLogIn() bool { return m.authNeed && m.canAuthenticate() }

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
	overlayLogin
	overlayStopped
	overlaySettings
	overlaySessions
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
	// Above the stopped panel it was opened from, which is still true underneath
	// it — an agent wanting credentials is exactly when this picker is up.
	case m.login.open:
		return overlayLogin
	case m.stopped():
		return overlayStopped
	case m.settings.open:
		return overlaySettings
	case m.sessions.open:
		return overlaySessions
	case m.showHelp:
		return overlayHelp
	}
	return overlayNone
}

// overlayDef is everything an overlay owes the frame: the key handler it
// takes over, the footer height it needs, and the footer it draws. One row
// per overlay, because these used to be three separate switches that each had
// to be extended in step — a panel added to the keyboard but not the layout
// drew at the wrong height, and the compiler had nothing to say about it.
type overlayDef struct {
	keys   func(Model, tea.KeyMsg) (tea.Model, tea.Cmd)
	height func(Model) int
	view   func(Model) string
}

// Filled in init rather than declared: the handlers reach relayout, relayout
// reaches footerHeight, and footerHeight reads this map — a package-level
// literal would be an initialization cycle.
var overlayDefs map[overlay]overlayDef

func init() {
	overlayDefs = map[overlay]overlayDef{
		overlayPermission: {Model.handlePermissionKey, Model.permissionHeight, Model.permissionView},
		overlayLogin:      {Model.handleLoginKey, Model.loginHeight, Model.loginView},
		overlayStopped:    {Model.handleStoppedKey, Model.stoppedHeight, Model.stoppedView},
		overlaySettings:   {Model.handleSettingsKey, Model.settingsHeight, Model.settingsView},
		overlaySessions:   {Model.handleSessionsKey, Model.sessionsHeight, Model.sessionsView},
		overlayHelp:       {Model.handleHelpKey, Model.helpHeight, Model.helpView},
	}
}

func (m Model) withAgentInfo(info *acp.InitializeResponse) Model {
	if info == nil {
		return m
	}
	m.authMethods = info.AuthMethods
	m.canResume = info.AgentCapabilities.CanReopen()
	m.canListSessions = info.AgentCapabilities.CanList()
	m.canCloseSession = info.AgentCapabilities.CanClose()
	m.canSendImages = info.AgentCapabilities.PromptCapabilities.Image
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
		if want == "" {
			return m.freshSession(client, cwd, "")
		}
		return m.reopenSession(client, want, cwd)
	}
}

// reopenSession opens an existing conversation, falling back to a new one with
// a note saying why. Which protocol method that takes is the client's business,
// not this view's: an agent that only serves session/resume reaches here the
// same way as one that replays its history.
func (m Model) reopenSession(client *acp.Client, id, cwd string) tea.Msg {
	resp, err := client.Reopen(m.ctx, id, cwd)
	switch {
	case err == nil:
		note := ""
		if !resp.Replayed {
			// The agent has the conversation; this view does not. Saying so
			// beats an empty log that looks like a lost one.
			note = "resumed — this agent does not replay what was said before"
		}
		return sessionReadyMsg{id: id, options: resp.ConfigOptions, note: note, resumed: true}

	case errors.Is(err, acp.ErrCannotReopen):
		// Both agents opentree ships with can, so this is for the next one.
		return m.freshSession(client, cwd,
			"this agent cannot reopen a conversation — starting a new one")

	case acp.IsAuthRequired(err):
		return errMsg{err: err, auth: true}
	}

	// A session the agent has forgotten is not worth failing over; the worktree
	// is still the unit of work. The id is left out: it is the agent's
	// bookkeeping, and nothing the reader can act on.
	return m.freshSession(client, cwd,
		"the previous conversation could not be resumed — this is a new one")
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
		return errMsg{err: err, auth: acp.IsAuthRequired(err), fatal: true}
	}
	if m.opts.SaveSession != nil {
		// Dated now: a conversation that has just been opened is the most
		// recent one there is, and an undated row sorts to the bottom of the
		// picker as if it were the oldest.
		info := acp.SessionInfo{SessionID: resp.SessionID, Cwd: cwd, UpdatedAt: time.Now()}
		if err := m.opts.SaveSession(info); err != nil {
			return errMsg{err: fmt.Errorf("failed to record session id: %w", err), fatal: true}
		}
	}
	return sessionReadyMsg{id: resp.SessionID, options: resp.ConfigOptions, note: note}
}

// promptCmd sends blocks that have already been composed. Composing happens in
// startTurn instead, where the log entry is written: the two have to agree
// about what is being sent, and an image only shows up in one of them.
func (m Model) promptCmd(blocks []acp.ContentBlock) tea.Cmd {
	client, sessionID := m.client, m.sessionID
	return func() tea.Msg {
		resp, err := client.Prompt(m.ctx, sessionID, blocks)
		return promptDoneMsg{resp: resp, err: err}
	}
}

// pasteCmd resolves ctrl+v. An image on the clipboard becomes an attachment;
// anything else is the terminal's ordinary paste, handed to the textarea's own
// one so there is a single implementation of pasting text.
//
// It runs off the event loop because asking is not cheap: the macOS route is an
// osascript round trip, measured at ~150ms, and it is paid on every paste
// including the text ones. That is a wait, not a freeze — the chat keeps
// drawing — and it buys one mental model for the key instead of two.
//
// ponytail: a var, so a test can tell this apart from the textarea's own paste
// without a clipboard interface and its one implementation. The textarea also
// answers ctrl+v with a non-nil command, so nothing observable distinguishes
// "opentree claimed the key" from "opentree let it through".
var pasteCmd = func() tea.Cmd {
	return func() tea.Msg {
		data, mime, ok := clipboardImage()
		if !ok {
			return textarea.Paste()
		}
		if int64(len(data)) > maxImageBytes {
			return pastedImageMsg{err: fmt.Sprintf("that image is %s — too large to send",
				humanBytes(int64(len(data))))}
		}
		return pastedImageMsg{block: acp.ImageBlock(base64.StdEncoding.EncodeToString(data), mime)}
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
