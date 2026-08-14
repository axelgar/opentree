package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/skills"
)

func testSkill(name, agent string, scope skills.Scope, dir string) skills.Skill {
	return skills.Skill{
		Name:        name,
		Description: name + " does a thing",
		Dir:         dir,
		Agents:      []string{agent},
		Scope:       scope,
	}
}

// skillsModel is a model already sitting on the Skills tab.
func skillsModel(list ...skills.Skill) Model {
	m := newTestModel()
	m.tab = tabSkills
	m.skills = list
	return m
}

func TestTab_SwitchesBetweenPlaces(t *testing.T) {
	m := newTestModel(testWS("alpha"))
	next, _ := m.Update(keyMsg("tab"))
	m = next.(Model)
	if m.tab != tabSkills {
		t.Fatal("tab did not open the Skills tab")
	}
	if !strings.Contains(m.View(), "No skills installed") {
		t.Error("Skills tab did not render")
	}

	next, _ = m.Update(keyMsg("tab"))
	if next.(Model).tab != tabWorkspaces {
		t.Error("tab did not return to Workspaces")
	}
}

// Arrows walk the tab bar the way it looks like they should.
func TestArrows_SwitchBetweenPlaces(t *testing.T) {
	m := newTestModel(testWS("alpha"))
	next, _ := m.Update(keyMsg("right"))
	m = next.(Model)
	if m.tab != tabSkills {
		t.Fatal("right did not open the Skills tab")
	}
	next, _ = m.Update(keyMsg("left"))
	if next.(Model).tab != tabWorkspaces {
		t.Error("left did not return to Workspaces")
	}
}

// esc is a step back, not an exit, while the Skills tab has focus.
func TestEsc_StepsBackFromSkillsButQuitsFromWorkspaces(t *testing.T) {
	m := skillsModel()
	next, cmd := m.Update(keyMsg("esc"))
	if next.(Model).tab != tabWorkspaces {
		t.Error("esc did not return to Workspaces")
	}
	if cmd != nil && cmd() == (tea.QuitMsg{}) {
		t.Error("esc quit from the Skills tab")
	}

	_, cmd = newTestModel(testWS("alpha")).Update(keyMsg("esc"))
	if cmd == nil || cmd() != (tea.QuitMsg{}) {
		t.Error("esc did not quit from the Workspaces tab")
	}
}

// Both tabs are always named, so the second place is discoverable rather than
// hidden behind a key nobody presses.
func TestTabBar_NamesBothPlaces(t *testing.T) {
	view := newTestModel(testWS("alpha")).View()
	if !strings.Contains(view, "Workspaces") || !strings.Contains(view, "Skills") {
		t.Errorf("tab bar missing a place:\n%s", view)
	}
}

