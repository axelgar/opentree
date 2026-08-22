package chat

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/ui"
)

// The styles here deliberately mirror pkg/tui so the chat and the workspace
// list read as one program. They stay duplicated rather than shared, because
// exporting sixty style vars to share eight of them is a worse trade — but the
// colours they are built from now come from pkg/ui, where each is an
// AdaptiveColor. Mirroring by hand had the two agreeing by coincidence, and
// neither of them agreeing with a light terminal at all.
var (
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF7DB")).
			Background(lipgloss.Color("#888B7E")).
			Padding(0, 1)

	metaStyle = lipgloss.NewStyle().
			Foreground(ui.Dim)

	// What you said is a block, not a line: an accent rule down the left and a
	// band a shade off the background. Scrolling a long conversation is mostly
	// hunting for your own last question, and a marker glyph alone does not
	// survive that at a glance.
	userBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(ui.Accent).
			Background(ui.Band).
			Foreground(ui.Body).
			PaddingLeft(1)

	// Brand styles for an agent outside the registry, or one that named no
	// colour. Both still get the layout; they just get it in grey.
	agentMarkStyle = lipgloss.NewStyle().
			Foreground(ui.Meta)

	logoStyle = lipgloss.NewStyle().
			Foreground(ui.Meta)

	agentNameStyle = lipgloss.NewStyle().
			Bold(true)

	cwdStyle = lipgloss.NewStyle().
			Foreground(ui.Dim)

	agentTextStyle = lipgloss.NewStyle().
			Foreground(ui.Body)

	thoughtStyle = lipgloss.NewStyle().
			Foreground(ui.Faint).
			Italic(true)

	toolRunningStyle = lipgloss.NewStyle().
				Foreground(ui.Warn)

	toolDoneStyle = lipgloss.NewStyle().
			Foreground(ui.Success)

	toolFailedStyle = lipgloss.NewStyle().
			Foreground(ui.Danger)

	toolTitleStyle = lipgloss.NewStyle().
			Foreground(ui.Muted)

	// A tool's own output hangs off the row that ran it, quieter than the row
	// itself: it is evidence, not narration, and it must not compete with what
	// the agent says about it.
	toolOutputStyle = lipgloss.NewStyle().
			Foreground(ui.ToolOutput)

	noticeStyle = lipgloss.NewStyle().
			Foreground(ui.Meta).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(ui.Danger).
			Bold(true)

	// A slash command painted where it was typed, in the same accent the
	// palette selects with — so the row you picked and the word now sitting in
	// the message read as the same thing. Unbolded, unlike the palette's
	// selection: inside the input the colour is the whole signal, and bold on
	// top of it reads as shouting at your own message.
	commandStyle = lipgloss.NewStyle().
			Foreground(ui.Accent)

	// A word in the message that names a file, and so will leave it as an
	// attachment rather than as text. Not the command's orange: both say the
	// word resolves, but a command and a file are not the same promise, and one
	// colour for both would make the message look like it was all instructions.
	mentionStyle = lipgloss.NewStyle().
			Foreground(ui.Success)

	// The rest of the completion, shown ahead of the cursor. Dimmer than the
	// message it sits in, because it is not part of it until it is accepted.
	ghostStyle = lipgloss.NewStyle().
			Foreground(ui.Ghost)

	completionItemStyle = lipgloss.NewStyle().
				Foreground(ui.Meta)

	completionSelectedStyle = lipgloss.NewStyle().
				Foreground(ui.Accent).
				Bold(true)

	errorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ui.Danger).
			Padding(0, 1)

	diffAddStyle    = lipgloss.NewStyle().Foreground(ui.Success)
	diffRemoveStyle = lipgloss.NewStyle().Foreground(ui.Danger)

	permBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ui.Accent).
			Padding(0, 1)

	permKeyStyle = lipgloss.NewStyle().
			Foreground(ui.Accent).
			Bold(true)

	permLabelStyle = lipgloss.NewStyle().
			Foreground(ui.BodyStrong)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, false, false, true).
			BorderForeground(ui.Border).
			PaddingLeft(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(ui.Dim)

	// Live agent flags beside the input: bright enough to read at a glance
	// without competing with the conversation.
	flagStyle = lipgloss.NewStyle().
			Foreground(ui.Accent)
)
