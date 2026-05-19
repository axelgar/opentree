package tui

// Mode represents the active UI state for the TUI dispatcher.
type Mode int

const (
	ModeList Mode = iota
	ModeCreate
	ModeCreateFromIssue
	ModeCreateFromRemote
	ModeDelete
	ModeDiff
	ModePRGenerating
	ModePRCreating
	ModeAgentSelect
	ModeErrorLog
)
