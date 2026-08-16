package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/bootstrap"
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
//
// Under portless it is the name rather than the port, which is the whole reason
// to use it — https://feat-dark-mode.opentree.localhost reads as "this branch
// of this project", where :20431 reads as nothing.
func (m Model) serverURL(ws WorkspaceItem) string {
	if ws.Port == 0 || serverStateOf(ws) != serverUp {
		return ""
	}
	if m.portless.Ready {
		return "https://" + bootstrap.PortlessHost(ws.Name, filepath.Base(m.repoRoot))
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
		url := m.serverURL(ws)
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
	if note := m.portlessNote(); note != "" {
		s.WriteString("\n" + note + "\n")
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
		where = m.serverURL(ws)
		if m.portless.Ready {
			// The port stays on the row beside the name. It is what an OAuth
			// redirect URI was registered against, and it is still the way in
			// for anything that is not a browser.
			where += fmt.Sprintf("  ·  :%d", ws.Port)
		}
	case serverStarting:
		badge = agentIdleStyle.Render("starting…")
		if ws.Port != 0 {
			where = fmt.Sprintf("port %d, nothing listening yet", ws.Port)
		}
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

// portlessNote says what portless would add, or why an installed one is not
// being used. It is a line at the bottom rather than a badge per row: it is a
// property of the machine, and the same either way for every workspace here.
func (m Model) portlessNote() string {
	switch {
	case m.portless.Ready:
		return ""
	case m.portless.Reason != "":
		// Decision made for the user, and said out loud: starting portless
		// needs a CA, a hosts file and a root service, and it asks for them
		// with a sudo prompt — in a detached window nobody would ever see it.
		return diffStyle.Render("  " + m.portless.Reason + " — serving on ports meanwhile")
	}
	return diffStyle.Render(
		"  Install vercel-labs/portless for https://<branch>.<repo>.localhost instead of ports. opentree works either way.")
}

// serverHelp is this tab's keys, which are not the workspace list's.
func (m Model) serverHelp() string {
	return helpStyle.Render(
		"↑/k ↓/j move • s start • x stop • r restart • enter attach • o open • tab workspaces")
}
