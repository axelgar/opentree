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

	// delete confirmation styles
	dangerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	// The title chip of a destructive card. Same shape as titleStyle so the
	// dialogs read as one family, in the colour that says what this one does.
	dangerTitleStyle = titleStyle.
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("196"))

	// The key hints inside a dialog card. helpStyle's top margin is for the
	// bottom of a full screen; the card already puts a blank line there.
	dialogHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	confirmKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261")).
			Bold(true)

	confirmLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262"))

	// two-step create dialog
	stepLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Italic(true)

	// Palette anchors for "it worked", "it needs attention" and "it failed".
	// Every place that means one of those three renders through these, so the
	// program reads as one program: a CI badge, an agent's readiness in the
	// picker and the toast slot are the same green as each other.
	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E9C46A"))

	toastErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E76F51"))

	// CI badge styles
	ciSuccessStyle = successStyle.Bold(true)

	ciFailureStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	ciPendingStyle = warnStyle

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

	// The rule above the status bar and the numbers inside it: the bar reads
	// as a bar when the counts stand out from their labels and the whole line
	// is fenced off from the list above.
	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#333"))

	statNumStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EEEEEE"))

	// per-row action hint under the selected workspace
	rowHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555")).
			Italic(true)

	// "n more" markers when the list is taller than the terminal
	scrollHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666"))

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

	// branch status badges. Chips are for states that invite action (open,
	// merged, conflicts, remote deleted); passive facts (not pushed, closed)
	// are plain dim text — padding without a background reads as a chip that
	// failed to load.
	notPushedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#666"))

	pushedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#0A6EBD")).
				Padding(0, 1)

	conflictsBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#E76F51")).
				Padding(0, 1)

	closedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	remoteDeletedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("196")).
				Padding(0, 1)

	// agent completion badges
	// Agent liveness badges. Fresh states (working, waiting) draw the eye;
	// stale states (stalled, idle) are dimmed so a parked worktree recedes.
	agentWorkingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E9C46A")) // yellow — actively generating

	agentWaitingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000")).
				Background(lipgloss.Color("#F4A261")). // amber — fresh, your turn
				Bold(true).
				Padding(0, 1)

	agentIdleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666")) // grey — parked/stale

	// tab bar: the active tab wears the title chip, the others recede
	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262")).
				Padding(0, 1)

	// skills
	skillScopeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666"))

	sharedTagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#88C0D0"))

	// A skill an agent has been told to ignore: greyed rather than dropped, so
	// the row still says it is installed.
	skillOffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555"))

	// inline loading states
	pendingItemStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(lipgloss.Color("#555"))

	pendingLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888")).
				Italic(true)
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
