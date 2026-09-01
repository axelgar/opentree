package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/registry"
)

// The Agents tab is `opentree agents` in the dashboard, so these tests hold
// it to the command line's behaviour: the same refusals, the same consent
// text, the same outcome sentences.

// snapshotAgentList guards the package-level registry: reloadAgents replaces
// it, and -shuffle runs these tests in any order against every other test
// in the package. No test in this file may call t.Parallel.
func snapshotAgentList(t *testing.T) {
	t.Helper()
	prev := config.PredefinedAgents
	config.PredefinedAgents = append([]config.PredefinedAgent{}, prev...)
	t.Cleanup(func() { config.PredefinedAgents = prev })
}

// emptyHome points HOME at a fresh directory, so the store holds only what
// the test put there and selecting writes config somewhere disposable.
func emptyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Chdir(t.TempDir())
	return home
}

// fabricateInstall writes a registry install the loader accepts: a record
// naming an executable that exists.
func fabricateInstall(t *testing.T, home, id, name, version string) string {
	t.Helper()
	dir := filepath.Join(home, ".opentree", "registry", "agents", id)
	bin := filepath.Join(dir, "npm", "bin", id)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- a test's fake agent binary
		t.Fatal(err)
	}
	record := map[string]any{
		"entry": map[string]any{
			"id": id, "name": name, "version": version, "description": "a test agent",
			"distribution": map[string]any{"npx": map[string]any{"package": id + "@" + version}},
		},
		"index_url": registry.DefaultIndexURL,
		"command":   bin,
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.json"), data, 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
	return bin
}

// fabricateBrokenInstall leaves a directory in the store whose record does
// not parse — exactly what `agents remove` and the tab's x exist to clear.
func fabricateBrokenInstall(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, ".opentree", "registry", "agents", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.json"), []byte("{not json"), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}

// agentsModel is a model on the Agents tab with the store reloaded, and
// readiness stubbed so the rows do not depend on this machine.
func agentsModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel()
	m.tab = tabAgents
	m.agentReadiness = readinessOf(agentReady)
	m.reloadAgents()
	return m
}

// cursorOn moves the tab's cursor to the row for the given command or id.
func cursorOn(t *testing.T, m *Model, id string) {
	t.Helper()
	for i, r := range m.agentsTab.rows {
		if r.agent.Command == id || r.id == id {
			m.agentsTab.cursor = i
			return
		}
	}
	t.Fatalf("no row for %q among %d rows", id, len(m.agentsTab.rows))
}

func npmEntry(id, name, version string) registry.Entry {
	return registry.Entry{
		ID: id, Name: name, Version: version, Description: name + " does agent things",
		Distribution: registry.Distribution{Npx: &registry.PackageDist{Package: "@x/" + id}},
	}
}

func uvxEntry(id string) registry.Entry {
	return registry.Entry{
		ID: id, Name: id, Version: "1.0.0", Description: "python only",
		Distribution: registry.Distribution{Uvx: &registry.PackageDist{Package: id}},
	}
}

// ---------------------------------------------------------------------------
// The list
// ---------------------------------------------------------------------------

func TestAgents_TabBarNamesTheFivePlaces(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	view := ansi.Strip(agentsModel(t).View())
	for _, name := range []string{"Workspaces", "Agents", "Skills", "Plugins", "Servers"} {
		if !strings.Contains(view, name) {
			t.Errorf("tab bar is missing %q:\n%s", name, view)
		}
	}
	if !strings.Contains(view, "a installs more from the ACP Registry") {
		t.Errorf("with nothing installed the tab does not say how to add:\n%s", view)
	}
}

// A row says where the agent came from, the way `agents list` does: the
// built-ins are built-in, a registry install wears its version.
func TestAgents_RowsShowSourceAndTheActiveOne(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "3000.6.7")
	m := agentsModel(t)
	m.cfg.Agent.Command = "devin"
	view := ansi.Strip(m.View())

	for _, want := range []string{"Claude Code", "built-in", "Devin (active)", "registry 3000.6.7"} {
		if !strings.Contains(view, want) {
			t.Errorf("rows are missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "a installs more") {
		t.Error("the how-to-add hint should go once something is installed")
	}
}

// A store entry whose record does not load is still a row, because x is how
// it gets cleared — hiding it would make the directory unreachable from here.
func TestAgents_BrokenInstallIsARow(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateBrokenInstall(t, home, "ghost")
	m := agentsModel(t)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "ghost") || !strings.Contains(view, "broken install") {
		t.Fatalf("the broken install is not a row:\n%s", view)
	}
	cursorOn(t, &m, "ghost")
	got, _ := m.updateAgents(keyMsg("x"))
	if got.agentsTab.removing == nil {
		t.Fatal("x on a broken install did not offer to clear it")
	}
	got, cmd := got.updateAgents(keyMsg("y"))
	if !got.agentsTab.busy || cmd == nil {
		t.Error("y did not start the removal")
	}
}

// Same invariant as the other tabs: every key is consumed, because a fallen-
// through "n" would open a workspace dialog the view never draws.
func TestAgentsTab_SwallowsWorkspaceKeys(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	for _, k := range []string{"n", "p", "o", "A", "s", "m", "d"} {
		got, _ := m.updateAgents(keyMsg(k))
		if got.creating || got.prCreating || got.agentSelecting || got.prompting || got.tab != tabAgents {
			t.Errorf("key %q leaked into the workspace handler", k)
		}
	}
}

// ---------------------------------------------------------------------------
// use, setup
// ---------------------------------------------------------------------------

func TestAgentsUse_EnterWritesTheRepoConfig(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	cursorOn(t, &m, "claude")
	got, cmd := m.updateAgents(keyMsg("enter"))
	if got.cfg.Agent.Command != "claude" || cmd == nil {
		t.Fatalf("enter did not select claude: command=%q", got.cfg.Agent.Command)
	}
	if !strings.Contains(got.notice, "for this repository") {
		t.Errorf("notice = %q, want the scope named", got.notice)
	}
}

func TestAgentsUse_GWritesTheGlobalConfig(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	cursorOn(t, &m, "gemini")
	got, _ := m.updateAgents(keyMsg("g"))
	if !strings.Contains(got.notice, "everywhere") {
		t.Errorf("notice = %q, want everywhere", got.notice)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "opentree", "opentree.toml"))
	if err != nil {
		t.Fatalf("g did not write the global config: %v", err)
	}
	if !strings.Contains(string(data), "command = 'gemini'") {
		t.Errorf("global config = %q", data)
	}
}

