package tui

import (
	"strings"
	"testing"
	"time"
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

// Regression: the tab indexed m.workspaces directly, and that list comes from
// ranging a map. The row the cursor highlighted and the row s/x/r acted on
// could be different workspaces, and both re-randomised on every refresh.
func TestServers_RowsAreOrderedRegardlessOfHowTheyArrived(t *testing.T) {
	m := serversModel(
		serving("gamma", 20003, false, false),
		serving("alpha", 20001, false, false),
		serving("beta", 20002, false, false),
	)

	rows := m.serverRows()
	want := []string{"alpha", "beta", "gamma"}
	for i, name := range want {
		if rows[i].Name != name {
			t.Fatalf("row %d = %q, want %q", i, rows[i].Name, name)
		}
	}

	// The view highlights the cursor's row, and the keys act on it. Both go
	// through the same order or they disagree about which workspace is which.
	m.serversTab.cursor = 1
	if got := m.currentServerName(); got != "beta" {
		t.Errorf("currentServerName() = %q, want beta", got)
	}
	if _, cmd := m.updateServers(keyMsg("s")); cmd == nil {
		t.Error("s did nothing on the row under the cursor")
	}
}

// The sort is by name and not by m.sortMode: sorting by activity would have
// rows reordering themselves under the cursor as agents worked, which is the
// defect this fixes, arrived at deliberately.
func TestServers_RowsIgnoreTheWorkspaceListSort(t *testing.T) {
	m := serversModel(serving("alpha", 20001, false, false), serving("beta", 20002, false, false))
	m.workspaces[0].LastActivity = time.Now()
	m.workspaces[1].LastActivity = time.Now().Add(-time.Hour)
	m.sortMode = sortByActivity

	if got := m.serverRows()[0].Name; got != "alpha" {
		t.Errorf("first row = %q, want alpha — the Servers tab followed the list's sort", got)
	}
}

// And not by the filtered list either: the filter query belongs to the
// Workspaces tab and persists across tabs, so a filter typed there would
// silently hide servers here.
func TestServers_RowsIgnoreTheWorkspaceListFilter(t *testing.T) {
	m := serversModel(serving("alpha", 20001, false, false), serving("beta", 20002, false, false))
	m.filterQuery = "alpha"

	if got := len(m.serverRows()); got != 2 {
		t.Errorf("serverRows() returned %d rows, want 2 — the Workspaces filter leaked in", got)
	}
}

// The Servers cursor is re-anchored by name on the ten-second refresh. Its own
// handler only ever sees a KeyMsg, so the refresh — the one thing that
// reorders rows — never reaches it.
func TestServers_CursorSurvivesARefreshThatChangesTheRows(t *testing.T) {
	m := serversModel(serving("alpha", 20001, false, false), serving("beta", 20002, false, false))
	m.serversTab.cursor = 1 // beta

	next, _ := m.Update(loadedWorkspacesMsg{workspaces: []WorkspaceItem{
		serving("aardvark", 20000, false, false),
		serving("beta", 20002, false, false),
		serving("alpha", 20001, false, false),
	}})
	nm, ok := next.(Model)
	if !ok {
		t.Fatal("update did not return a Model")
	}
	if got := nm.currentServerName(); got != "beta" {
		t.Errorf("cursor moved to %q after a refresh, want it still on beta", got)
	}
}
