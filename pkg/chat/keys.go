package chat

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

type keyMap struct {
	Send      key.Binding
	Newline   key.Binding
	Cancel    key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	ScrollUp  key.Binding
	ScrollDn  key.Binding
	Settings  key.Binding
	CycleMode key.Binding
	Thoughts  key.Binding
	Expand    key.Binding
	Paste     key.Binding
	Help      key.Binding

	// HistoryPrev and HistoryNext walk the messages already sent. They are two
	// bindings and one help entry: they are one gesture to learn, and a status
	// line with room for five keys should not spend two of them saying ↑ and
	// then ↓.
	HistoryPrev key.Binding
	HistoryNext key.Binding

	Restart key.Binding
	Login   key.Binding
	Back    key.Binding

	// The setup phase's own three. Approve and Decline are the permission
	// dialog's letters, deliberately: this is the same question — may opentree
	// run this — asked about the project's commands rather than the agent's.
	Approve     key.Binding
	Decline     key.Binding
	StartAnyway key.Binding

	// Commands and Mentions open the completion palette by being typed into
	// the message rather than by being intercepted, so they are never passed
	// to key.Matches. They are here to be listed: a palette nobody knows to
	// summon may as well not exist.
	Commands key.Binding
	Mentions key.Binding
}

// ShortHelp is ordered by what should survive a narrow terminal, because the
// status line drops from the end. ? outranks esc and ctrl+c: it is the one
// binding that leads to the others.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Help, k.Cancel, k.Back}
}

// FullHelp is every key, grouped by what it is for.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Newline, k.Commands, k.Mentions, k.Paste, k.HistoryPrev},
		{k.Cancel, k.CycleMode, k.Settings, k.Thoughts, k.Expand},
		{k.ScrollUp, k.ScrollDn, k.PageUp, k.PageDown, k.Back},
	}
}

// StoppedHelp is the reduced set offered when the agent has exited or needs
// credentials, where sending a prompt is not an option. Logging in only
// appears when credentials are what is missing — offering it to someone whose
// agent crashed is advice that cannot help.
func (k keyMap) StoppedHelp(login bool) []key.Binding {
	if login {
		return []key.Binding{k.Restart, k.Login, k.Back}
	}
	return []key.Binding{k.Restart, k.Back}
}

var keys = keyMap{
	Send: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send"),
	),
	// Three keys for one gesture, because a terminal has three ways of
	// saying shift+enter: "shift+enter" for a runtime that names the key,
	// alt+enter for the ESC CR a terminal sends for enter held with a
	// modifier it cannot report, and ctrl+j for a terminal that reports no
	// modifiers at all — the last one being what the help pointed at for as
	// long as it was the only one that always arrived. The extended-key
	// request below is what turns the first into the common case.
	Newline: key.NewBinding(
		key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
		key.WithHelp("shift+enter", "newline"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "interrupt/clear"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("pgup", "page up"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("pgdn", "page down"),
	),
	// Scrolling deliberately avoids ctrl+u / ctrl+d: the textarea binds those
	// to delete-to-line-start and delete-forward, and an input box that cannot
	// erase a line is a worse trade than one without half-page scroll keys.
	ScrollUp: key.NewBinding(
		key.WithKeys("shift+up"),
		key.WithHelp("shift+↑", "scroll up"),
	),
	ScrollDn: key.NewBinding(
		key.WithKeys("shift+down"),
		key.WithHelp("shift+↓", "scroll down"),
	),
	// ctrl+g is free: the textarea binds neither it nor ctrl+o below, while
	// ctrl+m is Enter and ctrl+p is line-previous.
	Settings: key.NewBinding(
		key.WithKeys("ctrl+g"),
		key.WithHelp("ctrl+g", "settings"),
	),
	// shift+tab is free: tab belongs to the completion palette, and the
	// textarea binds neither.
	CycleMode: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "cycle mode"),
	),
	// Not ctrl+t: that is the textarea's transpose-character binding.
	Thoughts: key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "toggle reasoning"),
	),
	// ctrl+x is free: the textarea's cut family stops at ctrl+k/ctrl+u, and
	// emacs's ctrl+x prefix has no meaning in a one-box composer.
	Expand: key.NewBinding(
		key.WithKeys("ctrl+x"),
		key.WithHelp("ctrl+x", "expand tool output"),
	),
	// Taken from the textarea, which binds ctrl+v to pasting text, and handed
	// straight back when the clipboard holds no image. On macOS this is ctrl+v
	// and not cmd+v: cmd+v is the terminal's own paste, and a terminal asked to
	// paste a picture sends nothing at all.
	Paste: key.NewBinding(
		key.WithKeys("ctrl+v"),
		key.WithHelp("ctrl+v", "paste"),
	),
	// The arrows, which the message box also wants: they only recall from the
	// edges of what is written, so moving the cursor inside a message still
	// works. Only the first carries help text — one entry describes both.
	HistoryPrev: key.NewBinding(
		key.WithKeys("up"),
		key.WithHelp("↑/↓", "earlier messages"),
	),
	HistoryNext: key.NewBinding(
		key.WithKeys("down"),
	),
	// A printable character, so it only opens the key list when the message is
	// empty — typing "?" into a question must reach the textarea.
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "keys"),
	),
	Restart: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "restart agent"),
	),
	Login: key.NewBinding(
		key.WithKeys("l"),
		key.WithHelp("l", "log in"),
	),
	Back: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "back to opentree"),
	),
	Approve: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "run setup"),
	),
	Decline: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "skip setup"),
	),
	// Not "r": that is retry, on the same panel.
	StartAnyway: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "start the agent anyway"),
	),
	// Both carry their key as well as their help text: bubbles drops a binding
	// with no keys from the help entirely.
	Commands: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "commands")),
	Mentions: key.NewBinding(key.WithKeys("@"), key.WithHelp("@", "attach a file")),
}

