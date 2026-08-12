package chat

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// sessionTitleMax is how much of the first message names a conversation. Long
// enough to recognise, short enough for the row it has to share with a date.
const sessionTitleMax = 60

// sessions is the picker over earlier conversations, opened by /resume.
//
// Resuming is not a slash command in ACP: it is a pair of capabilities —
// sessionCapabilities.list to find the conversations, loadSession or
// sessionCapabilities.resume to open one — so the command is opentree's and
// only exists where the agent can serve it.
type sessions struct {
	open   bool
	cursor int

	// loading is set while the agent's own directory is on its way. The
	// recorded ones are shown immediately underneath it, so the picker is never
	// an empty box waiting on a round trip.
	loading bool
	rows    []acp.SessionInfo
	err     string
}

// sessionsListedMsg is the agent's answer to session/list.
type sessionsListedMsg struct {
	sessions []acp.SessionInfo
	err      error
}

// canPickSession reports whether /resume has anything to offer: an agent that
// can reopen a conversation, and somewhere to get one from.
func (m Model) canPickSession() bool {
	return m.canResume && (m.canListSessions || len(m.opts.KnownSessions) > 0)
}

func (m Model) openSessions() (tea.Model, tea.Cmd) {
	m.sessions = sessions{
		open:    true,
		loading: m.canListSessions && m.client != nil,
		rows:    mergeSessions(nil, m.opts.KnownSessions),
	}
	return m.relayout(), m.listSessionsCmd()
}

// listSessionsCmd asks the agent which conversations it still has here. Scoped
// to the worktree: a session belongs to the directory it was opened in, and the
// ones from the workspace next door are somebody else's work.
func (m Model) listSessionsCmd() tea.Cmd {
	if !m.canListSessions || m.client == nil {
		return nil
	}
	client, cwd := m.client, m.opts.Cwd
	return func() tea.Msg {
		resp, err := client.ListSessions(m.ctx, cwd, "")
		if err != nil {
			return sessionsListedMsg{err: err}
		}
		return sessionsListedMsg{sessions: resp.Sessions}
	}
}

// mergeSessions is the directory the picker shows: what the agent still has,
// plus what opentree recorded, newest first.
//
// The agent's copy wins on a collision — its title summarises the conversation,
// opentree's is only the first thing that was said to it — but a title is not
// dropped for a blank one, because an agent may list a session it has not
// named. Anything undated sorts last: it is a recorded id from before there was
// anything to date it by.
func mergeSessions(agent, known []acp.SessionInfo) []acp.SessionInfo {
	at := make(map[string]int, len(known)+len(agent))
	var out []acp.SessionInfo

	add := func(s acp.SessionInfo) {
		if s.SessionID == "" {
			return
		}
		i, seen := at[s.SessionID]
		if !seen {
			at[s.SessionID] = len(out)
			out = append(out, s)
			return
		}
		if s.Title != "" {
			out[i].Title = s.Title
		}
		if s.UpdatedAt.After(out[i].UpdatedAt) {
			out[i].UpdatedAt = s.UpdatedAt
		}
	}

	for _, s := range known {
		add(s)
	}
	for _, s := range agent {
		add(s)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// sessionRows is the picker's list: what each conversation was about, and when
// it was last touched.
func (m Model) sessionRows() []completionItem {
	rows := make([]completionItem, 0, len(m.sessions.rows))
	for _, s := range m.sessions.rows {
		title := s.Title
		if title == "" {
			// An agent that lists sessions without naming them, or an id
			// recorded before anything was said to it. The id is all there is.
			title = "session " + shortID(s.SessionID)
		}
		desc := ago(s.UpdatedAt)
		if s.SessionID == m.sessionID {
			desc = joinMeta("this conversation", desc)
		}
		rows = append(rows, completionItem{value: title, desc: desc})
	}
	return rows
}

func (m Model) sessionsView() string {
	rows := m.sessionRows()
	title := "resume a conversation"
	switch {
	case m.sessions.err != "":
		title = "resume a conversation — " + m.sessions.err
	case m.sessions.loading:
		title = "resume a conversation — asking " + m.opts.Agent + "…"
	case len(rows) == 0:
		title = "no earlier conversations here"
	}
	return pickerView(title, rows, m.sessions.cursor, m.width)
}

func (m Model) sessionsHeight() int { return pickerHeight(len(m.sessionRows())) }

// handleSessionsKey drives the picker. It mirrors the settings one: same keys,
// same digits, esc closes.
func (m Model) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.sessions.rows

	switch msg.String() {
	case "esc", "ctrl+c":
		m.sessions = sessions{}
		return m.relayout(), nil

	case "up", "ctrl+p":
		if len(rows) > 0 {
			m.sessions.cursor = (m.sessions.cursor - 1 + len(rows)) % len(rows)
		}
		return m.relayout(), nil

	case "down", "ctrl+n":
		if len(rows) > 0 {
			m.sessions.cursor = (m.sessions.cursor + 1) % len(rows)
		}
		return m.relayout(), nil

	case "enter":
		return m.chooseSession(m.sessions.cursor)
	}

	if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 && n <= len(rows) {
		return m.chooseSession(n - 1)
	}
	return m, nil
}

// chooseSession switches the chat to another conversation.
func (m Model) chooseSession(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.sessions.rows) {
		return m, nil
	}
	chosen := m.sessions.rows[i]
	m.sessions = sessions{}

	if chosen.SessionID == m.sessionID {
		return m.relayout(), nil // already in it
	}
	// A turn in flight belongs to the conversation being left, and its result
	// would land in the one being opened. Interrupting on someone's behalf is
	// worse than saying no.
	if m.turn {
		m = m.appendNotice("finish or interrupt this turn before switching conversation")
		return m.relayout(), nil
	}

	// The log belongs to the conversation being left. A load replays the new
	// one's history into the empty view; a resume leaves it empty and says so.
	m.entries, m.toolIdx = nil, make(map[string]int)
	m.usage, m.err = nil, nil
	m.sessionID = ""
	return m.relayout(), m.switchSessionCmd(chosen)
}

