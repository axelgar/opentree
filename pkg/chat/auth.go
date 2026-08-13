package chat

import (
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// authRemedy is one way out of "the agent wants credentials". Exactly one of
// the two forms is filled: a command to run in this terminal, or a method id
// for the agent to log itself in with.
type authRemedy struct {
	label string
	desc  string

	command string
	args    []string

	methodID string
}

// terminal reports which of the two forms this is.
func (r authRemedy) terminal() bool { return r.command != "" }

// line is the command as it will be run, for the panel that is about to run it.
func (r authRemedy) line() string {
	return strings.TrimSpace(r.command + " " + strings.Join(r.args, " "))
}

// login is the picker over authRemedies, for an agent that offers more than one.
type login struct {
	open   bool
	cursor int
}

// authenticatedMsg is the agent's answer to authenticate.
type authenticatedMsg struct{ err error }

// authRemedies is what the [l] key can actually do, in the order opentree
// trusts them.
//
// A command the agent named itself comes first: Copilot sends its own absolute
// path in _meta, which works whatever PATH this process inherited. The
// registry's command is next, and it is not redundant — Claude Code's adapter
// declares no methods at all and answers authenticate with "Method not
// implemented", so the command opentree recorded is the only thing that logs it
// in. The protocol call is last, for the agent that offers nothing else. That
// ordering also means opencode keeps the terminal login it has always had,
// rather than becoming the first user of a code path on the strength of a
// method id nobody has run.
//
// An agent that mixed the two forms across its methods would show only the
// terminal ones. None does today; the day one is offered, this list is where it
// goes.
func (m Model) authRemedies() []authRemedy {
	var out []authRemedy
	for _, a := range m.authMethods {
		cmd, args, ok := a.TerminalAuth()
		if !ok {
			continue
		}
		out = append(out, authRemedy{label: authLabel(a, cmd, args), desc: a.Description, command: cmd, args: args})
	}
	if len(out) > 0 {
		return out
	}

	if len(m.opts.AuthCommand) > 0 {
		r := authRemedy{command: m.opts.Command, args: m.opts.AuthCommand}
		r.label = r.line()
		return []authRemedy{r}
	}

	for _, a := range m.authMethods {
		if a.ID == "" {
			continue
		}
		out = append(out, authRemedy{label: authLabel(a, "", nil), desc: a.Description, methodID: a.ID})
	}
	return out
}

// authLabel names a method in one short line. The agent's own name for it wins
// ("Log in with Google"); a command falls back to its basename and arguments,
// since the absolute path it arrived as is longer than the box it goes in.
func authLabel(a acp.AuthMethod, command string, args []string) string {
	if a.Name != "" {
		return a.Name
	}
	if command != "" {
		return strings.TrimSpace(filepath.Base(command) + " " + strings.Join(args, " "))
	}
	return a.ID
}

// canAuthenticate reports whether logging in is possible, which is not the same
// as being asked for it. Credentials go wrong while an agent is perfectly happy
// to talk: a token expires, a key is revoked, an account turns out to be the
// wrong one — Gemini will authenticate against a gateway that has nothing behind
// it and only say so when the first turn comes back 403. That is what /login is
// for, and it is why this does not test authNeed.
func (m Model) canAuthenticate() bool { return !m.loggingIn && len(m.authRemedies()) > 0 }

// startLogin runs the only remedy there is, or opens the picker when the agent
// offers a choice. Gemini offers four, and picking between a Google account and
// a Vertex key is not a decision opentree can make for anyone.
func (m Model) startLogin() (tea.Model, tea.Cmd) {
	remedies := m.authRemedies()
	if len(remedies) == 0 {
		return m, nil
	}
	// A terminal login takes the screen and a protocol one re-credentials the
	// agent underneath its own turn. Neither is something to do to a running
	// one, and stopping it on somebody's behalf is worse than saying no.
	if m.turn {
		m = m.appendNotice("finish or interrupt this turn before logging in")
		return m.relayout(), nil
	}
	if len(remedies) == 1 {
		return m.runRemedy(remedies[0])
	}
	m.login = login{open: true}
	return m.relayout(), nil
}

func (m Model) runRemedy(r authRemedy) (tea.Model, tea.Cmd) {
	if r.terminal() {
		return m, m.authCmd(r)
	}
	cmd := m.authenticateCmd(r.methodID)
	if cmd == nil {
		// No agent to ask — it never started, or it died. Saying "logging in…"
		// over a login nobody is performing would wait forever; the panel's
		// restart is the honest offer.
		return m, nil
	}
	m.loggingIn = true
	return m.relayout(), cmd
}

// authenticateCmd has the agent log itself in. The chat's own context bounds
// it, so an unfinished browser flow ends when the window does.
//
// ponytail: no cancel key while it is in flight — that needs a context per
// call, and ctrl+c already leaves the chat. Add one if a hung flow ever traps
// somebody.
func (m Model) authenticateCmd(methodID string) tea.Cmd {
	client := m.client
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		return authenticatedMsg{err: client.Authenticate(m.ctx, methodID)}
	}
}

// loginAction is what the [l] key says it will do: the one remedy by name, or
// the fact that there is a choice to make. A command is named in full — pressing
// the key runs it, and what is about to run should be readable first.
func (m Model) loginAction() string {
	remedies := m.authRemedies()
	if len(remedies) != 1 {
		return "log in"
	}
	if remedies[0].terminal() {
		return remedies[0].line()
	}
	return remedies[0].label
}

// loginRows is the picker's list: what each method is called, and what the
// agent says it does.
func (m Model) loginRows() []completionItem {
	remedies := m.authRemedies()
	rows := make([]completionItem, 0, len(remedies))
	for _, r := range remedies {
		desc := r.desc
		if r.terminal() {
			// The full command, since pressing this hands it a terminal.
			desc = r.line()
		}
		rows = append(rows, completionItem{value: r.label, desc: desc})
	}
	return rows
}

func (m Model) loginView() string {
	return pickerView("log in to "+m.opts.Agent, m.loginRows(), m.login.cursor, m.width)
}

func (m Model) loginHeight() int { return pickerHeight(len(m.loginRows())) }

// handleLoginKey drives the picker, the same keys as the other two.
func (m Model) handleLoginKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.authRemedies()

	switch msg.String() {
	case "esc", "ctrl+c":
		m.login = login{}
		return m.relayout(), nil

	case "up", "ctrl+p":
		if len(rows) > 0 {
			m.login.cursor = (m.login.cursor - 1 + len(rows)) % len(rows)
		}
		return m.relayout(), nil

	case "down", "ctrl+n":
		if len(rows) > 0 {
			m.login.cursor = (m.login.cursor + 1) % len(rows)
		}
		return m.relayout(), nil

	case "enter":
		return m.chooseRemedy(m.login.cursor)
	}

	if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 && n <= len(rows) {
		return m.chooseRemedy(n - 1)
	}
	return m, nil
}

func (m Model) chooseRemedy(i int) (tea.Model, tea.Cmd) {
	rows := m.authRemedies()
	if i < 0 || i >= len(rows) {
		return m, nil
	}
	m.login = login{}
	return m.runRemedy(rows[i])
}