// ---------------------------------------------------------------------------
// Keys the runtime has no name for
// ---------------------------------------------------------------------------

// modifyOtherKeys is xterm's request for the terminal to report the keys that
// have no encoding of their own — shift+enter being the one the message box
// wants, since a terminal left to itself sends a bare CR for it, the same byte
// enter sends and indistinguishable from it.
//
// Level 1 is the conservative one: every key that already has a well-known
// encoding keeps it, so esc stays esc and ctrl+j stays ctrl+j. Level 2 would
// re-encode those too, and nothing here can read them.
const (
	modifyOtherKeysOn  = "\x1b[>4;1m"
	modifyOtherKeysOff = "\x1b[>4;0m"
)

// extendedKeys asks the terminal for modified keys and returns the undo, which
// the caller owes it: the mode outlives the program, and a terminal left in it
// reports keys to a shell that never asked.
//
// This is what makes shift+enter work with nothing for the reader to configure.
// Inside tmux it takes tmux's cooperation as well: tmux answers this request
// only when its own extended-keys option is on, which is why opentree sets that
// when it makes a window.
func extendedKeys() func() {
	f := os.Stdout
	if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		// Not a terminal: there is nobody to ask, and the sequence would land
		// in whatever the output was redirected into.
		return func() {}
	}
	_, _ = f.WriteString(modifyOtherKeysOn)
	return func() { _, _ = f.WriteString(modifyOtherKeysOff) }
}

// enterKeys is every way a terminal that reports modified keys can say enter,
// as bubbletea prints it, and what each one means to the message box. Both
// formats are here because terminals differ and tmux picks between them with
// its extended-keys-format option: xterm's CSI 27;mod;13~, and the CSI 13;mod u
// of the newer protocols.
//
// mod is a bitmask plus one — shift 1, alt 2, ctrl 4, super 8 — so counting to
// 16 covers every combination of the four, and each of them breaks the line.
// Which modifier was held is not a distinction worth making the reader
// remember: enter alone sends, enter with anything else does not.
//
// The sequences are kept as what bubbletea prints for them rather than as the
// bytes: a CSI sequence it cannot name reaches Update as a type it keeps to
// itself, so what it prints is the only handle on it there is.
var enterKeys = func() map[string]tea.KeyMsg {
	m := make(map[string]tea.KeyMsg, 32)
	for mod := 1; mod <= 16; mod++ {
		// alt+enter rather than a shift+enter of its own: every modified enter
		// means the same thing here, and alt+enter is the one bubbletea already
		// produces by itself — from the ESC CR a terminal sends for it whether
		// or not it reports modifiers.
		key := tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
		if mod == 1 {
			// No modifier held at all. A terminal asked for level 1 keeps
			// sending a bare CR for this, so it should never arrive — but one
			// that did and was dropped would read as a chat that cannot send,
			// which is too poor a failure to leave to a should.
			key = tea.KeyMsg{Type: tea.KeyEnter}
		}
		m[unknownCSI(fmt.Sprintf("27;%d;13~", mod))] = key
		m[unknownCSI(fmt.Sprintf("13;%du", mod))] = key
	}
	return m
}()

// unknownCSI is how bubbletea prints a CSI sequence it has no name for: the
// bytes after the introducer, which is exactly what tells one from another.
func unknownCSI(seq string) string {
	return fmt.Sprintf("?CSI%+v?", []byte(seq))
}

// namedKey gives an enter the terminal spelled out in full the name bubbletea
// would have given it if it knew the sequence, so the message box can bind it
// like any other key. Everything else passes through untouched.
func namedKey(msg tea.Msg) tea.Msg {
	s, ok := msg.(fmt.Stringer)
	if !ok {
		return msg
	}
	if key, ok := enterKeys[s.String()]; ok {
		return key
	}
	return msg
}
