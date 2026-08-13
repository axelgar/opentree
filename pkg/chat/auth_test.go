package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// copilotMethod is Copilot's own authMethod, which names the binary to run.
func copilotMethod() acp.AuthMethod {
	return acp.AuthMethod{
		ID:          "copilot-login",
		Name:        "Log in with Copilot CLI",
		Description: "Run `copilot login` in the terminal",
		Meta: json.RawMessage(`{"terminal-auth":{"command":"/opt/homebrew/bin/copilot",` +
			`"args":["login"],"label":"Copilot Login"}}`),
	}
}

// geminiMethods are logins the agent performs itself; there is no command to run.
func geminiMethods() []acp.AuthMethod {
	return []acp.AuthMethod{
		{ID: "oauth-personal", Name: "Log in with Google", Description: "Log in with your Google account"},
		{ID: "gemini-api-key", Name: "Gemini API key", Description: "Use an API key with Gemini Developer API"},
		{ID: "vertex-ai", Name: "Vertex AI"},
	}
}

// The three sources rank in the order opentree trusts them.
func TestAuthRemedies_Precedence(t *testing.T) {
	t.Run("the agent's own command beats the registry's", func(t *testing.T) {
		m := newTestModel()
		m.opts.Command, m.opts.AuthCommand = "copilot", []string{"login"}
		m.authMethods = []acp.AuthMethod{copilotMethod()}

		got := m.authRemedies()
		if len(got) != 1 || got[0].command != "/opt/homebrew/bin/copilot" {
			t.Fatalf("remedies = %+v, want the absolute path the agent reported", got)
		}
	})

	t.Run("the registry's command beats the protocol", func(t *testing.T) {
		// opencode declares a method whose remedy is prose: "run this in a
		// terminal". The command opentree recorded is that same login, and it is
		// the one that has always worked.
		m := newTestModel()
		m.opts.Command, m.opts.AuthCommand = "opencode", []string{"auth", "login"}
		m.authMethods = []acp.AuthMethod{{ID: "opencode-login", Name: "Login with opencode"}}

		got := m.authRemedies()
		if len(got) != 1 || got[0].line() != "opencode auth login" {
			t.Fatalf("remedies = %+v, want the registry's command", got)
		}
		if got[0].methodID != "" {
			t.Error("a terminal remedy must not also carry a method id — it would run twice")
		}
	})

	t.Run("the protocol is what is left", func(t *testing.T) {
		m := newTestModel()
		m.authMethods = geminiMethods()

		got := m.authRemedies()
		if len(got) != 3 {
			t.Fatalf("remedies = %d, want one per method", len(got))
		}
		if got[0].methodID != "oauth-personal" || got[0].terminal() {
			t.Errorf("first remedy = %+v, want a protocol login", got[0])
		}
		if got[0].label != "Log in with Google" {
			t.Errorf("label = %q, want the agent's name for it", got[0].label)
		}
	})

	t.Run("an agent with nothing to offer", func(t *testing.T) {
		// Claude Code's adapter: no methods, and authenticate is not implemented.
		// Without a registry command there is no honest [l] to show.
		m := newTestModel()
		if got := m.authRemedies(); len(got) != 0 {
			t.Errorf("remedies = %+v, want none", got)
		}
		m.authNeed = true
		if m.canLogIn() {
			t.Error("canLogIn with no remedy offers a key that cannot do anything")
		}
	})
}

// The gap this exists to close: an agent that authenticates over the protocol
// used to get no [l] at all, because the registry had no command for it.
func TestAuthRequired_ProtocolAgentIsOfferedTheKey(t *testing.T) {
	m := newTestModel()
	m.opts.Agent, m.opts.Command = "Gemini CLI", "gemini"
	m.authMethods = geminiMethods()
	m, _ = applyUpdate(m, errMsg{err: errString("acp error -32000: Gemini API key is missing"), auth: true})

	if !m.canLogIn() {
		t.Fatal("an agent offering four logins should be offered the login key")
	}
	if !strings.Contains(m.footer(), "[l]") {
		t.Errorf("stopped panel has no [l]:\n%s", m.footer())
	}

	// Four ways in means a choice, not a guess.
	next, _ := m.startLogin()
	m = next.(Model)
	if !m.login.open {
		t.Fatal("expected the picker for an agent with more than one method")
	}
	view := m.footer()
	for _, want := range []string{"Log in with Google", "Gemini API key", "Vertex AI"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker missing %q:\n%s", want, view)
		}
	}
}

// One remedy is not a choice, so [l] runs it.
func TestStartLogin_SingleRemedyRunsWithoutAPicker(t *testing.T) {
	m := newTestModel()
	m.opts.Command, m.opts.AuthCommand = "opencode", []string{"auth", "login"}

	next, cmd := m.startLogin()
	m = next.(Model)
	if m.login.open {
		t.Error("one way in does not need a picker")
	}
	if cmd == nil {
		t.Error("expected the login command to run")
	}
}

// A protocol login needs an agent to ask. Without one, saying "logging in…"
// would be a wait that never ends.
func TestStartLogin_ProtocolWithNoAgentDoesNotHang(t *testing.T) {
	m := newTestModel()
	m.authMethods = []acp.AuthMethod{{ID: "oauth-personal", Name: "Log in with Google"}}

	next, cmd := m.startLogin() // no client on a test model
	m = next.(Model)
	if m.loggingIn {
		t.Error("nothing is logging in; the panel should still offer a restart")
	}
	if cmd != nil {
		t.Error("expected no command with no agent to send it to")
	}
}

