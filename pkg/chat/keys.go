package chat

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Send     key.Binding
	Newline  key.Binding
	Cancel   key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	ScrollUp key.Binding
	ScrollDn key.Binding
	Settings key.Binding
	Thoughts key.Binding
	Restart  key.Binding
	Login    key.Binding
	Quit     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Cancel, k.Settings, k.Thoughts, k.Quit}
}

// StoppedHelp is the reduced set offered when the agent has exited or needs
// credentials, where sending a prompt is not an option.
func (k keyMap) StoppedHelp() []key.Binding {
	return []key.Binding{k.Restart, k.Login, k.Quit}
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
	// Not ctrl+t: that is the textarea's transpose-character binding.
	Thoughts: key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "toggle reasoning"),
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
}
