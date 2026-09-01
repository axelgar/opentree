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
	// Compare is Diff writ large: d is one workspace's changes, D is every
	// sibling of its fan-out group in one scroll.
	Compare key.Binding
	PR      key.Binding
	Open    key.Binding
	Review  key.Binding
	// Autopilot pairs with PR the way Review does: p publishes once by hand,
	// P keeps publishing until the PR is green.
	Autopilot key.Binding
	Delete    key.Binding
	// Promote is uppercase for the same reason Delete confirms: it removes
	// workspaces. W as in winner — the sibling it keeps.
	Promote key.Binding
	Select  key.Binding
	Filter  key.Binding
	Sort    key.Binding
	Answer  key.Binding
	Stop    key.Binding
	Msg     key.Binding
	Server  key.Binding
	Blocked key.Binding
	ErrLog  key.Binding
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
		{k.Diff, k.Compare, k.Promote, k.PR, k.Open, k.Review, k.Autopilot, k.Select, k.Delete},
		{k.Answer, k.Stop, k.Msg, k.Blocked, k.Server, k.Filter, k.Sort},
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
	Compare: key.NewBinding(
		key.WithKeys("D"),
		key.WithHelp("D", "compare fan-out group"),
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
	Autopilot: key.NewBinding(
		key.WithKeys("P"),
		key.WithHelp("P", "autopilot on/off"),
	),
	Delete: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "delete"),
	),
	Promote: key.NewBinding(
		key.WithKeys("W"),
		key.WithHelp("W", "promote fan-out winner"),
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
	Answer: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "answer agent"),
	),
	Stop: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "interrupt agent"),
	),
	// The one server key on this tab. Starting the thing you are looking at is
	// the everyday action; everything else about servers lives in the Servers
	// tab, which has a keyspace of its own.
	Server: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "start/stop server"),
	),
	Msg: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "message agent"),
	),
	// The other half of a notification. Sorting is a preference you set once;
	// "who needs me now" is a question you ask at a moment, and a list that
	// reordered itself every ten seconds as agents changed state would be
	// unusable for everything else it is for.
	Blocked: key.NewBinding(
		key.WithKeys("b"),
		key.WithHelp("b", "next blocked"),
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
	// Five places now, walked in the order the bar draws them.
	Tab: key.NewBinding(
		key.WithKeys("tab", "left", "right"),
		key.WithHelp("tab/←→", "agents · skills · plugins · servers"),
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
