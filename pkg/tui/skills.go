package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/skills"
	"github.com/axelgar/opentree/pkg/ui"
)

// The Skills tab is a place rather than a dialog, which is why it is a tab and
// not another overlay. Everything else opentree opens over the list — a diff,
// the agent picker, a confirmation — is transient: you open it, act, and it
// closes. Skills are an inventory you visit, read, and prune.
//
// Rows are one per skill directory rather than one per skill name. Delete and
// copy have to mean exactly one directory, and an ambiguous destructive key is
// the wrong trade for a shorter list. Two same-named skills sort together and
// carry a "duplicate" tag, which is the case worth noticing: one is shadowing
// the other.
//
// "Which agents can use this" is a separate question from "where does it
// live", and the badges answer it — a tree is commonly read by every agent at
// once, so a skill carries a mark per reader rather than one owner.

// skillsTab is everything the Skills tab keeps on the Model, gathered into
// one field so the shared Model does not grow a loose field every time this
// tab does — the workspace list and the tab change for different reasons.
type skillsTab struct {
	list      []skills.Skill
	cursor    int
	filter    string
	filtering bool

	deleting *skills.Skill
	// deleteChoosing is the step before the confirmation, drawn only when the
	// skill is in more than one tree: which copies of it go.
	deleteChoosing bool
	copying        *skills.Skill

	// pickCursor is the cursor of whichever picker is up. The tree pickers
	// and the entry picker are mutually exclusive, so they share one cursor,
	// and each resets it as it opens.
	pickCursor int

	// chosen is the tree picker's ticked set, keyed by directory so it
	// survives the cursor moving. Empty means "the row under the cursor",
	// which is what one pick stays: ticking is for the second tree onwards.
	chosen map[string]bool

	// adding a skill, in up to four steps: adding while the address is being
	// typed, discovering while the site is asked what it publishes, a picker
	// over entries when it published more than one, and finally the same tree
	// picker a copy uses. A site that publishes nothing skips the middle two
	// and the address is cloned as a git URL instead.
	adding      bool
	addURL      string
	discovering bool
	entries     []skills.Entry
	entry       *skills.Entry

	// updating is one re-check in flight. A second one on the same row would
	// race the first over the directory it is swapping.
	updating bool

	// probe is what the agent itself says it loaded, against which the rest
	// of this tab is opentree's reading of the documentation. Nil until asked.
	probe   map[string]bool
	probed  string // the agent the answer came from
	probing bool
}

// skillTarget is a directory a picker acts on: the tree a skill is copied
// into, or the skill's own directory when the picker is removing it.
type skillTarget struct {
	Label string
	Dir   string
	// Agents are every agent that reads this tree, which is rarely just the one
	// it is named after: .claude/skills is read by three of them. Shown on the
	// row because picking two trees with the same reader is how you end up with
	// two copies of a skill and a "duplicate" tag explaining it afterwards.
	Agents []string
}

// scanSkillsCmd re-reads every skills tree from disk.
func (m Model) scanSkillsCmd() tea.Msg {
	return skillsScannedMsg{skills: skills.Scan(m.repoRoot)}
}

// sharedNames are the skill names held by more than one tree. Two SKILL.md
// files answering to one name means one is shadowing the other, which is worth
// saying out loud.
func sharedNames(list []skills.Skill) map[string]bool {
	count := make(map[string]int, len(list))
	for _, s := range list {
		count[s.Name]++
	}
	shared := make(map[string]bool)
	for name, n := range count {
		if n > 1 {
			shared[name] = true
		}
	}
	return shared
}

// visibleSkills applies the tab's filter, which matches name and description
// both: a user looking for "release" and one looking for "npm" are both
// looking for the same skill.
func (m Model) visibleSkills() []skills.Skill {
	if m.skillsTab.filter == "" {
		return m.skillsTab.list
	}
	q := strings.ToLower(m.skillsTab.filter)
	var out []skills.Skill
	for _, s := range m.skillsTab.list {
		if strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Description), q) {
			out = append(out, s)
		}
	}
	return out
}

// currentSkill is the skill under the cursor, or false when the list is empty.
func (m Model) currentSkill() (skills.Skill, bool) {
	visible := m.visibleSkills()
	if m.skillsTab.cursor < 0 || m.skillsTab.cursor >= len(visible) {
		return skills.Skill{}, false
	}
	return visible[m.skillsTab.cursor], true
}

// copyTargets are the trees a skill is not already in. A tree that already
// holds a skill of that name is left out rather than offered and then refused.
func (m Model) copyTargets(s skills.Skill) []skillTarget {
	occupied := make(map[string]bool)
	for _, existing := range m.skillsTab.list {
		if existing.Name == s.Name {
			occupied[filepath.Dir(existing.Dir)] = true
		}
	}

	// Who reads what is the trees' own answer rather than one reconstructed
	// here: a directory is read by whichever agents name it, and most of them
	// name more than their own.
	readers := map[string][]string{}
	for _, tree := range skills.Trees(m.repoRoot) {
		readers[tree.Dir] = tree.Agents
	}

	var out []skillTarget
	seen := map[string]bool{}
	// Only the canonical spelling of each tree is offered. The alternates exist
	// so an existing directory is found, not so opentree can invent one.
	add := func(agent config.PredefinedAgent, dir, scope string) {
		if dir == "" || occupied[dir] || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, skillTarget{
			Label:  agent.Name + " · " + scope,
			Dir:    dir,
			Agents: readers[dir],
		})
	}
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.UserDirs) > 0 {
			add(agent, skills.ExpandUserDir(agent.Skills.UserDirs[0]), "user")
		}
		if len(agent.Skills.RepoDirs) > 0 && m.repoRoot != "" {
			add(agent, filepath.Join(m.repoRoot, agent.Skills.RepoDirs[0]), "repo")
		}
	}
	return out
}

