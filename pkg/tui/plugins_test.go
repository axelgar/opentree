package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/plugins"
)

// pluginsModel is a model already sitting on the Plugins tab.
func pluginsModel(list ...plugins.Plugin) Model {
	m := newTestModel()
	m.tab = tabPlugins
	m.pluginsTab.list = list
	return m
}

func testPlugin(name string) plugins.Plugin {
	return plugins.Plugin{
		Name:        name,
		Version:     "1.2.0",
		Description: name + " does things",
		Dir:         "/home/u/.opentree/plugins/" + name,
		Skills:      []string{"deploy"},
	}
}

func TestPlugins_TabBarNamesTheFourthPlace(t *testing.T) {
	m := pluginsModel()
	view := m.View()
	for _, name := range []string{"Workspaces", "Skills", "Plugins", "Servers"} {
		if !strings.Contains(view, name) {
			t.Errorf("tab bar is missing %q:\n%s", name, view)
		}
	}
	if !strings.Contains(view, "No plugins installed") {
		t.Errorf("empty state did not render:\n%s", view)
	}
}

// A row says what the plugin declares, because that is what the tab is for:
// the skills already list on the Skills tab, but the plugin as a unit — its
// servers and its failures — only shows here.
func TestPlugins_RowShowsDeclarationsAndProblems(t *testing.T) {
	p := testPlugin("tools-plugin")
	p.Servers = []plugins.Server{{Name: "validator", Type: "stdio", Command: "./bin/validator"}}
	p.Problems = []string{`mcp server "broken" skipped: unknown type "websocket"`}
	view := pluginsModel(p).View()

	for _, want := range []string{"tools-plugin 1.2.0", "1 skill · 1 mcp",
		"mcp validator · stdio · ./bin/validator", "⚠ 1 problem", `"broken" skipped`} {
		if !strings.Contains(view, want) {
			t.Errorf("row is missing %q:\n%s", want, view)
		}
	}
}

// Same invariant as the other tabs: every key is consumed, because a fallen-
// through "n" would open a workspace dialog the view never draws.
func TestPluginsTab_SwallowsWorkspaceKeys(t *testing.T) {
	m := pluginsModel(testPlugin("tools-plugin"))
	for _, k := range []string{"n", "i", "p", "o", "A", "s", "m"} {
		got, _ := m.updatePlugins(keyMsg(k))
		if got.creating || got.prCreating || got.agentSelecting || got.prompting || got.tab != tabPlugins {
			t.Errorf("key %q leaked into the workspace handler", k)
		}
	}
}

// x asks before it deletes: removal takes a store directory and every link
// pointing into it, in one keypress otherwise.
func TestPluginsRemove_ConfirmsFirst(t *testing.T) {
	m := pluginsModel(testPlugin("tools-plugin"))
	got, _ := m.updatePlugins(keyMsg("x"))
	if got.pluginsTab.removing == nil {
		t.Fatal("x did not open the confirmation")
	}
	view := got.View()
	if !strings.Contains(view, `Remove plugin "tools-plugin"?`) {
		t.Errorf("the confirmation does not name the plugin:\n%s", view)
	}

	got, _ = got.updatePlugins(keyMsg("n"))
	if got.pluginsTab.removing != nil {
		t.Error("n did not cancel the removal")
	}
}

// A pasted URL is one key carrying every rune, and a URL is far more often
// pasted than typed.
func TestPluginsAdd_AcceptsAPastedURL(t *testing.T) {
	m := pluginsModel()
	got, _ := m.updatePlugins(keyMsg("a"))
	if !got.pluginsTab.adding {
		t.Fatal("a did not open the prompt")
	}
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("https://github.com/x/plugin")}
	got, _ = got.updatePlugins(paste)
	if got.pluginsTab.addURL != "https://github.com/x/plugin" {
		t.Errorf("addURL = %q after a paste", got.pluginsTab.addURL)
	}
	got, _ = got.updatePlugins(keyMsg("esc"))
	if got.pluginsTab.adding || got.pluginsTab.addURL != "" {
		t.Error("esc did not cancel the prompt")
	}
}

// Enter on the prompt starts the install and marks the tab busy, so a second
// a or x cannot race the store writes the first one is making.
func TestPluginsAdd_EnterInstallsAndGuards(t *testing.T) {
	m := pluginsModel()
	got, _ := m.updatePlugins(keyMsg("a"))
	got.pluginsTab.addURL = "https://github.com/x/plugin"
	got, cmd := got.updatePlugins(keyMsg("enter"))
	if !got.pluginsTab.busy || cmd == nil {
		t.Fatal("enter did not start the install")
	}
	got, cmd = got.updatePlugins(keyMsg("a"))
	if got.pluginsTab.adding || cmd == nil {
		t.Error("a mid-install should refuse with a message rather than open the prompt")
	}
}

// The install's outcome lands as a message; both inventories rescan, because
// the store gained a plugin and the agents' trees gained its skills.
func TestPluginInstalled_ClearsBusyAndRescans(t *testing.T) {
	m := pluginsModel()
	m.pluginsTab.busy = true
	next, cmd := m.Update(pluginInstalledMsg{plugin: testPlugin("tools-plugin"), linked: 4})
	if next.(Model).pluginsTab.busy {
		t.Error("the outcome did not clear busy")
	}
	if cmd == nil {
		t.Error("nothing was rescanned after an install")
	}
}

// A refresh can shrink the list under the cursor — a plugin removed from the
// CLI while the tab was open.
func TestPluginsScanned_ClampsTheCursor(t *testing.T) {
	m := pluginsModel(testPlugin("a-plugin"), testPlugin("b-plugin"))
	m.pluginsTab.cursor = 1
	next, _ := m.Update(pluginsScannedMsg{list: []plugins.Plugin{testPlugin("a-plugin")}})
	if got := next.(Model).pluginsTab.cursor; got != 0 {
		t.Errorf("cursor = %d after the list shrank, want 0", got)
	}
}
