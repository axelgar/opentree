package chat

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// /shell opens a shell in this worktree, in a tmux window beside this one,
// and goes to it — for the moment the agent asks for something only a person
// at a prompt can do. It runs `opentree shell <workspace>` rather than
// talking to tmux itself: the command already knows the window's name, how
// to reuse one that exists, and how to move a client to it from inside the
// session, and a second copy of that here would be the one that drifts.

// canOpenShell is whether there is a tmux session to open the window in.
// Outside tmux — a chat run by hand — there is nowhere beside this one.
func (m Model) canOpenShell() bool {
	return os.Getenv("TMUX") != "" && m.opts.Workspace != ""
}

// shellOpenedMsg is the command's answer. Nothing on success: the user is
// looking at the shell by then.
type shellOpenedMsg struct{ err string }

func (m Model) openShell() (tea.Model, tea.Cmd) {
	name := m.opts.Workspace
	return m, func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "opentree"
		}
		out, err := exec.Command(exe, "shell", name).CombinedOutput() // #nosec G204 -- opentree's own binary and a workspace name it was started with
		if err != nil {
			reason := firstLine(strings.TrimSpace(string(out)))
			if reason == "" {
				reason = err.Error()
			}
			return shellOpenedMsg{err: reason}
		}
		return shellOpenedMsg{}
	}
}

func (m Model) shellOpened(msg shellOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.err == "" {
		return m, nil
	}
	next, cmd := m.withFlash("could not open a shell: "+msg.err, true)
	return next, cmd
}
