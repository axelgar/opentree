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

	promptMarkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261")).
			Bold(true)

	userTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#DDD"))

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

	noticeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

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
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
