package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	New    key.Binding
	Issue  key.Binding
	Remote key.Binding
	Enter  key.Binding
	Diff   key.Binding
	PR     key.Binding
	Open   key.Binding
	Delete key.Binding
	Select key.Binding
	Filter key.Binding
	Sort   key.Binding
	ErrLog key.Binding
	Quit   key.Binding
	Help   key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.Issue, k.Remote, k.Enter, k.Diff, k.Delete, k.Quit, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.New, k.Issue, k.Remote, k.Enter},
		{k.Diff, k.PR, k.Open, k.Select, k.Delete},
		{k.Filter, k.Sort, k.ErrLog, k.Quit, k.Help},
	}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new workspace"),
	),
	Issue: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "from GH issue"),
	),
	Remote: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "from remote branch"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "attach"),
	),
	Diff: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "diff"),
	),
	PR: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "create PR"),
	),
	Open: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "open PR in browser"),
	),
	Delete: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "delete"),
	),
	Select: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "multi-select"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Sort: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "cycle sort"),
	),
	ErrLog: key.NewBinding(
		key.WithKeys("E"),
		key.WithHelp("E", "error log"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
}