// deleteTargets are the copies of a skill this machine holds — one row per
// directory, because a skill in three trees is three directories and taking it
// out of one of them is a different act from taking it out of all three.
//
// The marks matter more here than anywhere else in the tab: a directory read
// by several agents cannot be taken away from one of them by deleting it, and
// the row is the only thing that says so before the confirmation does.
func (m Model) deleteTargets(s skills.Skill) []skillTarget {
	var out []skillTarget
	for _, existing := range m.skillsTab.list {
		if existing.Name != s.Name {
			continue
		}
		out = append(out, skillTarget{
			Label:  m.shortDir(filepath.Dir(existing.Dir)),
			Dir:    existing.Dir,
			Agents: existing.Agents,
		})
	}
	return out
}

// doomedSkills are the skills a delete confirmation will remove: the ticked
// copies, or the single row the picker was skipped for.
func (m Model) doomedSkills() []skills.Skill {
	if m.skillsTab.deleting == nil {
		return nil
	}
	if len(m.skillsTab.chosen) == 0 {
		return []skills.Skill{*m.skillsTab.deleting}
	}
	var out []skills.Skill
	for _, s := range m.skillsTab.list {
		if m.skillsTab.chosen[s.Dir] {
			out = append(out, s)
		}
	}
	return out
}

// shortDir names a directory the way the user thinks of it: relative to the
// repository when it is inside one, under ~ when it is in their home, and in
// full when it is neither.
func (m Model) shortDir(dir string) string {
	if m.repoRoot != "" {
		if rel, err := filepath.Rel(m.repoRoot, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return dir
}

// addName is what the pending skill will be called on disk: the name the
// publisher gave it, or the last element of the URL when git is about to clone
// it. Either way it is known before the fetch, which is what lets a tree that
// already holds one be left out of the picker rather than refused afterwards.
func (m Model) addName() string {
	if m.skillsTab.entry != nil {
		return m.skillsTab.entry.Name
	}
	return skills.CloneName(m.skillsTab.addURL)
}

// addTargets are the trees the pending skill could land in.
func (m Model) addTargets() []skillTarget {
	return m.copyTargets(skills.Skill{Name: m.addName()})
}

// cancelAdd clears every step of the add flow at once. Four pieces of state
// spread over two pickers and a request is three too many to unwind by hand at
// each of the places that can abandon it.
func (m Model) cancelAdd() Model {
	m.skillsTab.adding, m.skillsTab.addURL = false, ""
	m.skillsTab.discovering = false
	m.skillsTab.entries, m.skillsTab.entry = nil, nil
	m.skillsTab.chosen = nil
	return m
}

// chosenTargets are the trees a confirm applies to: everything ticked, or the
// row under the cursor when nothing is. Picking one tree is the common case
// and should not need a key to arm it first.
func (m Model) chosenTargets(targets []skillTarget) []skillTarget {
	var out []skillTarget
	for _, t := range targets {
		if m.skillsTab.chosen[t.Dir] {
			out = append(out, t)
		}
	}
	if len(out) > 0 {
		return out
	}
	if m.skillsTab.pickCursor < len(targets) {
		return targets[m.skillsTab.pickCursor : m.skillsTab.pickCursor+1]
	}
	return nil
}

// toggleTarget ticks or unticks the tree under the cursor.
func (m Model) toggleTarget(targets []skillTarget) Model {
	if m.skillsTab.pickCursor >= len(targets) {
		return m
	}
	if m.skillsTab.chosen == nil {
		m.skillsTab.chosen = map[string]bool{}
	}
	dir := targets[m.skillsTab.pickCursor].Dir
	if m.skillsTab.chosen[dir] {
		delete(m.skillsTab.chosen, dir)
		return m
	}
	m.skillsTab.chosen[dir] = true
	return m
}

// targetDirs is a picker's answer as the argument the skills package takes.
func targetDirs(targets []skillTarget) []string {
	dirs := make([]string, len(targets))
	for i, t := range targets {
		dirs[i] = t.Dir
	}
	return dirs
}

// targetsLabel names where a skill landed: the tree itself when there was one,
// a count when there were several — four labels in a toast is a line nobody
// reads.
func targetsLabel(targets []skillTarget) string {
	if len(targets) == 1 {
		return targets[0].Label
	}
	return plural(len(targets), "tree")
}

// pickTree moves the add flow on to choosing a destination, or abandons it
// when every tree already holds a skill by that name.
func (m Model) pickTree() (Model, tea.Cmd) {
	if len(m.addTargets()) > 0 {
		m.skillsTab.pickCursor, m.skillsTab.chosen = 0, map[string]bool{}
		return m, nil
	}
	name := m.addName()
	m = m.cancelAdd()
	return m, m.transientErrCmd(name + " is already in every tree")
}

// workspacesMissing counts the workspaces whose worktree cannot see a repo
// skill tree. Only meaningful for a repo-scoped skill: a machine-wide one is
// visible from every directory on the filesystem.
func (m Model) workspacesMissing(s skills.Skill) int {
	if s.Scope != skills.ScopeRepo {
		return 0
	}
	tree := filepath.Dir(s.Dir)
	n := 0
	for _, ws := range m.workspaces {
		for _, rel := range ws.MissingSkills {
			if filepath.Join(m.repoRoot, rel) == tree {
				n++
				break
			}
		}
	}
	return n
}

// editSkillCmd opens a skill's SKILL.md in the user's editor. Editing is what
// "update a skill" means — there is no version to pull and no registry to
// re-fetch from, just a markdown file the agent reads.
func editSkillCmd(s skills.Skill) tea.Cmd {
	name, args := editorCommand()
	args = append(args, filepath.Join(s.Dir, "SKILL.md"))
	// #nosec G702 -- the editor is the user's own $VISUAL/$EDITOR, the same
	// value git and every other terminal tool hands a file to. Anyone able to
	// set it already owns the shell opentree was started from.
	c := exec.Command(name, args...)
	// The editor takes over the terminal, so the skill list is rescanned on the
	// way back: the name and description in the frontmatter may be what changed.
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return skillEditedMsg{err: err}
	})
}

// editorCommand splits $VISUAL/$EDITOR into a binary and its leading arguments.
// "code --wait" and "nvim -u NONE" are ordinary values, and passing the whole
// string as the binary would send opentree looking for a file named after the
// flags.
func editorCommand() (string, []string) {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return "vi", nil
	}
	return fields[0], fields[1:]
}