// The remedy for an agent that is not there depends on where it came from:
// the built-in is installed by its vendor, the registry install by u.
func TestAgentsUse_NotFoundNamesTheRemedy(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	m := agentsModel(t)
	m.agentReadiness = readinessOf(agentNotFound)

	cursorOn(t, &m, "claude")
	got, _ := m.updateAgents(keyMsg("enter"))
	if got.err == nil || !strings.Contains(got.err.Error(), "install claude first") {
		t.Errorf("built-in refusal = %v", got.err)
	}
	cursorOn(t, &m, "devin")
	got, _ = m.updateAgents(keyMsg("enter"))
	if got.err == nil || !strings.Contains(got.err.Error(), "u reinstalls it") {
		t.Errorf("registry refusal = %v", got.err)
	}
}

// Enter on a built-in whose adapter is missing asks about the download and
// remembers both the switch and, for g, where the switch is written.
func TestAgentsUse_AdapterMissingArmsTheCardWithScope(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	m.agentReadiness = readinessOf(agentAdapterMissing)
	cursorOn(t, &m, "claude")
	got, _ := m.updateAgents(keyMsg("g"))
	if got.agentInstallConfirm == nil || got.agentPendingSelect == nil {
		t.Fatal("g did not arm the adapter card with a pending switch")
	}
	if got.agentPendingPath != config.GlobalConfigPath() {
		t.Errorf("pending path = %q, want the global config", got.agentPendingPath)
	}
	if !strings.Contains(ansi.Strip(got.View()), "Install adapter for Claude Code?") {
		t.Error("the tab does not draw the adapter card")
	}
	got, _ = got.updateAgents(keyMsg("n"))
	if got.agentInstallConfirm != nil || got.agentPendingPath != "" {
		t.Error("n did not clear the card and its scope")
	}
}

// i is `agents setup`: a registry install is managed by u, and says so.
func TestAgentsSetup_RegistryInstallPointsAtU(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	m := agentsModel(t)
	cursorOn(t, &m, "devin")
	got, _ := m.updateAgents(keyMsg("i"))
	if got.err == nil || !strings.Contains(got.err.Error(), "installed from the ACP Registry — u updates it") {
		t.Errorf("i on a registry install: %v", got.err)
	}
}

