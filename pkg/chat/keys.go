package chat

import "github.com/charmbracelet/bubbles/key"

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
	Help      key.Binding
	Restart   key.Binding
	Login     key.Binding
	Quit      key.Binding

	// Commands and Mentions open the completion palette by being typed into
	// the message rather than by being intercepted, so they are never passed
	// to key.Matches. They are here to be listed: a palette nobody knows to
	// summon may as well not exist.
	Commands key.Binding
	Mentions key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Cancel, k.Help, k.Quit}
}

// FullHelp is every key, grouped by what it is for.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.Newline, k.Commands, k.Mentions},
		{k.Cancel, k.CycleMode, k.Settings, k.Thoughts},
		{k.ScrollUp, k.ScrollDn, k.PageUp, k.PageDown, k.Quit},
	}
}

// StoppedHelp is the reduced set offered when the agent has exited or needs
// credentials, where sending a prompt is not an option. Logging in only
// appears when credentials are what is missing — offering it to someone whose
// agent crashed is advice that cannot help.
func (k keyMap) StoppedHelp(login bool) []key.Binding {
	if login {
		return []key.Binding{k.Restart, k.Login, k.Quit}
	}
	return []key.Binding{k.Restart, k.Quit}
}

var keys = keyMap{
	Send: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send"),
	),
	Newline: key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "newline"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "interrupt"),
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
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit"),
	),
	// Both carry their key as well as their help text: bubbles drops a binding
	// with no keys from the help entirely.
	Commands: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "commands")),
	Mentions: key.NewBinding(key.WithKeys("@"), key.WithHelp("@", "attach a file")),
}