// toggleSkill switches a skill off, or clears the override that switched it
// off. Only agents with a settings mechanism can be told anything; a skill
// readable only by agents without one has nothing to toggle.
//
// Turning it back on clears the override rather than writing "on", so a skill
// that asked not to be model-invoked returns to that rather than to fully
// automatic.
func (m Model) toggleSkill(s skills.Skill) (Model, tea.Cmd) {
	var switched []string
	for _, name := range s.Agents {
		agent := config.FindAgent(name)
		// Either mechanism will do — a map of states or a list of the disabled.
		// Which one an agent keeps is SetState's problem, not this loop's.
		if agent == nil || (agent.Skills.OverridesKey == "" && agent.Skills.DisabledKey == "") {
			continue
		}
		state := skills.StateOff
		if s.State(name) == skills.StateOff {
			state = "" // clear it
		}
		if _, err := skills.SetState(agent.Skills, m.repoRoot, s.Name, state); err != nil {
			return m, m.transientErrCmd(err.Error())
		}
		switched = append(switched, name)
	}
	if len(switched) == 0 {
		return m, m.transientErrCmd(s.Name + " has no agent that can switch it off")
	}

	verb := "disabled"
	if s.State(switched[0]) == skills.StateOff {
		verb = "enabled"
	}
	return m, tea.Batch(m.scanSkillsCmd,
		m.noticeCmd(fmt.Sprintf("%s %s for %s", verb, s.Name, strings.Join(switched, ", "))))
}

// relinkSkillsCmd makes the links that should already exist: the repository's
// skills bridged to every agent that reads a different tree, and then every
// workspace pointed at them.
//
// One key for both because they are one gap seen from two sides — a skill the
// list shows and an agent cannot reach — and because bridging without
// relinking would leave the new tree missing from every existing worktree.
func (m Model) relinkSkillsCmd() tea.Cmd {
	return func() tea.Msg {
		bridged, err := skills.Bridge(m.repoRoot)
		if err != nil {
			return errMsg{err}
		}
		n := 0
		for _, ws := range m.workspaces {
			// Bridging may have just created a tree this workspace has never
			// seen, so Link is asked about every workspace rather than only the
			// ones already known to be missing one.
			linked, err := skills.Link(m.repoRoot, m.svc.WorktreePath(ws.Name))
			if err != nil {
				return errMsg{err}
			}
			if len(linked) > 0 {
				n++
			}
		}
		return skillsRelinkedMsg{bridged: bridged, count: n}
	}
}

// cloneSkillCmd fetches a skill from a git URL into the chosen trees.
func cloneSkillCmd(url string, dirs []string) tea.Cmd {
	return func() tea.Msg {
		if err := skills.Clone(url, dirs...); err != nil {
			return skillClonedMsg{err: err}
		}
		return skillClonedMsg{name: skills.CloneName(url), trees: len(dirs)}
	}
}

// discoverSkillsCmd asks a site what skills it publishes at its well-known
// path. Every typed address goes through here first: it is one request, and
// the answer decides whether there is anything to pick from before git is
// asked to clone the same address.
func discoverSkillsCmd(site string) tea.Cmd {
	return func() tea.Msg {
		// Short. A publisher answers from a CDN, and the fallback waiting
		// behind this is a git clone the user is expecting to take longer.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries, err := skills.Discover(ctx, site)
		return skillsDiscoveredMsg{site: site, entries: entries, err: err}
	}
}

// installSkillCmd downloads one published skill into the chosen trees.
func installSkillCmd(e skills.Entry, dirs []string) tea.Cmd {
	return func() tea.Msg {
		// Longer than discovery: this is an archive rather than an index, and
		// the digest is checked over the whole of it before anything is written.
		// The trees past the first are local copies of what the first got.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := skills.Install(ctx, e, dirs...); err != nil {
			return skillClonedMsg{err: err}
		}
		return skillClonedMsg{name: e.Name, trees: len(dirs)}
	}
}

// updateSkillCmd asks the site that published a skill whether it has changed.
//
// Only a skill taken from an index has anything to ask: a clone carries its
// own .git and is a `git pull` away, and a hand-written skill has nowhere to
// look. Update says so itself rather than the row having to know.
func updateSkillCmd(s skills.Skill) tea.Cmd {
	return func() tea.Msg {
		// Room for the index and, if the digest moved, the artifact behind it —
		// the same budget installing one gets, because that is what this
		// becomes when something has changed.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		changed, err := skills.Update(ctx, s.Dir)
		return skillUpdatedMsg{name: s.Name, changed: changed, err: err}
	}
}

// probeSkillsCmd asks the configured agent what it actually loaded.
//
// Only the configured one: the answer costs a subprocess and a session, and
// the agent opentree would launch is the one whose reading of these
// directories decides what a workspace can do.
func (m Model) probeSkillsCmd(agent config.PredefinedAgent) tea.Cmd {
	return func() tea.Msg {
		// Generous: a cold agent has an npm package to load and a login to
		// check before it says anything.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		commands, err := skills.Probe(ctx, agent, m.repoRoot)
		return skillProbedMsg{agent: agent.Name, commands: commands, err: err}
	}
}

// probeRefusal is why this agent cannot be asked, or empty when it can.
//
// An agent that does not put its skills among its commands would answer with a
// list that mentions none of them, and every row would come back flagged as
// unloaded. Refusing costs the user an answer; asking anyway would cost them
// the truth.
func probeRefusal(agent *config.PredefinedAgent) string {
	switch {
	case agent == nil:
		return "no agent configured to ask"
	case !agent.Skills.AdvertisesSkills:
		return agent.Name + " does not name its skills over the protocol — nothing to check against"
	}
	return ""
}

