package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Styles
var (
	appStyle = lipgloss.NewStyle().Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF7DB")).
			Background(lipgloss.Color("#888B7E")).
			Padding(0, 1)

	selectedItemStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("#F4A261")).
				Foreground(lipgloss.Color("#F4A261")).
				Padding(0, 1)

	itemStyle = lipgloss.NewStyle().
			Padding(0, 1)

	diffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555"))

	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E9C46A"))

	stoppedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1)

	mergedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#6E40C9")).
				Padding(0, 1)

	prOpenBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#1F7A4D")).
				Padding(0, 1)

	issueBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF")).
			Background(lipgloss.Color("#0969DA")).
			Padding(0, 1)

	// agent preview panel styles
	previewBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444")).
			Padding(0, 1).
			MarginTop(1)

	previewTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	previewLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#AAA"))

	// delete confirmation styles
	dangerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	deleteDialogStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("196")).
				Padding(1, 3)

	confirmKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261")).
			Bold(true)

	confirmLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262"))

	// two-step create dialog
	stepLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Italic(true)

	// CI badge styles
	ciSuccessStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F")).
			Bold(true)

	ciFailureStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	ciPendingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E9C46A"))

	// multi-select
	selectedMarkStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F4A261")).
				Bold(true)

	// filter prompt
	filterPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F4A261"))

	// status bar
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// merged cleanup hint
	mergedHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555")).
			Italic(true)

	// error log
	errLogTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")).
				Bold(true)

	errLogLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAA"))

	// uncommitted changes
	uncommittedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E9C46A"))

	// diff view
	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	diffHunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#88C0D0"))
	diffFileStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Bold(true)

	// file changes panel
	fileChangesBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#444")).
				Padding(0, 1).
				MarginTop(1)

	fileChangesTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	fileNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAA"))

	fileAddedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	fileRemovedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196"))

	uncommittedFileStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E5C07B"))

	diffSectionStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F4A261")).
				Bold(true)

	// branch status badges
	notPushedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#666")).
				Padding(0, 1)

	pushedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#0A6EBD")).
				Padding(0, 1)

	conflictsBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#E76F51")).
				Padding(0, 1)

	closedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888")).
				Padding(0, 1)

	remoteDeletedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("196")).
				Padding(0, 1)

	// agent completion badges
	agentSuccessStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#2A9D8F")).
				Padding(0, 1)

	agentFailureStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("196")).
				Padding(0, 1)

	agentErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF")).
			Background(lipgloss.Color("#E76F51")).
			Padding(0, 1)

	agentInProgressStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E9C46A"))

	// inline loading states
	pendingItemStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(lipgloss.Color("#555"))

	pendingLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888")).
				Italic(true)

	// split-pane borders — double border for focused pane so the state
	// is visible even without color (accessibility).
	focusedPaneBorder = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(lipgloss.Color("#F4A261")).
				Padding(0, 1)

	unfocusedPaneBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#444")).
				Padding(0, 1)

	// terminal pane header
	termPaneHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888")).
				Bold(true)

	termPaneHeaderFocusedStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#F4A261")).
					Bold(true)

	// agent selection overlay — reusable styles (avoid NewStyle in loops)
	agentNotFoundStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
	agentInstalledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))

	// config overlay — reusable styles
	configLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#888"))
	configValueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#eee"))

	// error display style
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