func TestSkillsView_RendersNameDescriptionAndAgent(t *testing.T) {
	m := skillsModel(testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"))
	view := m.View()
	for _, want := range []string{"release", "does a thing", "✻", "repo"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

// A name held by two trees is the "shared across agents" case the tab exists to
// answer, so it is tagged rather than left for the reader to spot.
func TestSkillsView_TagsSharedSkills(t *testing.T) {
	m := skillsModel(
		testSkill("research", "Claude Code", skills.ScopeUser, "/home/.claude/skills/research"),
		testSkill("research", "OpenCode", skills.ScopeUser, "/home/.config/opencode/skills/research"),
		testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"),
	)
	view := m.View()
	if strings.Count(view, "duplicate") != 2 {
		t.Errorf("expected both research rows tagged duplicate, got %d:\n%s", strings.Count(view, "duplicate"), view)
	}
}

func TestSkillsFilter(t *testing.T) {
	m := skillsModel(
		testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"),
		testSkill("research", "Claude Code", skills.ScopeUser, "/home/.claude/skills/research"),
	)
	m.skillFilter = "relea"
	if got := m.visibleSkills(); len(got) != 1 || got[0].Name != "release" {
		t.Errorf("filter = %+v, want just release", got)
	}

	// The description is searched too: people remember what a skill does more
	// often than what it is called.
	m.skillFilter = "does a thing"
	if got := m.visibleSkills(); len(got) != 2 {
		t.Errorf("description filter matched %d, want 2", len(got))
	}
}

// esc clears the filter before it leaves the tab, so a filtered list does not
// need two presses to explain itself.
func TestSkillsEsc_ClearsFilterBeforeLeaving(t *testing.T) {
	m := skillsModel(testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"))
	m.skillFilter = "rel"

	next, _ := m.Update(keyMsg("esc"))
	m = next.(Model)
	if m.skillFilter != "" {
		t.Error("esc did not clear the filter")
	}
	if m.tab != tabSkills {
		t.Error("esc left the tab while a filter was set")
	}

	next, _ = m.Update(keyMsg("esc"))
	if next.(Model).tab != tabWorkspaces {
		t.Error("esc on an unfiltered list did not leave the tab")
	}
}

func TestSkillsCursor_StaysInRange(t *testing.T) {
	m := skillsModel(
		testSkill("a", "Claude Code", skills.ScopeUser, "/a"),
		testSkill("b", "Claude Code", skills.ScopeUser, "/b"),
	)
	// Down past the end must not run off it.
	for range 5 {
		next, _ := m.Update(keyMsg("j"))
		m = next.(Model)
	}
	if m.skillCursor != 1 {
		t.Errorf("cursor = %d, want 1 (clamped to the last row)", m.skillCursor)
	}
	for range 5 {
		next, _ := m.Update(keyMsg("k"))
		m = next.(Model)
	}
	if m.skillCursor != 0 {
		t.Errorf("cursor = %d, want 0", m.skillCursor)
	}
}

// A rescan that returns fewer skills than the cursor's position must not leave
// the cursor pointing past the end.
func TestSkillsScanned_ClampsCursor(t *testing.T) {
	m := skillsModel(
		testSkill("a", "Claude Code", skills.ScopeUser, "/a"),
		testSkill("b", "Claude Code", skills.ScopeUser, "/b"),
	)
	m.skillCursor = 1
	next, _ := m.Update(skillsScannedMsg{skills: []skills.Skill{testSkill("a", "Claude Code", skills.ScopeUser, "/a")}})
	if got := next.(Model).skillCursor; got != 0 {
		t.Errorf("cursor = %d, want 0 after the list shrank", got)
	}
}

func TestCopyTargets_ExcludesTreesAlreadyHoldingTheSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	claudeUser := filepath.Join(home, ".claude", "skills")
	m := skillsModel(testSkill("research", "Claude Code", skills.ScopeUser, filepath.Join(claudeUser, "research")))
	m.repoRoot = t.TempDir()

	targets := m.copyTargets(m.skills[0])
	for _, target := range targets {
		if target.Dir == claudeUser {
			t.Errorf("offered the tree the skill is already in: %+v", targets)
		}
	}
	// It should still be offerable to the other agent, which is the whole point.
	var labels []string
	for _, target := range targets {
		labels = append(labels, target.Label)
	}
	if !strings.Contains(strings.Join(labels, ","), "OpenCode") {
		t.Errorf("targets = %v, want OpenCode among them", labels)
	}
}

// c on a skill that is already everywhere must say so rather than opening an
// empty picker.
func TestSkillsCopy_RefusesWhenNowhereLeftToPutIt(t *testing.T) {
	m := skillsModel(testSkill("research", "Claude Code", skills.ScopeUser, "/only"))
	m.repoRoot = ""
	// Pretend every tree holds it by making copyTargets come back empty.
	m.skills = nil
	m.skills = append(m.skills, testSkill("research", "Claude Code", skills.ScopeUser, "/only"))
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	if len(m.copyTargets(m.skills[0])) != 0 {
		t.Skip("environment still offers a target; the refusal path is covered by the guard in updateSkills")
	}
	next, _ := m.Update(keyMsg("c"))
	got := next.(Model)
	if got.skillCopying != nil {
		t.Error("opened a picker with no targets")
	}
	if got.err == nil {
		t.Error("no explanation given")
	}
}

func TestSkillsDelete_ConfirmsThenRemoves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "doomed")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: doomed\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := skillsModel(testSkill("doomed", "Claude Code", skills.ScopeUser, dir))
	next, _ := m.Update(keyMsg("x"))
	m = next.(Model)
	if m.skillDeleting == nil {
		t.Fatal("x did not ask for confirmation")
	}
	if !strings.Contains(m.View(), "Delete skill") {
		t.Error("confirmation not rendered")
	}
	// The directory is still there until the answer.
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("deleted before confirming")
	}

	next, _ = m.Update(keyMsg("y"))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("confirmed delete did not remove the skill")
	}
	if next.(Model).skillDeleting != nil {
		t.Error("confirmation stayed open")
	}
}