// probeMismatch is what the agent's own answer says about a row that opentree's
// reading of the directories does not.
//
// Three disagreements, worth different things. A skill opentree expects to be
// loaded and the agent has never heard of means a directory or an override has
// been read wrong. One switched off that the agent offers anyway means the
// override is not honoured where opentree wrote it. And one the agent reads at
// all, on a row saying it cannot, means the directory the skill sits in is read
// by an agent the registry does not credit.
//
// That third case is the one this check exists for. Judging only the agents a
// row already names is how the first version of `v` agreed with a tab that had
// just called a skill invisible to opencode while opencode was answering to it:
// the list was never asked about the claim it had not made.
func (m Model) probeMismatch(s skills.Skill) string {
	if m.skillsTab.probe == nil {
		return ""
	}
	loaded := m.skillsTab.probe[s.Name]

	if !slices.Contains(s.Agents, m.skillsTab.probed) {
		if !loaded {
			return ""
		}
		// Stated as the observation rather than the cause. It may be a missing
		// registry entry, a tree opentree does not know, or a skill sharing a
		// name with one of the agent's own commands — and guessing between
		// those is what produced the wrong answer in the first place.
		mark, _, _ := config.Brand(m.skillsTab.probed)
		return "⚠ " + mark + " reads it anyway"
	}

	switch expected := s.State(m.skillsTab.probed) != skills.StateOff; {
	case expected && !loaded:
		return "⚠ not loaded"
	case !expected && loaded:
		return "⚠ still loaded"
	}
	return ""
}

// blindAgents are the registered agents that cannot see a repository skill and
// could be given it.
//
// Repo scope only: agents auto-load each other's user trees, so a machine-wide
// skill is already shared, and one that is not is a copy away rather than a
// link away.
//
// Standard trees only, too. A skill in a directory the user registered in one
// agent's own config is where they put it, for the agent whose config names it
// — and Bridge could not hand it over anyway, since the other agent does not
// search a tree the way the config-registered one is searched. Warning about
// that would be a warning with nothing behind it.
func (m Model) blindAgents(s skills.Skill) []string {
	if s.Scope != skills.ScopeRepo || !m.inStandardRepoTree(s) {
		return nil
	}
	var out []string
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.RepoDirs) > 0 && !slices.Contains(s.Agents, agent.Name) {
			out = append(out, agent.Name)
		}
	}
	return out
}

// inStandardRepoTree reports whether a skill sits directly in one of the
// repository trees the registry names — the ones Bridge links together.
func (m Model) inStandardRepoTree(s skills.Skill) bool {
	parent := filepath.Dir(s.Dir)
	for _, agent := range config.PredefinedAgents {
		for _, rel := range agent.Skills.RepoDirs {
			if parent == filepath.Join(m.repoRoot, rel) {
				return true
			}
		}
	}
	return false
}

// pickerMove steps a clamped cursor for the tab's shared movement keys,
// reporting whether the key was one of them. Five lists move this way — the
// tab itself, three tree pickers and the entry picker — and this is the one
// copy of the arithmetic; the fourth hand copy had already drifted once.
func pickerMove(key string, cursor *int, rows int) bool {
	switch key {
	case "up", "k":
		if *cursor > 0 {
			*cursor--
		}
	case "down", "j":
		if *cursor < rows-1 {
			*cursor++
		}
	default:
		return false
	}
	return true
}

