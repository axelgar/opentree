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
	Review key.Binding
	Delete key.Binding
	Select key.Binding
	Filter key.Binding
	Sort   key.Binding
	Agent  key.Binding
	Answer key.Binding
	Stop   key.Binding
	Msg    key.Binding
	ErrLog key.Binding
	// CopyErrLog is only ever consulted inside the error log, which swallows
	// every other key. That is why it can share a letter with Stop without
	// either becoming ambiguous — the log is modal, and the list is not
	// reachable behind it.
	CopyErrLog key.Binding
	Tab        key.Binding
	Quit       key.Binding
	Help       key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	// Messaging is in, creating from a remote branch is out: driving the
	// agent without attaching is the everyday action, and r remains one '?'
	// away in the full help.
	return []key.Binding{k.New, k.Issue, k.Enter, k.Diff, k.Msg, k.Answer, k.Delete, k.Quit, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.New, k.Issue, k.Remote, k.Enter},
		{k.Diff, k.PR, k.Open, k.Review, k.Select, k.Delete},
		{k.Answer, k.Stop, k.Msg, k.Filter, k.Sort, k.Agent},
		{k.Tab, k.ErrLog, k.Quit, k.Help},
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
	Review: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "send reviews to agent"),
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
	Agent: key.NewBinding(
		key.WithKeys("A"),
		key.WithHelp("A", "switch agent"),
	),
	Answer: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "answer agent"),
	),
	Stop: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "interrupt agent"),
	),
	Msg: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "message agent"),
	),
	ErrLog: key.NewBinding(
		key.WithKeys("E"),
		key.WithHelp("E", "error log"),
	),
	// Out of both help lists on purpose: it works on one screen, and that
	// screen's own footer names it. Advertising it beside keys that work
	// everywhere would only send people to press it where it does nothing.
	CopyErrLog: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "copy all"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab", "left", "right"),
		key.WithHelp("tab/←→", "skills"),
	),
	Quit: key.NewBinding(
		// esc is the way out of the workspace list; from Skills it only steps
		// back a tab, which that tab handles before this binding is consulted.
		key.WithKeys("q", "ctrl+c", "esc"),
		key.WithHelp("q/esc", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
}