func TestAgentsSetup_BuiltInArmsTheAdapterCard(t *testing.T) {
	noAdapterAnywhere(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	cursorOn(t, &m, "claude")
	got, _ := m.updateAgents(keyMsg("i"))
	if got.agentInstallConfirm == nil || got.agentPendingSelect != nil {
		t.Error("i should arm the download without a pending switch")
	}
}

// ---------------------------------------------------------------------------
// add: the browser and the consent card
// ---------------------------------------------------------------------------

func TestAgentsAdd_AFetchesThenBrowses(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	got, cmd := m.updateAgents(keyMsg("a"))
	if !got.agentsTab.busy || cmd == nil {
		t.Fatal("a did not start the fetch")
	}
	got, cmd = got.updateAgents(keyMsg("a"))
	if cmd == nil || got.agentsTab.browsing {
		t.Error("a second a mid-fetch should refuse rather than fetch twice")
	}

	idx := registry.Index{Agents: []registry.Entry{npmEntry("auggie", "Auggie CLI", "0.36.0"), uvxEntry("pyagent")}}
	got, _ = applyUpdate(got, registryIndexMsg{index: idx, note: "using the index cached 3h ago — offline", purpose: indexBrowse})
	if got.agentsTab.busy || !got.agentsTab.browsing {
		t.Fatal("the index did not open the browser")
	}
	view := ansi.Strip(got.View())
	for _, want := range []string{"ACP Registry — 2 agents", "auggie", "0.36.0", "npm",
		"pyagent", "uvx — not supported yet", "cached 3h ago"} {
		if !strings.Contains(view, want) {
			t.Errorf("browser is missing %q:\n%s", want, view)
		}
	}
}

func TestAgentsBrowser_FiltersLikeSearch(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	m.agentsTab.browsing = true
	m.agentsTab.index = []registry.Entry{npmEntry("auggie", "Auggie CLI", "0.36.0"), npmEntry("goose", "goose", "1.0.0")}

	got, _ := m.updateAgents(keyMsg("/"))
	got, _ = got.updateAgents(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("GOO")})
	if len(got.visibleEntries()) != 1 || got.visibleEntries()[0].ID != "goose" {
		t.Errorf("filter %q matched %d entries", got.agentsTab.filter, len(got.visibleEntries()))
	}
	got, _ = got.updateAgents(keyMsg("enter")) // done typing
	if got.agentsTab.filtering {
		t.Error("enter did not close the filter prompt")
	}
	// esc clears the filter first, then closes the browser.
	got, _ = got.updateAgents(keyMsg("esc"))
	if got.agentsTab.filter != "" || !got.agentsTab.browsing {
		t.Error("the first esc should clear the filter and keep browsing")
	}
	got, _ = got.updateAgents(keyMsg("esc"))
	if got.agentsTab.browsing {
		t.Error("the second esc should close the browser")
	}
}

