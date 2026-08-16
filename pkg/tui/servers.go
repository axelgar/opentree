package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/ui"
)

// The Servers tab is a place, like Skills: an inventory you come back to rather
// than something you open, act on and close.
//
// It lists every workspace, not only the ones serving. A view that showed only
// running servers would be empty exactly when you opened it to start one, and a
// tab that appeared and disappeared with the config is one people never learn
// is there.
//
// Nothing here is remembered. The rows come from the tmux window list and a
// dial at the port, both read on the same refresh the workspace list already
// runs — a server is a process, and the process list is the only thing about it
// that cannot be stale.

// serversTab is the tab's own state, which is a cursor and nothing else: the
// rows are the workspaces the dashboard already loaded.
type serversTab struct {
	cursor int
}

// serverState is what a row says a workspace's server is doing.
type serverState int

const (
	// serverStopped is no run window: nothing to attach to, nothing serving.
	serverStopped serverState = iota
	// serverStarting is a live window whose port answers nothing yet — the
	// minute a bundler spends before it listens, which is exactly the minute
	// somebody would otherwise spend wondering whether it worked.
	serverStarting
	// serverUp is a port that answered.
	serverUp
)

// serverStateOf reads the two facts a row is made of.
func serverStateOf(ws WorkspaceItem) serverState {
	switch {
	case !ws.ServerRunning:
		return serverStopped
	case ws.ServerListening:
		return serverUp
	}
	return serverStarting
}

// serverURL is where a running server can be reached, or "" for one that is not.
func serverURL(ws WorkspaceItem) string {
	if ws.Port == 0 || serverStateOf(ws) != serverUp {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d", ws.Port)
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

// updateServers drives the tab. Its keys are its own: a tab is a fresh
// keyspace, so s/x/r mean start, stop and restart here without arguing with
// what they mean on the workspace list.
func (m Model) updateServers(msg tea.KeyMsg) (Model, tea.Cmd) {
	rows := m.workspaces
	if pickerMove(msg.String(), &m.serversTab.cursor, len(rows)) {
		return m, nil
	}
	if m.serversTab.cursor >= len(rows) {
		m.serversTab.cursor = max(0, len(rows)-1)
	}

	switch msg.String() {
	case "tab", "right", "esc":
		// The bar wraps: forward from the last place is the first one, and esc
		// steps back to the list rather than leaving opentree.
		m.tab = tabWorkspaces
		return m, nil
	case "shift+tab", "left":
		m.tab = tabSkills
		return m, nil
	case "q":
		return m, tea.Quit
	}

	if len(rows) == 0 {
		return m, nil
	}
	ws := rows[m.serversTab.cursor]

	switch msg.String() {
	case "s":
		if ws.ServerRunning {
			return m, m.transientErrCmd(fmt.Sprintf("%s's server is already running", ws.Name))
		}
		return m, m.startServerCmd(ws.Name)

	case "x":
		if !ws.ServerRunning {
			return m, m.transientErrCmd(fmt.Sprintf("%s's server is not running", ws.Name))
		}
		return m, m.stopServerCmd(ws.Name)

	case "r":
		return m, m.restartServerCmd(ws.Name)

	case "enter":
		// Attaching is the answer to "where did my output go": the server is a
		// process in a window, and the window has all of it.
		if !ws.ServerRunning {
			return m, m.transientErrCmd(fmt.Sprintf("%s has no server window to attach to", ws.Name))
		}
		return m, m.attachServerCmd(ws.Name)

	case "o":
		url := serverURL(ws)
		if url == "" {
			return m, m.transientErrCmd(fmt.Sprintf("%s is not serving yet", ws.Name))
		}
		return m, openURLCmd(url)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// serversView renders the Servers tab.
func (m Model) serversView() string {
	var s strings.Builder
	s.WriteString(renderLogo())
	s.WriteString("\n\n")
	s.WriteString(m.tabBar())
	s.WriteString("\n\n")

	// One empty state for the project that configures no server, rather than a
	// tab that comes and goes with the config.
	if m.cfg.Workspace.Run == "" {
		s.WriteString(itemStyle.Render("No dev server configured.") + "\n")
		s.WriteString(diffStyle.Render(
			"  Set [workspace] run in opentree.toml — for example run = \"pnpm dev\" —") + "\n")
		s.WriteString(diffStyle.Render(
			"  and opentree starts one per worktree, each on a port of its own.") + "\n")
		s.WriteString("\n" + m.toastLine() + "\n")
		s.WriteString(m.serverHelp())
		return appStyle.Render(s.String())
	}

	if len(m.workspaces) == 0 {
		s.WriteString(itemStyle.Render("No workspaces yet.") + "\n")
		s.WriteString(diffStyle.Render("  Create one with 'n' on the Workspaces tab.") + "\n")
		s.WriteString("\n" + m.toastLine() + "\n")
		s.WriteString(m.serverHelp())
		return appStyle.Render(s.String())
	}

	for i, ws := range m.workspaces {
		s.WriteString(m.renderServerRow(ws, i == m.serversTab.cursor))
	}
	s.WriteString("\n" + m.toastLine() + "\n")
	s.WriteString(m.serverHelp())
	return appStyle.Render(s.String())
}

// renderServerRow is one workspace's line: what its server is doing, and where
// to reach it.
func (m Model) renderServerRow(ws WorkspaceItem, selected bool) string {
	style, cursor := itemStyle, "  "
	if selected {
		style, cursor = selectedItemStyle, "> "
	}

	var badge, where string
	switch serverStateOf(ws) {
	case serverUp:
		badge = agentWorkingStyle.Render("up")
		where = serverURL(ws)
	case serverStarting:
		badge = agentIdleStyle.Render("starting…")
		where = fmt.Sprintf("port %d, nothing listening yet", ws.Port)
	default:
		badge = dangerStyle.Render("stopped")
		if ws.Port != 0 {
			// The port a stopped server would come back on. It is kept for the
			// workspace's life, so it is worth saying even when nothing is
			// serving: an OAuth redirect URI was registered against it.
			where = fmt.Sprintf("port %d when started", ws.Port)
		}
	}

	line := fmt.Sprintf("%s%s  %s", cursor, ws.Name, badge)
	out := style.Render(ui.Truncate(line, m.panelWidth()))
	if where != "" {
		out += "\n" + diffStyle.Render("    "+where)
	}
	return out + "\n"
}

// serverHelp is this tab's keys, which are not the workspace list's.
func (m Model) serverHelp() string {
	return helpStyle.Render(
		"↑/k ↓/j move • s start • x stop • r restart • enter attach • o open • tab workspaces")
}