// switchSessionCmd reopens the chosen conversation and records it as the
// workspace's current one, so closing the window and coming back lands in the
// conversation that was chosen rather than the one that was left.
func (m Model) switchSessionCmd(chosen acp.SessionInfo) tea.Cmd {
	client, cwd := m.client, m.opts.Cwd
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		msg := m.reopenSession(client, chosen.SessionID, cwd)
		ready, ok := msg.(sessionReadyMsg)
		if !ok || m.opts.SaveSession == nil {
			// A fresh session created by the fallback path has already recorded
			// itself; anything else failed and has nothing to record.
			return msg
		}
		if err := m.opts.SaveSession(acp.SessionInfo{
			SessionID: ready.id,
			Cwd:       cwd,
			Title:     chosen.Title,
			UpdatedAt: time.Now(),
		}); err != nil {
			return errMsg{err: fmt.Errorf("failed to record session id: %w", err)}
		}
		return msg
	}
}

// nameSessionCmd records what a conversation is about, from the first thing
// said to it.
//
// An agent that serves session/list sends a better title of its own and it
// wins; this is what one that keeps no directory leaves behind, and it is free
// — opentree already has the text in its hand.
func (m Model) nameSessionCmd(text string) tea.Cmd {
	if m.opts.SaveSession == nil || m.sessionID == "" {
		return nil
	}
	save := m.opts.SaveSession
	info := acp.SessionInfo{
		SessionID: m.sessionID,
		Cwd:       m.opts.Cwd,
		Title:     sessionTitle(text),
		UpdatedAt: time.Now(),
	}
	return func() tea.Msg {
		// Not worth failing a turn over: the conversation is under way, and the
		// name is only how it will be found later.
		_ = save(info)
		return nil
	}
}

func sessionTitle(text string) string {
	title := firstLine(text)
	if runes := []rune(title); len(runes) > sessionTitleMax {
		title = string(runes[:sessionTitleMax-1]) + "…"
	}
	return title
}

// shortID is the leading chunk of a session id, which is all a row has space
// for and all anyone reads of one anyway.
func shortID(id string) string {
	if runes := []rune(id); len(runes) > 8 {
		return string(runes[:8])
	}
	return id
}

// ago is how long since a conversation was last touched, in the roughest unit
// that still says something.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func joinMeta(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + " · " + b
}