// Enter on an entry runs `agents add`'s gate, ending in the consent card
// with Describe()'s words — or in the same refusals the command gives.
func TestAgentsBrowser_EnterShowsTheConsentCard(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	m.agentsTab.browsing = true
	entry := npmEntry("auggie", "Auggie CLI", "0.36.0")
	m.agentsTab.index = []registry.Entry{entry}

	got, _ := m.updateAgents(keyMsg("enter"))
	if got.agentsTab.confirm == nil || got.agentsTab.browsing {
		t.Fatal("enter did not open the consent card")
	}
	plan, err := registry.NewPlan(entry, registry.DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	// The card wraps a long prefix path, so the check is on the words that
	// carry the consent rather than on whole lines: what it is, what runs,
	// and the three arguments that are the install's security posture.
	view := ansi.Strip(got.View())
	if !strings.Contains(plan.Describe(), "--ignore-scripts @x/auggie@0.36.0") {
		t.Fatalf("Describe() no longer spells the pinned spec: %q", plan.Describe())
	}
	for _, want := range []string{"Install into", "Auggie CLI — Auggie CLI does agent things (0.36.0, npm)",
		"opentree will run:", "npm install -g --prefix", "--ignore-scripts @x/auggie@0.36.0", "y install"} {
		if !strings.Contains(view, want) {
			t.Errorf("the card lacks %q:\n%s", want, view)
		}
	}

	got, cmd := got.updateAgents(keyMsg("y"))
	if !got.agentsTab.busy || cmd == nil || got.agentsTab.confirm != nil {
		t.Error("y did not start the install")
	}
}

func TestAgentsBrowser_EnterRefusesWhatAddRefuses(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	m := agentsModel(t)
	m.agentsTab.browsing = true
	m.agentsTab.index = []registry.Entry{
		uvxEntry("pyagent"),
		npmEntry("devin", "Devin", "2.0.0"),
		npmEntry("gemini", "Gemini CLI", "1.0.0"),
		npmEntry("claude-clone", "Claude Code", "1.0.0"),
	}
	wants := []string{
		"distributed via uvx",
		"devin 1.0.0 is already installed — u refreshes it",
		`"gemini" is built into opentree — i manages its adapter`,
		`is named "Claude Code", which opentree's built-in`,
	}
	for i, want := range wants {
		m.agentsTab.browseCursor = i
		got, _ := m.updateAgents(keyMsg("enter"))
		if got.agentsTab.confirm != nil {
			t.Errorf("entry %d opened a consent card", i)
		}
		if got.err == nil || !strings.Contains(got.err.Error(), want) {
			t.Errorf("entry %d: err = %v, want %q", i, got.err, want)
		}
	}
}

// The outcome reloads the runtime list, so the new agent is a row — and in
// the A picker, and the next chat — without a restart.
func TestAgentsInstalled_ReloadsTheList(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	before := len(m.agentsTab.rows)
	m.agentsTab.busy = true

	fabricateInstall(t, home, "devin", "Devin", "3000.6.7")
	got, cmd := applyUpdate(m, registryRanMsg{id: "devin", version: "3000.6.7"})
	if got.agentsTab.busy || cmd == nil {
		t.Error("the outcome did not clear busy or raise a notice")
	}
	if len(got.agentsTab.rows) != before+1 {
		t.Errorf("rows = %d after the install, want %d", len(got.agentsTab.rows), before+1)
	}
	if config.FindAgent("devin") == nil {
		t.Error("the picker's list does not know the new agent")
	}
	if !strings.Contains(got.notice, "devin 3000.6.7 installed — enter uses it") {
		t.Errorf("notice = %q", got.notice)
	}
}

// A failure keeps what the install printed, in the log rather than the
// toast: the toast holds one line and npm's reason is rarely one line.
func TestAgentsInstalled_FailureGoesToTheLog(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	got, _ := applyUpdate(m, registryRanMsg{id: "auggie", update: true, from: "0.35.0",
		output: "npm ERR! code E404\nnpm ERR! 404 Not Found", err: stringError("npm install failed")})
	if got.err == nil || !strings.Contains(got.err.Error(), "the installed 0.35.0 is untouched") {
		t.Errorf("err = %v, want the CLI's untouched sentence", got.err)
	}
	if !strings.Contains(strings.Join(got.errLog, "\n"), "E404") {
		t.Errorf("the install's output did not reach the log: %v", got.errLog)
	}
}

// ---------------------------------------------------------------------------
// update, remove
// ---------------------------------------------------------------------------

func TestAgentsUpdate_URefusesABuiltIn(t *testing.T) {
	emptyHome(t)
	snapshotAgentList(t)
	m := agentsModel(t)
	cursorOn(t, &m, "opencode")
	got, _ := m.updateAgents(keyMsg("u"))
	if got.err == nil || !strings.Contains(got.err.Error(), "built into opentree — i manages its adapter") {
		t.Errorf("u on a built-in: %v", got.err)
	}
}

func TestAgentsUpdate_OneInstallUpToDateOrOutdated(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	m := agentsModel(t)
	cursorOn(t, &m, "devin")
	got, cmd := m.updateAgents(keyMsg("u"))
	if !got.agentsTab.busy || cmd == nil {
		t.Fatal("u did not fetch the index")
	}

	same := registry.Index{Agents: []registry.Entry{npmEntry("devin", "Devin", "1.0.0")}}
	after, _ := applyUpdate(got, registryIndexMsg{index: same, purpose: indexUpdateOne, id: "devin"})
	if after.agentsTab.confirm != nil || !strings.Contains(after.notice, "devin — up to date") {
		t.Errorf("up to date: confirm=%v notice=%q", after.agentsTab.confirm, after.notice)
	}

	newer := registry.Index{Agents: []registry.Entry{npmEntry("devin", "Devin", "1.1.0")}}
	after, _ = applyUpdate(got, registryIndexMsg{index: newer, purpose: indexUpdateOne, id: "devin"})
	if after.agentsTab.confirm == nil || !after.agentsTab.confirm.update {
		t.Fatal("a newer version did not open the update card")
	}
	view := ansi.Strip(after.View())
	for _, want := range []string{"Update devin?", "devin 1.0.0 → 1.1.0", "opentree will run:"} {
		if !strings.Contains(view, want) {
			t.Errorf("update card is missing %q:\n%s", want, view)
		}
	}

	gone := registry.Index{Agents: nil}
	after, _ = applyUpdate(got, registryIndexMsg{index: gone, purpose: indexUpdateOne, id: "devin"})
	if after.err == nil || !strings.Contains(after.err.Error(), "no longer in the registry — it stays installed") {
		t.Errorf("unlisted: %v", after.err)
	}
}

// U asks about every outdated install in turn — one card each, n skips
// one, esc abandons the rest — the way `agents update` confirms each
// separately so one agent's new version never smuggles in another's.
func TestAgentsUpdate_UWalksTheQueue(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "auggie", "Auggie CLI", "0.1.0")
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	fabricateInstall(t, home, "goose", "goose", "2.0.0")
	m := agentsModel(t)
	got, _ := m.updateAgents(keyMsg("U"))
	idx := registry.Index{Agents: []registry.Entry{
		npmEntry("auggie", "Auggie CLI", "0.2.0"),
		npmEntry("devin", "Devin", "1.1.0"),
		npmEntry("goose", "goose", "2.0.0"),
	}}
	got, _ = applyUpdate(got, registryIndexMsg{index: idx, purpose: indexUpdateAll})
	if got.agentsTab.confirm == nil || got.agentsTab.confirm.plan.Entry.ID != "auggie" || len(got.agentsTab.queue) != 1 {
		t.Fatalf("U did not queue the two outdated installs: confirm=%v queue=%d", got.agentsTab.confirm, len(got.agentsTab.queue))
	}
	if !strings.Contains(ansi.Strip(got.View()), "skip (1 more)") {
		t.Error("the card does not say more are queued")
	}
	got, _ = got.updateAgents(keyMsg("n"))
	if got.agentsTab.confirm == nil || got.agentsTab.confirm.plan.Entry.ID != "devin" {
		t.Fatal("n did not move on to the next queued update")
	}
	got, _ = got.updateAgents(keyMsg("esc"))
	if got.agentsTab.confirm != nil || len(got.agentsTab.queue) != 0 {
		t.Error("esc did not abandon the queue")
	}
}

