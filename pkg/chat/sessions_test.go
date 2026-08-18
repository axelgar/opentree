package chat

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

func newResumeModel() Model {
	m := newTestModel()
	m.canResume, m.canListSessions = true, true
	// A client that is never spoken to: the picker asks whether there is a
	// connection before offering to use it, and these tests stop at the command
	// rather than running it.
	m.client = &acp.Client{}
	m.opts.KnownSessions = []acp.SessionInfo{
		{SessionID: "ses_old", Title: "the auth bug", UpdatedAt: time.Now().Add(-48 * time.Hour)},
	}
	return m
}

// ---------------------------------------------------------------------------
// When the command exists
// ---------------------------------------------------------------------------

// /resume is not an ACP slash command — neither agent advertises one — so it is
// opentree's, and it exists exactly where the capabilities to serve it do.
func TestResumeCommand_ExistsWhereTheAgentCanServeIt(t *testing.T) {
	known := []acp.SessionInfo{{SessionID: "ses_old"}}

	tests := []struct {
		name       string
		reopen     bool
		list       bool
		known      []acp.SessionInfo
		wantOffere bool
	}{
		{"lists its own sessions", true, true, nil, true},
		{"keeps no list, but opentree recorded some", true, false, known, true},
		{"keeps no list and there is nothing recorded", true, false, nil, false},
		// An agent that cannot reopen anything has nothing to offer, whatever
		// the directory says.
		{"cannot reopen a conversation", false, true, known, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.canResume, m.canListSessions, m.opts.KnownSessions = tc.reopen, tc.list, tc.known

			var found bool
			for _, c := range m.clientCommands() {
				if c.Name == "resume" {
					found = true
				}
			}
			if found != tc.wantOffere {
				t.Errorf("/resume offered = %v, want %v", found, tc.wantOffere)
			}
		})
	}
}

func TestResumeCommand_OpensThePicker(t *testing.T) {
	m := typeInto(newResumeModel(), "/resume")
	// The first enter accepts the palette's completion, the second sends what
	// is now in the box — the same two presses /model takes.
	m, _ = applyUpdate(m, keyMsg("enter"))
	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.overlay() != overlaySessions {
		t.Fatalf("overlay = %v, want the session picker", m.overlay())
	}
	// It must not have been sent to the agent as a prompt.
	if len(m.entries) != 0 {
		t.Errorf("entries = %+v, want opentree's own command to stay out of the conversation", m.entries)
	}
	if !strings.Contains(m.sessionsView(), "the auth bug") {
		t.Errorf("the recorded conversation should be listed while the agent answers\ngot:\n%s", m.sessionsView())
	}
}

// The agent's list is a round trip. The recorded ones are on screen in the
// meantime, so the picker is never an empty box waiting.
func TestResumePicker_ShowsRecordedOnesWhileLoading(t *testing.T) {
	m := newResumeModel()
	next, _ := m.openSessions()
	m = next.(Model)

	if !m.sessions.loading {
		t.Error("the agent can list its sessions, so the picker should be waiting on it")
	}
	if len(m.sessions.rows) != 1 {
		t.Fatalf("rows = %+v, want the recorded conversation shown immediately", m.sessions.rows)
	}

	m, _ = applyUpdate(m, sessionsListedMsg{sessions: []acp.SessionInfo{
		{SessionID: "ses_new", Title: "today's work", UpdatedAt: time.Now()},
	}})
	if m.sessions.loading {
		t.Error("the answer arrived; nothing is loading")
	}
	if len(m.sessions.rows) != 2 {
		t.Errorf("rows = %+v, want the agent's list merged with the recorded one", m.sessions.rows)
	}
}

// A failed list is not a failed command: what opentree recorded is still there,
// and one of those is probably the conversation being looked for.
func TestResumePicker_SurvivesAFailedList(t *testing.T) {
	m := newResumeModel()
	next, _ := m.openSessions()
	m, _ = applyUpdate(next.(Model), sessionsListedMsg{err: stringError("method not found")})

	if !m.sessions.open {
		t.Fatal("the picker closed on an error it could work around")
	}
	if len(m.sessions.rows) != 1 {
		t.Errorf("rows = %+v, want the recorded conversation to survive", m.sessions.rows)
	}
	if !strings.Contains(m.sessionsView(), "could not list") {
		t.Errorf("the picker should say the agent's own list is missing\ngot:\n%s", m.sessionsView())
	}
}

