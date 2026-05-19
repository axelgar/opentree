package tui

// View is the top-level Bubble Tea renderer. It dispatches via m.mode
// to per-mode view functions in mode_*.go.
func (m Model) View() string {
	switch m.mode {
	case ModeErrorLog:
		return viewErrorLog(m)
	case ModeAgentSelect:
		return viewAgentSelect(m)
	case ModeDiff:
		return viewDiff(m)
	case ModeDelete:
		return viewDelete(m)
	case ModeCreateFromIssue:
		return viewCreateFromIssue(m)
	case ModeCreateFromRemote:
		return viewCreateFromRemote(m)
	case ModeCreate:
		return viewCreate(m)
	case ModePRGenerating:
		return viewPRGenerating(m)
	case ModePRCreating:
		return viewPRCreating(m)
	}
	return viewList(m)
}