func TestAgentsRemove_RefusesBuiltInsConfirmsInstalls(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	m := agentsModel(t)

	cursorOn(t, &m, "claude")
	got, _ := m.updateAgents(keyMsg("x"))
	if got.agentsTab.removing != nil || got.err == nil || !strings.Contains(got.err.Error(), "built into opentree and cannot be removed") {
		t.Errorf("x on a built-in: removing=%v err=%v", got.agentsTab.removing, got.err)
	}

	cursorOn(t, &m, "devin")
	got, _ = m.updateAgents(keyMsg("x"))
	if got.agentsTab.removing == nil {
		t.Fatal("x did not ask")
	}
	if !strings.Contains(ansi.Strip(got.View()), `Remove agent "devin"?`) {
		t.Error("the card does not name the agent")
	}
	got, _ = got.updateAgents(keyMsg("n"))
	if got.agentsTab.removing != nil {
		t.Error("n did not cancel")
	}
}

// After a removal the list is reloaded, and a config still naming the
// agent is said out loud rather than discovered at the next `opentree new`.
func TestAgentsRemoved_ReloadsAndWarnsAboutConfig(t *testing.T) {
	home := emptyHome(t)
	snapshotAgentList(t)
	fabricateInstall(t, home, "devin", "Devin", "1.0.0")
	m := agentsModel(t)
	m.cfg.Agent.Command = "devin"
	m.agentsTab.busy = true
	if err := registry.Remove("devin"); err != nil {
		t.Fatal(err)
	}
	got, _ := applyUpdate(m, registryRemovedMsg{id: "devin"})
	if got.agentsTab.busy {
		t.Error("busy not cleared")
	}
	if config.FindAgent("devin") != nil {
		t.Error("the removed agent still resolves")
	}
	if !strings.Contains(got.notice, "removed devin") || !strings.Contains(got.notice, "your config still names it") {
		t.Errorf("notice = %q", got.notice)
	}
}
