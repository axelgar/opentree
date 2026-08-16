package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// agentOutput is what the agent has printed outside the protocol, prefixed with
// whose it is — in a log of the agent's own words, an unattributed block of text
// reads as something it said.
//
// Empty when the agent has printed nothing, which is the normal case and worth
// nothing on screen.
func (m Model) agentOutput() string {
	if m.client == nil {
		return ""
	}
	out := strings.TrimSpace(m.client.Stderr())
	if out == "" {
		return ""
	}
	return m.opts.Agent.Name + " printed:\n" + out
}

// canShowOutput reports whether there is an agent to have printed anything.
func (m Model) canShowOutput() bool { return m.client != nil }

// showOutput puts the agent's own output in the log, for the times it says
// something the protocol has no place for: a warning that the worktree is not a
// trusted folder, a deprecation, a stack trace it survived.
//
// It reads as a command that might find nothing, and says so when it does —
// silence and "there was nothing" look identical otherwise.
func (m Model) showOutput() (tea.Model, tea.Cmd) {
	out := m.agentOutput()
	if out == "" {
		out = m.opts.Agent.Name + " has printed nothing outside the conversation"
	}
	return m.appendNotice(out).relayout(), nil
}