// A login the agent performs happens inside the running process, so unlike the
// terminal flow it needs no restart — only the session it was stopped on.
func TestAuthenticated_StartsTheSessionWithoutARestart(t *testing.T) {
	m := newTestModel()
	m.authMethods = geminiMethods()
	m.dead, m.authNeed, m.loggingIn = true, true, true
	m.err = errString("Gemini API key is missing")

	m, cmd := applyUpdate(m, authenticatedMsg{})
	if m.authNeed || m.dead || m.err != nil {
		t.Errorf("after a login: authNeed=%v dead=%v err=%v, want all cleared", m.authNeed, m.dead, m.err)
	}
	if m.loggingIn {
		t.Error("the login is over; the panel should stop saying otherwise")
	}
	if m.restarting {
		t.Error("the process that logged itself in is the one already running")
	}
	// No client in a test model, so there is no session command to run — what
	// matters is that nothing here asked for a new agent.
	_ = cmd
}

// Credentials go wrong while the agent is still answering: a token expires, a
// key is revoked, or a login succeeds against a gateway with nothing behind it.
// /login exists for the case nothing has asked for it.
func TestLoginCommand_OfferedWithoutBeingAsked(t *testing.T) {
	m := newTestModel()
	m.authMethods = geminiMethods()

	if m.authNeed {
		t.Fatal("this test is about an agent that is not asking for credentials")
	}
	var found *acp.Command
	for _, c := range m.clientCommands() {
		if c.Name == "login" {
			found = &c
			break
		}
	}
	if found == nil {
		t.Fatalf("no /login in %v", m.clientCommands())
	}

	run, ok := m.clientCommandFor("/login")
	if !ok {
		t.Fatal("/login is listed but does not run")
	}
	next, _ := run(m)
	if !next.(Model).login.open {
		t.Error("/login should open the picker")
	}
}

// An agent with no way to log in must not advertise a command that cannot work.
func TestLoginCommand_AbsentWithNoRemedy(t *testing.T) {
	m := newTestModel() // no methods, no registry command
	for _, c := range m.clientCommands() {
		if c.Name == "login" {
			t.Error("/login offered for an agent with nothing to log in with")
		}
	}
}

// A terminal login takes the screen and a protocol one re-credentials the agent
// under its own turn.
func TestLogin_RefusedMidTurn(t *testing.T) {
	m := newTestModel()
	m.authMethods = geminiMethods()
	m.turn = true

	next, cmd := m.startLogin()
	m = next.(Model)
	if m.login.open || m.loggingIn {
		t.Error("a login should not start on top of a running turn")
	}
	if cmd != nil {
		t.Error("expected nothing to run")
	}
	if !strings.Contains(m.View(), "interrupt this turn") {
		t.Errorf("nothing said why:\n%s", m.View())
	}
}

// Logging in again mid-conversation keeps the conversation. Reopening it would
// replay a history the log already holds.
func TestAuthenticated_LiveSessionIsNotReloaded(t *testing.T) {
	m := newTestModel() // newTestModel already has a session
	m.authMethods = geminiMethods()
	m.loggingIn = true

	m, cmd := applyUpdate(m, authenticatedMsg{})
	if cmd != nil {
		t.Error("a conversation already open needs nothing reopened")
	}
	if !strings.Contains(m.View(), "logged in") {
		t.Errorf("a login with nothing else to show should say it happened:\n%s", m.View())
	}
}

// The agent's own words are the answer. "Gemini API key is missing or not
// configured" says everything opentree could, and says it accurately.
func TestAuthenticated_FailureKeepsTheAgentsMessage(t *testing.T) {
	m := newTestModel()
	m.authMethods = geminiMethods()
	m.authNeed, m.loggingIn = true, true

	m, _ = applyUpdate(m, authenticatedMsg{err: errString("Gemini API key is missing or not configured")})
	if m.loggingIn {
		t.Error("a failed login is over too")
	}
	if !m.authNeed {
		t.Error("credentials are still missing, so the panel must still say so")
	}
	if !strings.Contains(m.footer(), "Gemini API key is missing") {
		t.Errorf("panel does not carry the agent's message:\n%s", m.footer())
	}
}

// Pressing [l] a second time while the browser is open would start a second
// login, so the panel stops offering it and says what is happening instead.
func TestStoppedPanel_WhileLoggingIn(t *testing.T) {
	m := newTestModel()
	m.authMethods = geminiMethods()
	m.authNeed, m.loggingIn = true, true
	m.err = errString("Authentication required")
	m = m.relayout()

	view := m.footer()
	if !strings.Contains(view, "logging in…") {
		t.Errorf("panel does not say a login is under way:\n%s", view)
	}
	if strings.Contains(view, "[l]") {
		t.Errorf("panel still offers [l] mid-login:\n%s", view)
	}

	next, cmd := m.handleStoppedKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if cmd != nil {
		t.Error("l pressed twice should not start a second login")
	}
	_ = next
}

// A command is shown in full before the key that runs it is pressed.
func TestStoppedPanel_NamesTheCommandItWillRun(t *testing.T) {
	m := newTestModel()
	m.opts.Command, m.opts.AuthCommand = "opencode", []string{"auth", "login"}
	m.authNeed = true
	m.err = errString("Authentication required")
	m = m.relayout()

	if !strings.Contains(m.footer(), "opencode auth login") {
		t.Errorf("panel does not name the command:\n%s", m.footer())
	}
}