// ---------------------------------------------------------------------------
// The directory
// ---------------------------------------------------------------------------

func TestMergeSessions_NewestFirstAndDeduped(t *testing.T) {
	now := time.Now()
	agent := []acp.SessionInfo{
		{SessionID: "ses_b", Title: "agent's summary", UpdatedAt: now},
		{SessionID: "ses_a", Title: "older", UpdatedAt: now.Add(-72 * time.Hour)},
	}
	known := []acp.SessionInfo{
		{SessionID: "ses_b", Title: "what was typed first", UpdatedAt: now.Add(-time.Hour)},
		{SessionID: "ses_c", UpdatedAt: now.Add(-time.Minute)},
	}

	got := mergeSessions(agent, known)
	if len(got) != 3 {
		t.Fatalf("merged = %+v, want three distinct conversations", got)
	}
	if got[0].SessionID != "ses_b" || got[1].SessionID != "ses_c" || got[2].SessionID != "ses_a" {
		t.Errorf("order = %v/%v/%v, want newest first", got[0].SessionID, got[1].SessionID, got[2].SessionID)
	}
	// The agent's title summarises the conversation; opentree's is only the
	// first thing that was said to it.
	if got[0].Title != "agent's summary" {
		t.Errorf("title = %q, want the agent's", got[0].Title)
	}
}

// An agent that lists sessions without naming them still gets rows worth
// showing: a session with no title is an id and a date, not a blank line.
func TestSessionRows_FallBackToTheID(t *testing.T) {
	m := newResumeModel()
	m.sessionID = "ses_current"
	m.sessions.rows = []acp.SessionInfo{
		{SessionID: "ses_current", Title: "still going", UpdatedAt: time.Now().Add(-90 * time.Minute)},
		{SessionID: "abcdef0123456789", UpdatedAt: time.Now().Add(-26 * time.Hour)},
	}

	rows := m.sessionRows()
	if !strings.Contains(rows[0].desc, "this conversation") {
		t.Errorf("desc = %q, want the current conversation marked", rows[0].desc)
	}
	if rows[1].value != "session abcdef01" {
		t.Errorf("value = %q, want the id shortened for a session with no title", rows[1].value)
	}
	if rows[1].desc != "1d ago" {
		t.Errorf("desc = %q, want a rough age", rows[1].desc)
	}
}

// ---------------------------------------------------------------------------
// Switching
// ---------------------------------------------------------------------------

func TestChooseSession_ClearsTheLogItIsLeaving(t *testing.T) {
	m := newResumeModel()
	m = m.appendChunk(entryAgent, "something from the old conversation")
	m.sessions = sessions{open: true, rows: []acp.SessionInfo{{SessionID: "ses_other", Title: "elsewhere"}}}

	next, cmd := m.chooseSession(0)
	m = next.(Model)

	if cmd == nil {
		t.Fatal("choosing a conversation should go and open it")
	}
	if m.sessions.open {
		t.Error("the picker should close behind the choice")
	}
	if len(m.entries) != 0 {
		t.Errorf("entries = %+v, want the log of the conversation being left cleared", m.entries)
	}
	if m.sessionID != "" {
		t.Errorf("sessionID = %q, want it empty until the new one is open", m.sessionID)
	}
}

// A turn in flight belongs to the conversation being left, and its result would
// land in the one being opened. Interrupting on someone's behalf is worse than
// saying no.
func TestChooseSession_RefusesMidTurn(t *testing.T) {
	m := newResumeModel()
	m.turn = true
	m.sessions = sessions{open: true, rows: []acp.SessionInfo{{SessionID: "ses_other"}}}

	next, cmd := m.chooseSession(0)
	m = next.(Model)

	if cmd != nil {
		t.Error("nothing should be opened while the agent is working")
	}
	if m.sessionID != "ses_test" {
		t.Errorf("sessionID = %q, want the current conversation kept", m.sessionID)
	}
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].kind != entryNotice {
		t.Error("refusing silently leaves the key looking broken")
	}
}