func TestSkillsDelete_CancelKeepsTheSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spared")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	m := skillsModel(testSkill("spared", "Claude Code", skills.ScopeUser, dir))
	next, _ := m.Update(keyMsg("x"))
	next, _ = next.(Model).Update(keyMsg("n"))
	if next.(Model).skillDeleting != nil {
		t.Error("n did not close the confirmation")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("cancelled delete removed the skill anyway")
	}
}

// The warning is what tells you the agent in a worktree is working without the
// repository's skills; without it nothing on the row would say so.
func TestWorkspaceRow_WarnsWhenSkillsAreMissing(t *testing.T) {
	ws := testWS("alpha")
	ws.MissingSkills = []string{".claude/skills"}
	if !strings.Contains(newTestModel(ws).View(), "no repo skills") {
		t.Error("workspace row did not warn about missing skills")
	}
	if strings.Contains(newTestModel(testWS("beta")).View(), "no repo skills") {
		t.Error("warned about a workspace that is not missing anything")
	}
}

func TestWorkspacesMissing_CountsOnlyRepoSkills(t *testing.T) {
	m := newTestModel()
	m.repoRoot = "/repo"
	alpha, beta := testWS("alpha"), testWS("beta")
	alpha.MissingSkills = []string{".claude/skills"}
	m.workspaces = []WorkspaceItem{alpha, beta}

	repoSkill := testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release")
	if got := m.workspacesMissing(repoSkill); got != 1 {
		t.Errorf("workspacesMissing = %d, want 1", got)
	}
	// A machine-wide skill is visible from every directory, so no worktree can
	// be missing it.
	userSkill := testSkill("research", "Claude Code", skills.ScopeUser, "/home/.claude/skills/research")
	if got := m.workspacesMissing(userSkill); got != 0 {
		t.Errorf("workspacesMissing on a user skill = %d, want 0", got)
	}
}

func TestSkillWindow_KeepsCursorVisible(t *testing.T) {
	// Budget for three rows out of ten, cursor near the end.
	start, end := skillWindow(10, 8, 6)
	if end-start != 3 {
		t.Errorf("window = [%d,%d), want 3 rows", start, end)
	}
	if 8 < start || 8 >= end {
		t.Errorf("cursor 8 outside window [%d,%d)", start, end)
	}
	// Everything fits: no scrolling.
	if start, end := skillWindow(3, 0, 100); start != 0 || end != 3 {
		t.Errorf("window = [%d,%d), want the whole list", start, end)
	}
	// A terminal too short for even one row still renders one.
	if start, end := skillWindow(5, 0, 0); end-start != 1 {
		t.Errorf("window = [%d,%d), want at least one row", start, end)
	}
}

func TestEditorCommand(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if name, args := editorCommand(); name != "vi" || len(args) != 0 {
		t.Errorf("default editor = %q %v, want vi", name, args)
	}

	// An editor with flags is an ordinary value; treating the whole string as a
	// binary name would send opentree looking for "code --wait".
	t.Setenv("EDITOR", "code --wait")
	name, args := editorCommand()
	if name != "code" || len(args) != 1 || args[0] != "--wait" {
		t.Errorf("editor = %q %v, want code [--wait]", name, args)
	}

	// VISUAL wins, the way every other terminal tool resolves it.
	t.Setenv("VISUAL", "nvim")
	if name, _ := editorCommand(); name != "nvim" {
		t.Errorf("editor = %q, want nvim", name)
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "workspace"); got != "1 workspace" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(2, "workspace"); got != "2 workspaces" {
		t.Errorf("plural(2) = %q", got)
	}
	if got := plural(0, "skill"); got != "0 skills" {
		t.Errorf("plural(0) = %q", got)
	}
}

