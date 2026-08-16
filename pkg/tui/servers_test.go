package tui

import (
	"strings"
	"testing"
)

// serversModel is a model already sitting on the Servers tab, for a project
// that configures a dev server.
func serversModel(workspaces ...WorkspaceItem) Model {
	m := newTestModel(workspaces...)
	m.cfg.Workspace.Run = "pnpm dev"
	m.tab = tabServers
	return m
}

// serving is a workspace with a port and, optionally, a live server.
func serving(name string, port int, running, listening bool) WorkspaceItem {
	ws := testWS(name)
	ws.Port = port
	ws.ServerRunning = running
	ws.ServerListening = listening
	return ws
}

// Alive-but-not-listening is not the same as up: a bundler spends a minute
// compiling before it answers, and that minute is exactly when somebody would
// otherwise wonder whether it worked.
func TestServers_ThreeStates(t *testing.T) {
	m := serversModel(
		serving("stopped-one", 20001, false, false),
		serving("starting-one", 20002, true, false),
		serving("up-one", 20003, true, true),
	)

	if got := serverStateOf(m.workspaces[0]); got != serverStopped {
		t.Errorf("no window = %v, want stopped", got)
	}
	if got := serverStateOf(m.workspaces[1]); got != serverStarting {
		t.Errorf("window but no answer = %v, want starting", got)
	}
	if got := serverStateOf(m.workspaces[2]); got != serverUp {
		t.Errorf("port answered = %v, want up", got)
	}

	view := m.View()
	for _, want := range []string{"stopped", "starting…", "up", "http://localhost:20003"} {
		if !strings.Contains(view, want) {
			t.Errorf("the tab does not show %q:\n%s", want, view)
		}
	}
}

// A view that showed only running servers would be empty exactly when you
// opened it to start one.
func TestServers_ListsEveryWorkspace(t *testing.T) {
	m := serversModel(serving("alpha", 0, false, false), serving("beta", 0, false, false))

	view := m.View()
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(view, name) {
			t.Errorf("workspace %q is missing from the tab:\n%s", name, view)
		}
	}
}

// A tab that appeared and disappeared with the config is one people never
// learn is there, so an unconfigured project gets an empty state instead.
func TestServers_EmptyStateWithNoRunCommand(t *testing.T) {
	m := serversModel(serving("alpha", 0, false, false))
	m.cfg.Workspace.Run = ""

	view := m.View()
	if !strings.Contains(view, "No dev server configured") {
		t.Errorf("the tab does not explain itself:\n%s", view)
	}
	if !strings.Contains(view, "run =") {
		t.Errorf("the empty state does not say how to configure one:\n%s", view)
	}
}

// A fresh keyspace: s, x and r mean start, stop and restart here, whatever they
// mean on the workspace list.
func TestServers_KeysActOnTheRowUnderTheCursor(t *testing.T) {
	m := serversModel(serving("alpha", 20001, false, false), serving("beta", 20002, true, true))

	// Stopping one that is not running, and starting one that already is, are
	// refused rather than sent to the service to fail there.
	if _, cmd := m.updateServers(keyMsg("x")); cmd == nil {
		t.Error("x on a stopped server said nothing")
	}
	next, _ := m.updateServers(keyMsg("down"))
	if next.serversTab.cursor != 1 {
		t.Fatalf("cursor = %d, want the second row", next.serversTab.cursor)
	}
	if _, cmd := next.updateServers(keyMsg("s")); cmd == nil {
		t.Error("s on a running server said nothing")
	}

	// o opens the URL of a server that is up, and refuses one that is not.
	if _, cmd := next.updateServers(keyMsg("o")); cmd == nil {
		t.Error("o on a running server did nothing")
	}
	if _, cmd := m.updateServers(keyMsg("o")); cmd == nil {
		t.Error("o on a stopped server said nothing")
	}
}

func TestServers_TabBarNamesTheThirdPlace(t *testing.T) {
	view := newTestModel(testWS("alpha")).View()
	if !strings.Contains(view, "Servers") {
		t.Errorf("the tab bar does not name the Servers tab:\n%s", view)
	}
}

// esc steps back to the list rather than quitting, the way it does from Skills.
func TestServers_EscStepsBack(t *testing.T) {
	m, _ := serversModel(serving("alpha", 0, false, false)).updateServers(keyMsg("esc"))
	if m.tab != tabWorkspaces {
		t.Error("esc did not return to Workspaces")
	}
}