func TestChooseSession_TheCurrentOneDoesNothing(t *testing.T) {
	m := newResumeModel()
	m = m.appendChunk(entryAgent, "already here")
	m.sessions = sessions{open: true, rows: []acp.SessionInfo{{SessionID: m.sessionID}}}

	next, cmd := m.chooseSession(0)
	m = next.(Model)

	if cmd != nil {
		t.Error("reopening the conversation already on screen is a round trip for nothing")
	}
	if len(m.entries) != 1 {
		t.Errorf("entries = %+v, want the log left alone", m.entries)
	}
}

// ---------------------------------------------------------------------------
// Naming
// ---------------------------------------------------------------------------

// The ledger is what /resume offers an agent that keeps no directory, and an
// agent's own title is the better one where it exists — so opentree records
// only the first thing said, and only when the conversation has no name yet.
func TestFirstPrompt_NamesTheConversation(t *testing.T) {
	saved := make(chan acp.SessionInfo, 4)
	m := newTestModel()
	m.opts.SaveSession = func(s acp.SessionInfo) error { saved <- s; return nil }

	cmd := m.nameSessionCmd("fix the flaky login test\nand the one next to it")
	if cmd == nil {
		t.Fatal("a session with somewhere to be recorded should be named")
	}
	cmd()

	select {
	case got := <-saved:
		if got.SessionID != "ses_test" {
			t.Errorf("SessionID = %q, want the session being named", got.SessionID)
		}
		if got.Title != "fix the flaky login test" {
			t.Errorf("Title = %q, want the first line of the first message", got.Title)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing was recorded")
	}
}

// A conversation is named after what it started as, not after whatever was
// last asked — and a resumed one is already named, so today's first prompt must
// not rename it.
func TestNaming_HappensOncePerConversation(t *testing.T) {
	m := newTestModel()
	if m.titled {
		t.Fatal("a conversation nobody has spoken to has no name yet")
	}

	m, _ = m.startTurn("first thing")
	if !m.titled {
		t.Error("the first prompt names the conversation")
	}

	// Reopening one, whether at launch or through the picker, arrives named.
	fresh, _ := applyUpdate(newTestModel(), sessionReadyMsg{id: "ses_old", resumed: true})
	if !fresh.titled {
		t.Error("a resumed conversation would be renamed by the next thing said to it")
	}
	fresh, _ = applyUpdate(newTestModel(), sessionReadyMsg{id: "ses_new"})
	if fresh.titled {
		t.Error("a conversation that was just created has nothing to be named after yet")
	}
}

func TestSessionTitle_TrimmedToTheRow(t *testing.T) {
	long := strings.Repeat("a", 200)
	if got := []rune(sessionTitle(long)); len(got) != sessionTitleMax {
		t.Errorf("title length = %d, want it capped at %d", len(got), sessionTitleMax)
	}
}

// The picker is a footer panel like the settings one, so it has to give the
// viewport its lines back rather than growing to meet a long list.
func TestSessionPicker_FooterStaysBounded(t *testing.T) {
	m := newResumeModel()
	rows := make([]acp.SessionInfo, 30)
	for i := range rows {
		rows[i] = acp.SessionInfo{SessionID: "ses_" + strings.Repeat("x", i+1)}
	}
	m.sessions = sessions{open: true, rows: rows}

	if h := m.footerHeight(); h > pickerWindow+8 {
		t.Errorf("footerHeight = %d, want it capped near the window size", h)
	}
	if !strings.Contains(m.sessionsView(), "30") {
		t.Errorf("expected a position counter for a list longer than the window\ngot:\n%s", m.sessionsView())
	}
}

// The picker owns the keyboard while it is up, exactly as the settings one
// does: a digit picks a row rather than being typed into the message.
func TestSessionPicker_OwnsTheKeyboard(t *testing.T) {
	m := newResumeModel()
	m.sessions = sessions{open: true, rows: []acp.SessionInfo{
		{SessionID: "ses_a", Title: "first"},
		{SessionID: "ses_b", Title: "second"},
	}}

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.sessions.cursor != 1 {
		t.Errorf("cursor = %d, want the arrow to move it", m.sessions.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.sessions.open {
		t.Error("esc should close the picker")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want the picker's keys kept out of the message", m.input.Value())
	}
}
