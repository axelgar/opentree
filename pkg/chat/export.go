package chat

import (
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// /export writes the conversation to a file, as the markdown ctrl+y's "whole
// conversation" puts on the clipboard: a pull request description, an issue,
// a note to a colleague, a record of what the agent was told before it did
// what it did. The file goes under opentree's own directory rather than into
// the worktree, where it would dirty the branch and end up in a commit.

// ExportsDir is where a repository's exported conversations go:
// ~/.opentree/exports/<repository>, the repository identified the way the
// sockets and the history identify it. Empty with no home directory.
func ExportsDir(repoRoot string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".opentree", "exports", repoKey(repoRoot))
}

// exportedMsg is the file's answer.
type exportedMsg struct {
	path string
	err  error
}

// canExport is whether there is somewhere to write and something to write.
func (m Model) canExport() bool {
	return m.opts.Exports != "" && m.conversationStarted()
}

// exportTranscript writes the conversation as it stands. The document is
// built here, on the event loop, so what goes to the file is what was on
// screen when the command was typed; only the write runs off it.
func (m Model) exportTranscript() (tea.Model, tea.Cmd) {
	text := m.transcriptMarkdown()
	dir := m.opts.Exports
	name := workspaceFile(m.opts.Workspace) + "-" + time.Now().Format("20060102-150405") + ".md"
	return m, func() tea.Msg {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return exportedMsg{err: err}
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			return exportedMsg{err: err}
		}
		return exportedMsg{path: path}
	}
}

// exported says where the file went, in the log — a path is worth keeping,
// and a flash would take it away before it could be copied.
func (m Model) exported(msg exportedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		next, cmd := m.withFlash("could not export: "+firstLine(msg.err.Error()), true)
		return next, cmd
	}
	return m.appendNotice("exported to " + shortHome(msg.path)).relayout(), nil
}
