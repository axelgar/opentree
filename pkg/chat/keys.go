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
	Restart  key.Binding
	Login    key.Binding
	Quit     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.Newline, k.Cancel, k.ScrollUp, k.ScrollDn, k.Quit}
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
	ScrollUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "scroll up"),
	),
	ScrollDn: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "scroll down"),
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
