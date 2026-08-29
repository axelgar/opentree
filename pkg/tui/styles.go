package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/ui"
)

// Styles. The colours come from pkg/ui, where each is an AdaptiveColor: these
// were all chosen against a dark terminal, and several were unreadable on a
// light one.
var (
	appStyle = lipgloss.NewStyle().Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF7DB")).
			Background(lipgloss.Color("#888B7E")).
			Padding(0, 1)

	selectedItemStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(ui.Accent).
				Foreground(ui.Accent).
				Padding(0, 1)

	itemStyle = lipgloss.NewStyle().
			Padding(0, 1)

	diffStyle = lipgloss.NewStyle().
			Foreground(ui.Whisper)

	activeStyle = lipgloss.NewStyle().
			Foreground(ui.Success)

	idleStyle = lipgloss.NewStyle().
			Foreground(ui.Warn)

	stoppedStyle = lipgloss.NewStyle().
			Foreground(ui.Faint)

	helpStyle = lipgloss.NewStyle().
			Foreground(ui.Dim).
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

	autopilotBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#8250DF")).
				Padding(0, 1)

	fanoutBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#0E7490")).
				Padding(0, 1)

	// delete confirmation styles
	dangerStyle = lipgloss.NewStyle().
			Foreground(ui.Danger).
			Bold(true)

	// The title chip of a destructive card. Same shape as titleStyle so the
	// dialogs read as one family, in the colour that says what this one does.
	dangerTitleStyle = titleStyle.
				Foreground(lipgloss.Color("#FFF")).
				Background(ui.Danger)

	// The key hints inside a dialog card. helpStyle's top margin is for the
	// bottom of a full screen; the card already puts a blank line there.
	dialogHintStyle = lipgloss.NewStyle().
			Foreground(ui.Dim)

	confirmKeyStyle = lipgloss.NewStyle().
			Foreground(ui.Accent).
			Bold(true)

	confirmLabelStyle = lipgloss.NewStyle().
				Foreground(ui.Dim)

	// two-step create dialog
	stepLabelStyle = lipgloss.NewStyle().
			Foreground(ui.Meta).
			Italic(true)

	// Palette anchors for "it worked", "it needs attention" and "it failed".
	// Every place that means one of those three renders through these, so the
	// program reads as one program: a CI badge, an agent's readiness in the
	// picker and the toast slot are the same green as each other.
	successStyle = lipgloss.NewStyle().
			Foreground(ui.Success)

	warnStyle = lipgloss.NewStyle().
			Foreground(ui.Warn)

	toastErrStyle = lipgloss.NewStyle().
			Foreground(ui.Toast)

	// CI badge styles
	ciSuccessStyle = successStyle.Bold(true)

	ciFailureStyle = lipgloss.NewStyle().
			Foreground(ui.Danger).
			Bold(true)

	ciPendingStyle = warnStyle

	// multi-select
	selectedMarkStyle = lipgloss.NewStyle().
				Foreground(ui.Accent).
				Bold(true)

	// filter prompt
	filterPromptStyle = lipgloss.NewStyle().
				Foreground(ui.Accent)

	// status bar
	statusBarStyle = lipgloss.NewStyle().
			Foreground(ui.Dim)

	// The rule above the status bar and the numbers inside it: the bar reads
	// as a bar when the counts stand out from their labels and the whole line
	// is fenced off from the list above.
	dividerStyle = lipgloss.NewStyle().
			Foreground(ui.Divider)

	statNumStyle = lipgloss.NewStyle().
			Foreground(ui.Body)

	// per-row action hint under the selected workspace
	rowHintStyle = lipgloss.NewStyle().
			Foreground(ui.Whisper).
			Italic(true)

	// "n more" markers when the list is taller than the terminal
	scrollHintStyle = lipgloss.NewStyle().
			Foreground(ui.Faint)

	// error log
	errLogTitleStyle = lipgloss.NewStyle().
				Foreground(ui.Danger).
				Bold(true)

	errLogLineStyle = lipgloss.NewStyle().
			Foreground(ui.Muted)

	// uncommitted changes
	uncommittedStyle = lipgloss.NewStyle().
				Foreground(ui.Warn)

	// diff view
	diffAddStyle    = lipgloss.NewStyle().Foreground(ui.Success)
	diffRemoveStyle = lipgloss.NewStyle().Foreground(ui.Danger)
	diffHunkStyle   = lipgloss.NewStyle().Foreground(ui.Info)
	diffFileStyle   = lipgloss.NewStyle().Foreground(ui.Meta).Bold(true)

	// file changes panel
	fileChangesBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ui.Border).
				Padding(0, 1).
				MarginTop(1)

	fileChangesTitleStyle = lipgloss.NewStyle().
				Foreground(ui.Meta)

	fileNameStyle = lipgloss.NewStyle().
			Foreground(ui.Muted)

	fileAddedStyle = lipgloss.NewStyle().
			Foreground(ui.Success)

	fileRemovedStyle = lipgloss.NewStyle().
				Foreground(ui.Danger)

	uncommittedFileStyle = lipgloss.NewStyle().
				Foreground(ui.WarnFile)

	diffSectionStyle = lipgloss.NewStyle().
				Foreground(ui.Accent).
				Bold(true)

	// branch status badges. Chips are for states that invite action (open,
	// merged, conflicts, remote deleted); passive facts (not pushed, closed)
	// are plain dim text — padding without a background reads as a chip that
	// failed to load.
	notPushedBadgeStyle = lipgloss.NewStyle().
				Foreground(ui.Faint)

	pushedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#0A6EBD")).
				Padding(0, 1)

	conflictsBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(ui.Toast).
				Padding(0, 1)

	closedBadgeStyle = lipgloss.NewStyle().
				Foreground(ui.Meta)

	remoteDeletedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(ui.Danger).
				Padding(0, 1)

	// agent completion badges
	// Agent liveness badges. Fresh states (working, waiting) draw the eye;
	// stale states (stalled, idle) are dimmed so a parked worktree recedes.
	agentWorkingStyle = lipgloss.NewStyle().
				Foreground(ui.Warn) // yellow — actively generating

	agentWaitingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000")).
				Background(ui.Accent). // amber — fresh, your turn
				Bold(true).
				Padding(0, 1)

	agentIdleStyle = lipgloss.NewStyle().
			Foreground(ui.Faint) // grey — parked/stale

	// tab bar: the active tab wears the title chip, the others recede
	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(ui.Dim).
				Padding(0, 1)

	// skills
	skillScopeStyle = lipgloss.NewStyle().
			Foreground(ui.Faint)

	sharedTagStyle = lipgloss.NewStyle().
			Foreground(ui.Info)

	// A skill an agent has been told to ignore: greyed rather than dropped, so
	// the row still says it is installed.
	skillOffStyle = lipgloss.NewStyle().
			Foreground(ui.Whisper)

	// inline loading states
	pendingItemStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(ui.Whisper)

	pendingLabelStyle = lipgloss.NewStyle().
				Foreground(ui.Meta).
				Italic(true)
)