// updateSkills handles keys while the Skills tab has focus.
//
// It consumes every key, including the ones it does nothing with. Falling
// through to the workspace handler would let a key like "n" open a dialog that
// View never draws while this tab is up — a text input collecting keystrokes
// behind a screen that gives no sign it exists.
func (m Model) updateSkills(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Choosing which copies of a skill go, before the confirmation asks about
	// them. Only reached when there is more than one.
	if m.skillsTab.deleteChoosing {
		targets := m.deleteTargets(*m.skillsTab.deleting)
		if pickerMove(msg.String(), &m.skillsTab.pickCursor, len(targets)) {
			return m, nil
		}
		switch msg.String() {
		case " ":
			m = m.toggleTarget(targets)
		case "enter":
			// The ticks are the whole answer here, with no falling back to the
			// row under the cursor: this picker opens with one already ticked,
			// so unticking every row is a deliberate "none of these" rather
			// than an empty selection meaning the cursor's own.
			if len(m.skillsTab.chosen) == 0 {
				return m, nil
			}
			m.skillsTab.deleteChoosing = false
		case "esc", "q":
			m.skillsTab.deleting, m.skillsTab.deleteChoosing, m.skillsTab.chosen = nil, false, nil
		}
		return m, nil
	}

	// Delete confirmation. Removing a skill deletes a directory no git history
	// is necessarily holding, so it is confirmed rather than undone.
	if m.skillsTab.deleting != nil {
		switch msg.String() {
		case "y", "Y":
			name, doomed := m.skillsTab.deleting.Name, m.doomedSkills()
			m.skillsTab.deleting, m.skillsTab.chosen = nil, nil
			if err := skills.Delete(doomed...); err != nil {
				// Rescanned even so: one directory refusing does not mean none
				// of them went.
				return m, tea.Batch(m.scanSkillsCmd, m.transientErrCmd(err.Error()))
			}
			removed := "deleted " + name
			if len(doomed) > 1 {
				removed += " from " + plural(len(doomed), "tree")
			}
			return m, tea.Batch(m.scanSkillsCmd, m.noticeCmd(removed))
		case "n", "esc", "q":
			m.skillsTab.deleting, m.skillsTab.chosen = nil, nil
		}
		return m, nil
	}

	// Typing the git URL of a skill to add.
	if m.skillsTab.adding {
		switch msg.String() {
		case "enter":
			url := strings.TrimSpace(m.skillsTab.addURL)
			m.skillsTab.adding = false
			if url == "" {
				m.skillsTab.addURL = ""
				return m, nil
			}
			// Ask the site before git does. An address that publishes an index
			// names its skills and their hashes; one that does not answers in
			// well under the time a clone would have taken anyway.
			m.skillsTab.addURL, m.skillsTab.discovering = url, true
			return m, discoverSkillsCmd(url)
		case "esc":
			m = m.cancelAdd()
		case "backspace":
			if m.skillsTab.addURL != "" {
				m.skillsTab.addURL = m.skillsTab.addURL[:len(m.skillsTab.addURL)-1]
			}
		default:
			// Every rune of the key, not one: a pasted URL arrives as a single
			// message carrying all of it, and a URL is far more often pasted
			// than typed.
			if msg.Type == tea.KeyRunes {
				m.skillsTab.addURL += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Waiting on the site. Only escape means anything here — the answer is one
	// request away and every other key would act on a step not reached yet.
	if m.skillsTab.discovering {
		if msg.String() == "esc" {
			m = m.cancelAdd()
		}
		return m, nil
	}

	// Picking which of a site's skills to take.
	if m.skillsTab.entries != nil && m.skillsTab.entry == nil {
		if pickerMove(msg.String(), &m.skillsTab.pickCursor, len(m.skillsTab.entries)) {
			return m, nil
		}
		switch msg.String() {
		case "enter":
			if m.skillsTab.pickCursor >= len(m.skillsTab.entries) {
				return m, nil
			}
			// A copy, not a pointer into the slice: the entry outlives the
			// list it was chosen from.
			chosen := m.skillsTab.entries[m.skillsTab.pickCursor]
			m.skillsTab.entry = &chosen
			return m.pickTree()
		case "esc", "q":
			m = m.cancelAdd()
		}
		return m, nil
	}

	// Picking the tree the skill lands in.
	if m.skillsTab.addURL != "" {
		targets := m.addTargets()
		if pickerMove(msg.String(), &m.skillsTab.pickCursor, len(targets)) {
			return m, nil
		}
		switch msg.String() {
		case " ":
			m = m.toggleTarget(targets)
		case "enter":
			chosen := m.chosenTargets(targets)
			if len(chosen) == 0 {
				return m, nil
			}
			dirs := targetDirs(chosen)
			url, entry := m.skillsTab.addURL, m.skillsTab.entry
			m = m.cancelAdd()
			if entry != nil {
				return m, tea.Batch(
					m.noticeCmd("downloading "+entry.Name+"…"),
					installSkillCmd(*entry, dirs))
			}
			return m, tea.Batch(
				m.noticeCmd("cloning "+ui.Truncate(url, 60)+"…"),
				cloneSkillCmd(url, dirs))
		case "esc", "q":
			m = m.cancelAdd()
		}
		return m, nil
	}

	// Copy destination picker.
	if m.skillsTab.copying != nil {
		targets := m.copyTargets(*m.skillsTab.copying)
		if pickerMove(msg.String(), &m.skillsTab.pickCursor, len(targets)) {
			return m, nil
		}
		switch msg.String() {
		case " ":
			m = m.toggleTarget(targets)
		case "enter":
			chosen := m.chosenTargets(targets)
			if len(chosen) == 0 {
				return m, nil
			}
			source := *m.skillsTab.copying
			m.skillsTab.copying, m.skillsTab.chosen = nil, nil
			if err := skills.CopyTo(source, targetDirs(chosen)...); err != nil {
				// Rescanned even so: one tree refusing does not mean none of
				// them took the copy.
				return m, tea.Batch(m.scanSkillsCmd, m.transientErrCmd(err.Error()))
			}
			return m, tea.Batch(m.scanSkillsCmd,
				m.noticeCmd(fmt.Sprintf("copied %s to %s", source.Name, targetsLabel(chosen))))
		case "esc", "q":
			m.skillsTab.copying, m.skillsTab.chosen = nil, nil
		}
		return m, nil
	}

	if m.skillsTab.filtering {
		switch msg.String() {
		case "enter", "esc":
			m.skillsTab.filtering = false
		case "backspace":
			if m.skillsTab.filter != "" {
				m.skillsTab.filter = m.skillsTab.filter[:len(m.skillsTab.filter)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.skillsTab.filter += string(msg.Runes)
			}
		}
		m.skillsTab.cursor = 0
		return m, nil
	}

	if pickerMove(msg.String(), &m.skillsTab.cursor, len(m.visibleSkills())) {
		return m, nil
	}
	switch msg.String() {
	case "tab", "shift+tab", "left", "right", "esc":
		if msg.String() == "esc" && m.skillsTab.filter != "" {
			m.skillsTab.filter = ""
			m.skillsTab.cursor = 0
			return m, nil
		}
		m.tab = tabWorkspaces
	case "/":
		m.skillsTab.filtering = true
	case "r":
		return m, m.scanSkillsCmd
	case "a":
		m.skillsTab.adding, m.skillsTab.addURL = true, ""
	case "v":
		if m.skillsTab.probing {
			return m, nil
		}
		// Refused here rather than inside the command, so the model that carries
		// the message back is this one and not a copy of it.
		agent := config.FindAgent(m.cfg.Agent.Command)
		if why := probeRefusal(agent); why != "" {
			return m, m.transientErrCmd(why)
		}
		m.skillsTab.probing = true
		return m, tea.Batch(
			m.noticeCmd("asking "+m.cfg.Agent.Command+" what it loaded…"),
			m.probeSkillsCmd(*agent))
	case "enter":
		if s, ok := m.currentSkill(); ok {
			return m, editSkillCmd(s)
		}
	case "u":
		s, ok := m.currentSkill()
		if !ok || m.skillsTab.updating {
			return m, nil
		}
		m.skillsTab.updating = true
		return m, tea.Batch(
			m.noticeCmd("checking "+s.Name+" with its publisher…"),
			updateSkillCmd(s))
	case "x":
		s, ok := m.currentSkill()
		if !ok {
			return m, nil
		}
		m.skillsTab.deleting, m.skillsTab.chosen = &s, nil
		// One copy is not a choice worth drawing a picker for — the same rule
		// the add flow follows when a site publishes a single skill.
		targets := m.deleteTargets(s)
		if len(targets) > 1 {
			m.skillsTab.deleteChoosing = true
			// The row x was pressed on opens ticked, not merely under the
			// cursor. x is already a statement about that copy, and dropping it
			// the moment a second one is ticked is the wrong surprise to spring
			// on a key that deletes directories. Space can untick it.
			m.skillsTab.chosen, m.skillsTab.pickCursor = map[string]bool{s.Dir: true}, 0
			for i, t := range targets {
				if t.Dir == s.Dir {
					m.skillsTab.pickCursor = i
				}
			}
		}
	case "c":
		s, ok := m.currentSkill()
		if !ok {
			return m, nil
		}
		if len(m.copyTargets(s)) == 0 {
			return m, m.transientErrCmd(s.Name + " is already in every tree")
		}
		m.skillsTab.copying = &s
		m.skillsTab.pickCursor, m.skillsTab.chosen = 0, map[string]bool{}
	case "t":
		if s, ok := m.currentSkill(); ok {
			return m.toggleSkill(s)
		}
	case "l":
		return m, m.relinkSkillsCmd()
	case "E":
		m.showErrLog = true
	case "q":
		// Same guard the workspace list uses: quitting mid-create would orphan
		// a half-built workspace, and tabbing over here does not make that safe.
		if m.workspaceCreating || m.workspaceDeleting {
			return m, m.transientErrCmd("an operation is in progress — ctrl+c to force quit")
		}
		return m, tea.Quit
	}
	return m, nil
}

// skillsView renders the Skills tab.
func (m Model) skillsView() string {
	var s strings.Builder
	s.WriteString(renderLogo())
	s.WriteString("\n\n")
	s.WriteString(m.tabBar())
	s.WriteString("\n\n")

	if m.skillsTab.deleteChoosing {
		return m.pickTreeView("Delete "+m.skillsTab.deleting.Name+" from…",
			m.deleteTargets(*m.skillsTab.deleting), dialogDanger)
	}
	if m.skillsTab.deleting != nil {
		return m.skillDeleteView()
	}
	if m.skillsTab.copying != nil {
		return m.pickTreeView("Copy "+m.skillsTab.copying.Name+" to…",
			m.copyTargets(*m.skillsTab.copying), dialogAccent)
	}
	// Typing first: the URL fills in a character at a time, so a picker drawn on
	// "is there a URL yet" appears on the first keystroke and hides the prompt
	// still collecting the rest of it.
	if m.skillsTab.adding {
		return appStyle.Render(s.String() + m.skillAddView())
	}
	if m.skillsTab.discovering {
		return appStyle.Render(s.String() + filterPromptStyle.Render("asking") + " " +
			ui.Truncate(m.skillsTab.addURL, 60) + " what it publishes…\n\n" +
			diffStyle.Render("  Esc cancels."))
	}
	if m.skillsTab.entries != nil && m.skillsTab.entry == nil {
		return m.pickEntryView()
	}
	if m.skillsTab.addURL != "" {
		verb := "Clone "
		if m.skillsTab.entry != nil {
			verb = "Install "
		}
		return m.pickTreeView(verb+m.addName()+" into…", m.addTargets(), dialogAccent)
	}

	if m.skillsTab.filtering {
		s.WriteString(filterPromptStyle.Render("/") + " " + m.skillsTab.filter + "█\n\n")
	} else if m.skillsTab.filter != "" {
		s.WriteString(filterPromptStyle.Render(
			fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.skillsTab.filter)) + "\n\n")
	}

	visible := m.visibleSkills()
	if len(visible) == 0 {
		if m.skillsTab.filter != "" {
			s.WriteString(itemStyle.Render("No skills match the filter.") + "\n")
		} else {
			s.WriteString(itemStyle.Render("No skills installed.") + "\n")
			s.WriteString(diffStyle.Render(
				"  Agents read them from "+m.knownTrees()) + "\n")
		}
	} else {
		shared := sharedNames(visible)
		budget := m.height - lipgloss.Height(s.String()) - skillsChromeLines
		start, end := skillWindow(len(visible), m.skillsTab.cursor, budget)
		if start > 0 {
			s.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
		}
		for i := start; i < end; i++ {
			s.WriteString(m.renderSkillRow(visible[i], i == m.skillsTab.cursor, shared[visible[i].Name]))
		}
		if end < len(visible) {
			s.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↓ %d more", len(visible)-end)) + "\n")
		}
	}

	s.WriteString(m.toastLine() + "\n")
	s.WriteString(m.divider() + "\n" + m.skillsStatusBar() + "\n")
	// Two lines rather than one long one: the keys no longer fit an 80-column
	// terminal, and a help line that wraps costs the list a row it has already
	// budgeted for.
	s.WriteString(helpStyle.Render(
		"↑/k ↓/j move • enter edit • a add • u update • c copy • x delete • t on/off\n" +
			"l link • v verify with agent • / filter • r rescan • tab workspaces"))
	return appStyle.Render(s.String())
}

// renderSkillRow draws one skill: its name and badges, then its description.
func (m Model) renderSkillRow(s skills.Skill, selected, isShared bool) string {
	style := itemStyle
	if selected {
		style = selectedItemStyle
	}

	// Every agent that reads this skill's tree, not just whoever owns the
	// directory: opencode auto-loads Claude Code's global skills, so one
	// SKILL.md is genuinely usable from both and a single badge would say the
	// opposite.
	title := fmt.Sprintf("%-*s %s %s",
		skillNameWidth, ui.Truncate(s.Name, skillNameWidth),
		pad(agentMarks(s), skillAgentWidth),
		skillScopeStyle.Render(s.Scope.String()))

	var tags []string
	if tag := stateTag(s); tag != "" {
		tags = append(tags, skillOffStyle.Render(tag))
	}
	if isShared {
		tags = append(tags, sharedTagStyle.Render("duplicate"))
	}
	if n := m.workspacesMissing(s); n > 0 {
		tags = append(tags, uncommittedStyle.Render(fmt.Sprintf("⚠ %s missing", plural(n, "worktree"))))
	}
	for _, name := range m.blindAgents(s) {
		mark, _, _ := config.Brand(name)
		tags = append(tags, uncommittedStyle.Render("⚠ invisible to "+mark))
	}
	if tag := m.probeMismatch(s); tag != "" {
		tags = append(tags, dangerStyle.Render(tag))
	}

	// Appended while they fit, rather than all of them: a title that wrapped
	// would make the row three lines tall and scroll the list past the bottom
	// of the frame, since the window arithmetic below counts two.
	for _, tag := range tags {
		if lipgloss.Width(title)+lipgloss.Width(tag)+4 > m.panelWidth() {
			title += " …"
			break
		}
		title += "  " + tag
	}

	// Descriptions run to a paragraph — the convention is that an agent reads
	// them to decide whether a skill applies. Truncated rather than wrapped:
	// the window arithmetic below assumes two lines per row, and a row that
	// silently became three would scroll the list past the bottom of the frame.
	desc := s.Description
	if desc == "" {
		desc = "no description"
	}
	return style.Render(title+"\n"+diffStyle.Render("  "+ui.Truncate(desc, m.panelWidth()-4))) + "\n"
}

func (m Model) skillDeleteView() string {
	s, doomed := *m.skillsTab.deleting, m.doomedSkills()

	title := fmt.Sprintf("Delete skill %q?", s.Name)
	body := confirmLabelStyle.Render(doomed[0].Dir + " and everything in it will be removed.")
	if len(doomed) > 1 {
		title = fmt.Sprintf("Delete %s from %s?", s.Name, plural(len(doomed), "tree"))
		lines := make([]string, len(doomed))
		for i, d := range doomed {
			lines[i] = "  " + d.Dir
		}
		body = confirmLabelStyle.Render(strings.Join(lines, "\n"))
	} else if len(s.Agents) > 1 {
		// The question this dialog is most often asked in the wrong place:
		// deleting is per directory, and one directory is frequently every
		// agent's. Naming them beats letting the deletion answer it.
		body += "\n" + diffStyle.Render("  One directory, read by "+
			brandMarks(s.Agents)+diffStyle.Render(" — all of them lose it."))
	}

	return m.dialogCard(title, body,
		fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("confirm"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel")),
		dialogDanger)
}

// pickTreeView is the tree picker, shared by copying a skill and cloning one:
// both end in "which of these directories does it go in", and answering it
// twice in two layouts would be two things to keep in step.
// colour is the card's, so a picker that is about to remove directories does
// not wear the same trim as one that is about to add them.
func (m Model) pickTreeView(title string, targets []skillTarget, colour lipgloss.Color) string {
	var b strings.Builder
	for i, t := range targets {
		cursor, style := "  ", itemStyle
		if i == m.skillsTab.pickCursor {
			cursor, style = "▶ ", selectedItemStyle
		}
		tick := "  "
		if m.skillsTab.chosen[t.Dir] {
			tick = "✓ "
		}
		// selectedItemStyle draws a left border where itemStyle has only
		// padding, so an unselected row starts one column further left. One
		// space puts them back in line: without it the marks column shifts
		// sideways as the cursor moves down the list.
		lead := " "
		if i == m.skillsTab.pickCursor {
			lead = ""
		}
		// The readers, in the same glyphs the list rows use. A tree is rarely
		// read only by the agent it is named after, and two trees with a reader
		// in common are two copies of one skill.
		row := lead + pad(cursor+tick+t.Label, skillTargetWidth) + brandMarks(t.Agents)
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(style.Render(strings.TrimRight(row, " ")))
	}
	return m.dialogCard(title, b.String(),
		dialogHintStyle.Render("↑/↓ navigate • space marks more • Enter confirm • Esc cancel"),
		colour)
}

// pickEntryView lists what a site publishes, one line each. The description is
// the publisher's own, and it is the line the agent will match against later —
// so it is what there is to choose on, not decoration.
func (m Model) pickEntryView() string {
	var b strings.Builder
	// The card's interior, less the padding it adds and the left border a
	// selected row carries. Getting this wrong wraps every row in the list.
	width := m.dialogMaxWidth() - 2*dialogPadding - 3
	// A publisher with more skills than the terminal has rows still has to be
	// choosable. skillWindow counts in rows two lines tall, which is what
	// these are: the same name-then-purpose shape as the tab's own list.
	start, end := skillWindow(len(m.skillsTab.entries), m.skillsTab.pickCursor,
		max(m.height-16, skillRowLines))
	if start > 0 {
		b.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		e := m.skillsTab.entries[i]
		cursor, style := "  ", itemStyle
		if i == m.skillsTab.pickCursor {
			cursor, style = "▶ ", selectedItemStyle
		}
		row := ui.Truncate(cursor+e.Name, width)
		if e.Description != "" {
			row += "\n" + diffStyle.Render("  "+ui.Truncate(e.Description, width-4))
		}
		if i > start {
			b.WriteString("\n")
		}
		b.WriteString(style.Render(row))
	}
	if end < len(m.skillsTab.entries) {
		b.WriteString("\n" + scrollHintStyle.Render(
			fmt.Sprintf("  ↓ %d more", len(m.skillsTab.entries)-end)))
	}
	return m.dialogCard(
		fmt.Sprintf("%s publishes %d skills", ui.Truncate(m.skillsTab.addURL, 40), len(m.skillsTab.entries)),
		b.String(),
		dialogHintStyle.Render("↑/↓ navigate • Enter confirm • Esc cancel"), dialogAccent)
}

// skillAddView prompts for the address to add a skill from.
func (m Model) skillAddView() string {
	return filterPromptStyle.Render("add skill from") + " " + m.skillsTab.addURL + "█\n\n" +
		diffStyle.Render("  A site that publishes skills, or a git repository whose\n"+
			"  root holds a SKILL.md. Esc cancels.")
}

// knownTrees names where skills live, for the empty state — an empty list
// should say where opentree looked rather than only that it found nothing.
func (m Model) knownTrees() string {
	var dirs []string
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.UserDirs) > 0 && !slices.Contains(dirs, agent.Skills.UserDirs[0]) {
			dirs = append(dirs, agent.Skills.UserDirs[0])
		}
	}
	return strings.Join(dirs, " and ")
}

func (m Model) skillsStatusBar() string {
	parts := []string{stat(len(m.skillsTab.list), pluralLabel(len(m.skillsTab.list), "skill"))}
	for _, agent := range config.PredefinedAgents {
		// An agent with no skills mechanism at all is left out rather than
		// tallied at zero: "GitHub Copilot 0" says it has somewhere to put
		// skills and nothing in it, which is a different thing from having no
		// such concept.
		if len(agent.Skills.UserDirs) == 0 && len(agent.Skills.RepoDirs) == 0 {
			continue
		}
		// Counts what the agent will actually load, not what is installed for
		// it — the whole point of reading the overrides.
		n := 0
		for _, s := range m.skillsTab.list {
			if slices.Contains(s.Agents, agent.Name) && s.State(agent.Name) != skills.StateOff {
				n++
			}
		}
		mark, colour, _ := config.Brand(agent.Name)
		label := fmt.Sprintf("%s %s %d", mark, agent.Name, n)
		if colour != "" {
			label = lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render(label)
		}
		parts = append(parts, label)
	}

	// What the agent itself confirmed, kept on screen rather than announced and
	// forgotten: the counts to its left are opentree's reading of the
	// documentation, and this is the one number that was checked.
	if m.skillsTab.probe != nil {
		confirmed, expected, unexpected := 0, 0, 0
		for _, s := range m.skillsTab.list {
			// Read by an agent this row does not name. Counted separately
			// because it answers a different question: the ratio says whether
			// what the list claims is true, and this says whether it is
			// complete. Only the first was ever checked, and a row can be
			// wrong without making any claim to check.
			if !slices.Contains(s.Agents, m.skillsTab.probed) {
				if m.skillsTab.probe[s.Name] {
					unexpected++
				}
				continue
			}
			if s.State(m.skillsTab.probed) == skills.StateOff {
				continue
			}
			expected++
			if m.skillsTab.probe[s.Name] {
				confirmed++
			}
		}
		label := fmt.Sprintf("%s confirmed %d/%d", m.skillsTab.probed, confirmed, expected)
		if unexpected > 0 {
			label += fmt.Sprintf(" · %d unexpected", unexpected)
		}
		parts = append(parts, statusBarStyle.Render(label))
	}
	// No outer wrap: an ANSI reset inside a styled part does not resume an
	// enclosing colour, so each part and each separator styles itself.
	return strings.Join(parts, statusBarStyle.Render("  •  "))
}

// tabBar renders the two top-level places, the active one highlighted.
func (m Model) tabBar() string {
	name := func(label string, tab int) string {
		if m.tab == tab {
			return titleStyle.Render(label)
		}
		return tabInactiveStyle.Render(label)
	}
	return name("Workspaces", tabWorkspaces) + tabInactiveStyle.Render("  ") + name("Skills", tabSkills)
}

func plural(n int, noun string) string {
	return fmt.Sprintf("%d %s", n, pluralLabel(n, noun))
}

// pluralLabel is plural's label half, for the status bars — they render the
// count themselves, in their own colour, and only want the noun agreed.
func pluralLabel(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// skillNameWidth is the name column. Wide enough for almost every skill name,
// and truncated past it so the badge column stays where the eye left it.
const skillNameWidth = 28

// skillAgentWidth holds the agent marks: one glyph and a space per agent.
const skillAgentWidth = 6

// skillTargetWidth is the tree picker's label column, wide enough for the
// longest agent-and-scope pair plus its cursor and tick.
const skillTargetWidth = 28

// brandMarks is one glyph per agent, each in its own colour — the same legend
// the tab's footer spells out.
func brandMarks(names []string) string {
	marks := make([]string, 0, len(names))
	for _, name := range names {
		mark, colour, _ := config.Brand(name)
		if colour != "" {
			mark = lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render(mark)
		}
		marks = append(marks, mark)
	}
	return strings.Join(marks, " ")
}

// agentMarks is one glyph per agent that can read the skill, in that agent's
// colour — and greyed for an agent whose settings switch the skill off, so the
// row says both "installed here" and "not actually available", which dropping
// the mark would lose.
//
// Glyphs rather than names because a skill is commonly readable by every agent
// at once, and spelling them all out would crowd the description off the row.
// The footer tally names them, and is the legend.
func agentMarks(s skills.Skill) string {
	marks := make([]string, 0, len(s.Agents))
	for _, name := range s.Agents {
		mark, colour, _ := config.Brand(name)
		switch {
		case s.State(name) == skills.StateOff:
			mark = skillOffStyle.Render(mark)
		case colour != "":
			mark = lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render(mark)
		}
		marks = append(marks, mark)
	}
	return strings.Join(marks, " ")
}

// stateTag is the row's one word about availability, taken from the agents that
// disagree with plain "on". Agents rarely disagree — the usual case is one
// override applying to every reader — so the tag names the state, and names the
// agent only when they differ.
func stateTag(s skills.Skill) string {
	labels := map[string][]string{}
	for _, name := range s.Agents {
		if label := s.State(name).Label(); label != "" {
			labels[label] = append(labels[label], name)
		}
	}
	if len(labels) == 0 {
		return ""
	}
	var parts []string
	for label, agents := range labels {
		if len(agents) == len(s.Agents) {
			parts = append(parts, label)
			continue
		}
		for _, name := range agents {
			mark, _, _ := config.Brand(name)
			parts = append(parts, label+" for "+mark)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// pad right-pads to a visible width. fmt's %-*s counts escape sequences as
// characters, so a styled string padded that way lands short and the column
// after it wanders.
func pad(s string, width int) string {
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// skillsChromeLines is what the list must leave for the status bar, the two
// help lines, the toast slot, and the blanks around them.
const skillsChromeLines = 6 + toastLines

// skillRowLines is the height of one skill row: its title and its description.
const skillRowLines = 2

// skillWindow is the slice of skills that fits, scrolled to keep the cursor
// inside it — the same reason the workspace list has one. An over-tall frame
// loses its first lines, taking the header and the tab bar with them.
func skillWindow(total, cursor, budget int) (start, end int) {
	rows := budget / skillRowLines
	if rows < 1 {
		rows = 1
	}
	if rows >= total {
		return 0, total
	}
	start = min(max(cursor-rows/2, 0), total-rows)
	return start, start + rows
}