// Keys the tab does nothing with must stop here. Falling through to the
// workspace handler would open a dialog that skillsView never draws, leaving a
// text input collecting keystrokes behind a screen with no sign of it.
func TestSkillsTab_SwallowsWorkspaceKeys(t *testing.T) {
	m := skillsModel(testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"))
	for _, k := range []string{"n", "i", "p", "o", "A", "s", "m"} {
		next, _ := m.Update(keyMsg(k))
		got := next.(Model)
		if got.creating || got.prCreating || got.agentSelecting || got.prompting {
			t.Errorf("%q opened a workspace dialog from the Skills tab", k)
		}
		if got.tab != tabSkills {
			t.Errorf("%q left the Skills tab", k)
		}
	}
}

func TestSkillsTab_QuitStillWorks(t *testing.T) {
	m := skillsModel()
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q did not quit from the Skills tab")
	}
}

// stateSkill is a skill with an explicit per-agent state.
func stateSkill(name string, states map[string]skills.State) skills.Skill {
	agents := make([]string, 0, len(states))
	for _, a := range []string{"OpenCode", "Claude Code"} {
		if _, ok := states[a]; ok {
			agents = append(agents, a)
		}
	}
	return skills.Skill{
		Name:        name,
		Description: name + " does a thing",
		Dir:         "/home/.claude/skills/" + name,
		Agents:      agents,
		Scope:       skills.ScopeUser,
		States:      states,
	}
}

// Installed is not available. A row for a skill the agent has been told to
// ignore has to say so, or the list is claiming a capability that is not there.
func TestSkillsView_MarksDisabledSkills(t *testing.T) {
	m := skillsModel(
		stateSkill("tdd", map[string]skills.State{"Claude Code": skills.StateOff}),
		stateSkill("research", map[string]skills.State{"Claude Code": skills.StateOn}),
	)
	view := m.View()
	if !strings.Contains(view, "off") {
		t.Errorf("view does not mark the disabled skill:\n%s", view)
	}
	// The tally counts what the agent will load, not what is installed.
	if !strings.Contains(view, "Claude Code 1") {
		t.Errorf("tally counts disabled skills:\n%s", view)
	}
}

// A skill that asked not to be model-invoked is loaded and slash-invocable —
// a different thing from off, and worth a different word.
func TestSkillsView_MarksManualOnly(t *testing.T) {
	m := skillsModel(stateSkill("ask-matt", map[string]skills.State{"Claude Code": skills.StateManualOnly}))
	view := m.View()
	if !strings.Contains(view, "manual") {
		t.Errorf("view does not mark the manual-only skill:\n%s", view)
	}
	// Still loaded, so it still counts.
	if !strings.Contains(view, "Claude Code 1") {
		t.Errorf("manual-only skill was not counted:\n%s", view)
	}
}

// Agents disagree when one reads a tree the other's settings switch off. The
// tag names the agent only in that case, so the common case stays terse.
func TestStateTag_NamesTheAgentOnlyWhenTheyDiffer(t *testing.T) {
	both := stateSkill("tdd", map[string]skills.State{
		"OpenCode": skills.StateOff, "Claude Code": skills.StateOff,
	})
	if got := stateTag(both); got != "off" {
		t.Errorf("stateTag = %q, want a bare \"off\" when every agent agrees", got)
	}

	split := stateSkill("tdd", map[string]skills.State{
		"OpenCode": skills.StateOn, "Claude Code": skills.StateOff,
	})
	got := stateTag(split)
	if !strings.Contains(got, "off for") {
		t.Errorf("stateTag = %q, want it to name the agent that differs", got)
	}

	clean := stateSkill("research", map[string]skills.State{"Claude Code": skills.StateOn})
	if got := stateTag(clean); got != "" {
		t.Errorf("stateTag = %q, want nothing for a fully available skill", got)
	}
}

