package chat

import "github.com/charmbracelet/lipgloss"

// The palette deliberately mirrors pkg/tui so the chat and the workspace list
// read as one program. It is duplicated rather than shared because exporting
// sixty style vars to share eight of them is a worse trade.
var (
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF7DB")).
			Background(lipgloss.Color("#888B7E")).
			Padding(0, 1)

	metaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// What you said is a block, not a line: an accent rule down the left and a
	// band a shade off the background. Scrolling a long conversation is mostly
	// hunting for your own last question, and a marker glyph alone does not
	// survive that at a glance.
	userBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("#F4A261")).
			Background(lipgloss.Color("#26262B")).
			Foreground(lipgloss.Color("#EEE")).
			PaddingLeft(1)

	// Brand styles for an agent outside the registry, or one that named no
	// colour. Both still get the layout; they just get it in grey.
	agentMarkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888"))

	logoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888"))

	agentNameStyle = lipgloss.NewStyle().
			Bold(true)

	cwdStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	agentTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EEE"))

	thoughtStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666")).
			Italic(true)

	toolRunningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E9C46A"))

	toolDoneStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	toolFailedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	toolTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAA"))

	// A tool's own output hangs off the row that ran it, quieter than the row
	// itself: it is evidence, not narration, and it must not compete with what
	// the agent says about it.
	toolOutputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7A7A7A"))

	noticeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	// A slash command painted where it was typed, in the same accent the
	// palette selects with — so the row you picked and the word now sitting in
	// the message read as the same thing. Unbolded, unlike the palette's
	// selection: inside the input the colour is the whole signal, and bold on
	// top of it reads as shouting at your own message.
	commandStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261"))

	// A word in the message that names a file, and so will leave it as an
	// attachment rather than as text. Not the command's orange: both say the
	// word resolves, but a command and a file are not the same promise, and one
	// colour for both would make the message look like it was all instructions.
	mentionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	// The rest of the completion, shown ahead of the cursor. Dimmer than the
	// message it sits in, because it is not part of it until it is accepted.
	ghostStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5F5F5F"))

	completionItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	completionSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F4A261")).
				Bold(true)

	errorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("196")).
			Padding(0, 1)

	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	permBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#F4A261")).
			Padding(0, 1)

	permKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261")).
			Bold(true)

	permLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#DDD"))

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("#444")).
			PaddingLeft(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// Live agent flags beside the input: bright enough to read at a glance
	// without competing with the conversation.
	flagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261"))
)