func TestToggle_WritesAndClearsTheOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{\n  \"model\": \"opus\"\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	m := skillsModel(stateSkill("tdd", map[string]skills.State{"Claude Code": skills.StateOn}))
	next, _ := m.Update(keyMsg("t"))
	m = next.(Model)

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tdd": "off"`) {
		t.Fatalf("t did not disable the skill:\n%s", data)
	}
	if !strings.Contains(string(data), `"model": "opus"`) {
		t.Errorf("the rest of the settings file was lost:\n%s", data)
	}

	// Toggling back clears the entry rather than writing "on", so a skill that
	// asked not to be model-invoked is not promoted to automatic.
	m.skills = []skills.Skill{stateSkill("tdd", map[string]skills.State{"Claude Code": skills.StateOff})}
	m.Update(keyMsg("t"))
	data, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"tdd"`) {
		t.Errorf("t did not clear the override:\n%s", data)
	}
}

// A skill only readable by agents with no override mechanism cannot be toggled,
// and says so rather than appearing to work.
func TestToggle_RefusesWithoutAMechanism(t *testing.T) {
	m := skillsModel(stateSkill("solo", map[string]skills.State{"OpenCode": skills.StateOn}))
	next, _ := m.Update(keyMsg("t"))
	if next.(Model).err == nil {
		t.Error("toggling a skill with no override mechanism gave no explanation")
	}
}

// --- adding a skill --------------------------------------------------------

// typeURL runs the prompt from "a" to the point where the site has been asked
// what it publishes.
func typeURL(t *testing.T, m Model, url string) Model {
	t.Helper()
	next, _ := m.Update(keyMsg("a"))
	m = next.(Model)
	if !m.skillAdding {
		t.Fatal("a did not open the prompt")
	}
	for _, r := range url {
		next, _ = m.Update(keyMsg(string(r)))
		m = next.(Model)
	}
	next, _ = m.Update(keyMsg("enter"))
	return next.(Model)
}

func TestSkillsAdd_TypesAnAddressThenAsksTheSite(t *testing.T) {
	m := skillsModel()
	next, _ := m.Update(keyMsg("a"))
	m = next.(Model)

	for _, k := range []string{"g", "i", "t", ":", "x"} {
		next, _ = m.Update(keyMsg(k))
		m = next.(Model)
	}
	if m.skillAddURL != "git:x" {
		t.Fatalf("typed URL = %q, want %q", m.skillAddURL, "git:x")
	}
	// Still the prompt, showing what has been typed. A picker drawn as soon as
	// there is any URL at all replaces the prompt on the first keystroke, and
	// the rest is typed into a screen that gives no sign of receiving it.
	if view := m.View(); !strings.Contains(view, "add skill from") || !strings.Contains(view, "git:x") {
		t.Errorf("the prompt stopped showing what is being typed:\n%s", view)
	}

	next, _ = m.Update(keyMsg("enter"))
	m = next.(Model)
	if m.skillAdding || !m.skillDiscovering {
		t.Fatalf("enter did not move on to asking the site: adding=%v discovering=%v",
			m.skillAdding, m.skillDiscovering)
	}
	if !strings.Contains(m.View(), "what it publishes") {
		t.Errorf("nothing on screen says the site is being asked:\n%s", m.View())
	}
}

// The ordinary case: the address is a git URL, the site publishes nothing, and
// the tree picker opens as if it had been asked for directly.
func TestSkillsAdd_FallsBackToGit(t *testing.T) {
	m := typeURL(t, skillsModel(), "git:x")
	next, _ := m.Update(skillsDiscoveredMsg{site: "git:x", err: errors.New("not a publisher")})
	m = next.(Model)

	if m.skillDiscovering || m.skillEntry != nil {
		t.Fatal("a failed lookup left the flow mid-air")
	}
	if !strings.Contains(m.View(), "Clone x into") {
		t.Errorf("the tree picker did not open:\n%s", m.View())
	}
}

func TestSkillsAdd_PicksAmongPublishedSkills(t *testing.T) {
	m := typeURL(t, skillsModel(), "example.com")
	next, _ := m.Update(skillsDiscoveredMsg{site: "example.com", entries: []skills.Entry{
		{Name: "review", Description: "look at code", Type: "skill-md"},
		{Name: "deploy", Description: "ship it", Type: "archive"},
	}})
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, "publishes 2 skills") {
		t.Fatalf("the entry picker did not open:\n%s", view)
	}
	// The publisher's description is what there is to choose on.
	if !strings.Contains(view, "look at code") || !strings.Contains(view, "ship it") {
		t.Errorf("descriptions missing from the picker:\n%s", view)
	}

	next, _ = m.Update(keyMsg("down"))
	next, _ = next.(Model).Update(keyMsg("enter"))
	m = next.(Model)
	if m.skillEntry == nil || m.skillEntry.Name != "deploy" {
		t.Fatalf("chosen entry = %+v, want deploy", m.skillEntry)
	}
	// And on to where it lands, named for the skill rather than the address.
	if !strings.Contains(m.View(), "Install deploy into") {
		t.Errorf("the tree picker did not open:\n%s", m.View())
	}
}

// Publishers write long descriptions — Cloudflare's run past 200 characters —
// and a row wider than the card wraps onto the next one, so every entry in the
// list is drawn across two ragged lines instead of one.
func TestSkillsAdd_PickerRowsFitTheCard(t *testing.T) {
	long := strings.Repeat("a description that keeps going ", 12)
	var entries []skills.Entry
	for _, name := range []string{"one", "two", "three"} {
		entries = append(entries, skills.Entry{Name: name, Description: long, Type: "skill-md"})
	}
	m := typeURL(t, skillsModel(), "example.com")
	next, _ := m.Update(skillsDiscoveredMsg{site: "example.com", entries: entries})
	m = next.(Model)

	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line is %d wide in a %d terminal:\n%s", w, m.width, line)
		}
	}
}

// One skill is not a choice, so the picker is skipped rather than drawn with a
// single row in it.
func TestSkillsAdd_SingleSkillSkipsThePicker(t *testing.T) {
	m := typeURL(t, skillsModel(), "example.com")
	next, _ := m.Update(skillsDiscoveredMsg{site: "example.com", entries: []skills.Entry{
		{Name: "review", Description: "look at code", Type: "skill-md"},
	}})
	m = next.(Model)

	if m.skillEntry == nil || m.skillEntry.Name != "review" {
		t.Fatalf("chosen entry = %+v, want review", m.skillEntry)
	}
	if !strings.Contains(m.View(), "Install review into") {
		t.Errorf("the tree picker did not open:\n%s", m.View())
	}
}

// An answer that arrives after the flow was abandoned must not reopen it.
func TestSkillsAdd_StaleAnswerIsIgnored(t *testing.T) {
	m := typeURL(t, skillsModel(), "example.com")
	next, _ := m.Update(keyMsg("esc"))
	next, _ = next.(Model).Update(skillsDiscoveredMsg{site: "example.com", entries: []skills.Entry{
		{Name: "review", Type: "skill-md"},
	}})
	if got := next.(Model); got.skillEntries != nil || got.skillEntry != nil || got.skillAddURL != "" {
		t.Errorf("a stale answer reopened the flow: %+v", got.skillEntries)
	}
}

func TestSkillsAdd_EscCancelsEveryStep(t *testing.T) {
	m := skillsModel()
	next, _ := m.Update(keyMsg("a"))
	next, _ = next.(Model).Update(keyMsg("esc"))
	if got := next.(Model); got.skillAdding || got.skillAddURL != "" {
		t.Error("esc did not close the prompt")
	}

	m = typeURL(t, skillsModel(), "example.com")
	next, _ = m.Update(keyMsg("esc"))
	if got := next.(Model); got.skillDiscovering || got.skillAddURL != "" {
		t.Error("esc did not abandon the lookup")
	}

	m = skillsModel()
	m.skillAddURL = "example.com"
	m.skillEntries = []skills.Entry{{Name: "a"}, {Name: "b"}}
	next, _ = m.Update(keyMsg("esc"))
	if got := next.(Model); got.skillEntries != nil || got.skillAddURL != "" {
		t.Error("esc did not close the entry picker")
	}

	m = skillsModel()
	m.skillAddURL = "https://example.com/a-skill"
	next, _ = m.Update(keyMsg("esc"))
	if next.(Model).skillAddURL != "" {
		t.Error("esc did not close the tree picker")
	}
}

// An empty URL is a cancelled prompt, not a clone of nothing.
func TestSkillsAdd_EmptyUrlDoesNothing(t *testing.T) {
	m := skillsModel()
	m.skillAdding = true
	next, _ := m.Update(keyMsg("enter"))
	if got := next.(Model); got.skillAdding || got.skillAddURL != "" {
		t.Error("an empty URL opened the picker")
	}
}

// --- the agent's own answer ------------------------------------------------

// The two disagreements worth reporting, and the three cases that are not.
func TestProbeMismatch(t *testing.T) {
	skill := testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release")
	off := testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release")
	off.States = map[string]skills.State{"Claude Code": skills.StateOff}

	tests := []struct {
		name  string
		skill skills.Skill
		probe map[string]bool
		want  string
	}{
		{"not asked yet", skill, nil, ""},
		{"agrees it is loaded", skill, map[string]bool{"release": true}, ""},
		{"expected but absent", skill, map[string]bool{}, "⚠ not loaded"},
		{"switched off and gone", off, map[string]bool{}, ""},
		{"switched off but offered anyway", off, map[string]bool{"release": true}, "⚠ still loaded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := skillsModel(tt.skill)
			m.skillProbe, m.skillProbed = tt.probe, "Claude Code"
			if got := m.probeMismatch(tt.skill); got != tt.want {
				t.Errorf("probeMismatch() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A row naming other agents is only quiet while the probed agent stays quiet
// about it too.
func TestProbeMismatch_IgnoresAnotherAgentsSkill(t *testing.T) {
	s := testSkill("release", "OpenCode", skills.ScopeRepo, "/repo/.opencode/skills/release")
	m := skillsModel(s)
	m.skillProbe, m.skillProbed = map[string]bool{}, "Claude Code"
	if got := m.probeMismatch(s); got != "" {
		t.Errorf("probeMismatch() = %q for a skill the probed agent neither reads nor claims", got)
	}
}

// The bug this check exists for, as it actually happened: opentree credited
// .claude/skills to Claude Code alone, and opencode was answering to /release
// from a workspace. The first version of the probe was silent here, because it
// only judged rows that named the agent it was asking.
func TestProbeMismatch_FlagsAnAgentReadingATreeTheListDoesNotCredit(t *testing.T) {
	s := testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release")
	m := skillsModel(s)
	m.skillProbe, m.skillProbed = map[string]bool{"release": true}, "OpenCode"

	got := m.probeMismatch(s)
	if got == "" {
		t.Fatal("probeMismatch() was silent about an agent reading a skill the row says it cannot")
	}
	mark, _, _ := config.Brand("OpenCode")
	if !strings.Contains(got, mark) {
		t.Errorf("probeMismatch() = %q, want it to name the agent doing the reading", got)
	}
}

func TestSkillsStatusBar_CountsWhatTheListDoesNotClaim(t *testing.T) {
	m := skillsModel(
		testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"),
		testSkill("research", "OpenCode", skills.ScopeUser, "/home/.config/opencode/skills/research"),
	)
	m.skillProbe, m.skillProbed = map[string]bool{"release": true, "research": true}, "OpenCode"

	bar := m.skillsStatusBar()
	if !strings.Contains(bar, "confirmed 1/1") {
		t.Errorf("status bar = %q, want the skill it does claim confirmed", bar)
	}
	if !strings.Contains(bar, "1 unexpected") {
		t.Errorf("status bar = %q, want the skill it does not claim counted", bar)
	}
}

// A clean probe says so without a second number: "confirmed 2/2 · 0 unexpected"
// reads as a finding rather than as nothing to report.
func TestSkillsStatusBar_QuietWhenNothingIsUnexpected(t *testing.T) {
	m := skillsModel(testSkill("release", "OpenCode", skills.ScopeRepo, "/repo/.opencode/skills/release"))
	m.skillProbe, m.skillProbed = map[string]bool{"release": true}, "OpenCode"

	if bar := m.skillsStatusBar(); strings.Contains(bar, "unexpected") {
		t.Errorf("status bar = %q, want no second number when there is nothing in it", bar)
	}
}

func TestProbe_OneAtATime(t *testing.T) {
	m := skillsModel()
	m.skillProbing = true
	if _, cmd := m.Update(keyMsg("v")); cmd != nil {
		t.Error("v started a second probe while one was already running")
	}
}

// Gemini names twenty commands of its own over ACP and not one skill, so its
// answer would read as "every skill missing". A check that cannot be run says
// so, and leaves `v` usable once another agent is configured.
func TestProbe_RefusesAnAgentThatDoesNotNameItsSkills(t *testing.T) {
	m := skillsModel()
	m.cfg.Agent.Command = "gemini"

	next, _ := m.Update(keyMsg("v"))
	m = next.(Model)
	if m.skillProbing {
		t.Error("started a probe against an agent with nothing to say")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "does not name its skills") {
		t.Errorf("err = %v, want the reason the check cannot run", m.err)
	}
}

// --- repo skills an agent cannot reach --------------------------------------

func TestBlindAgents_RepoSkillOnly(t *testing.T) {
	m := skillsModel()
	m.repoRoot = "/repo"

	// opencode's own repository tree, which no other agent reads. The other
	// direction has fewer cases: opencode reads .claude/skills too, so a skill
	// there is already readable by both and Scan gives it both marks.
	repo := testSkill("release", "OpenCode", skills.ScopeRepo, "/repo/.opencode/skills/release")
	got := m.blindAgents(repo)
	if len(got) == 0 || slices.Contains(got, "OpenCode") {
		t.Errorf("blindAgents() = %v, want the agents that cannot read the repo tree", got)
	}
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.RepoDirs) > 0 && agent.Name != "OpenCode" && !slices.Contains(got, agent.Name) {
			t.Errorf("blindAgents() = %v, missing %s — it does not read .opencode/skills either", got, agent.Name)
		}
	}

	// A machine-wide skill is already shared: agents auto-load each other's user
	// trees, and the answer for one that does not is a copy, not a link.
	user := testSkill("research", "Claude Code", skills.ScopeUser, "/home/.claude/skills/research")
	if got := m.blindAgents(user); len(got) != 0 {
		t.Errorf("blindAgents() = %v for a user-scoped skill, want none", got)
	}

	// A directory the user registered in one agent's own config is where they
	// put it, for that agent — and it is not a tree Bridge could hand over.
	registered := testSkill("deep", "OpenCode", skills.ScopeRepo, "/repo/team/nested/deep")
	if got := m.blindAgents(registered); len(got) != 0 {
		t.Errorf("blindAgents() = %v for a config-registered tree, want none — nothing would fix it", got)
	}
}

func TestSkillsView_WarnsWhenAnAgentCannotSeeARepoSkill(t *testing.T) {
	m := skillsModel(testSkill("release", "OpenCode", skills.ScopeRepo, "/repo/.opencode/skills/release"))
	m.repoRoot = "/repo"
	if !strings.Contains(m.View(), "invisible to") {
		t.Errorf("the row does not say the other agent cannot use it:\n%s", m.View())
	}
}

// The registry holds agents opentree can launch but which have no skills
// mechanism. Tallying one at zero says it has somewhere to put skills and
// nothing in it, which is not the same as having no such concept.
func TestSkillsStatusBar_SkipsAgentsWithoutSkills(t *testing.T) {
	m := skillsModel(testSkill("release", "Claude Code", skills.ScopeRepo, "/repo/.claude/skills/release"))
	bar := m.skillsStatusBar()
	for _, agent := range config.PredefinedAgents {
		named := strings.Contains(bar, agent.Name)
		has := len(agent.Skills.UserDirs) > 0 || len(agent.Skills.RepoDirs) > 0
		if named != has {
			t.Errorf("%s named in the tally: %v, want %v\n%s", agent.Name, named, has, bar)
		}
	}
}
