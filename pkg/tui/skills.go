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

// skillTarget is a tree a skill can be copied into.
type skillTarget struct {
	Label string
	Dir   string
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
	if m.skillFilter == "" {
		return m.skills
	}
	q := strings.ToLower(m.skillFilter)
	var out []skills.Skill
	for _, s := range m.skills {
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
	if m.skillCursor < 0 || m.skillCursor >= len(visible) {
		return skills.Skill{}, false
	}
	return visible[m.skillCursor], true
}

// copyTargets are the trees a skill is not already in. A tree that already
// holds a skill of that name is left out rather than offered and then refused.
func (m Model) copyTargets(s skills.Skill) []skillTarget {
	occupied := make(map[string]bool)
	for _, existing := range m.skills {
		if existing.Name == s.Name {
			occupied[filepath.Dir(existing.Dir)] = true
		}
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
		out = append(out, skillTarget{Label: agent.Name + " · " + scope, Dir: dir})
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

// addTargets are the trees a URL could be cloned into. The name is known
// before the clone — it is the last element of the URL — so a tree that
// already holds one is left out here rather than refused after the fetch.
func (m Model) addTargets(url string) []skillTarget {
	return m.copyTargets(skills.Skill{Name: skills.CloneName(url)})
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
		if agent == nil || agent.Skills.OverridesKey == "" {
			continue
		}
		state := skills.StateOff
		if s.State(name) == skills.StateOff {
			state = "" // clear it
		}
		if _, err := skills.SetOverride(agent.Skills, m.repoRoot, s.Name, state); err != nil {
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

// cloneSkillCmd fetches a skill from a git URL into the chosen tree.
func cloneSkillCmd(url, dir string) tea.Cmd {
	return func() tea.Msg {
		if err := skills.Clone(url, dir); err != nil {
			return skillClonedMsg{err: err}
		}
		return skillClonedMsg{name: filepath.Base(dir)}
	}
}

// probeSkillsCmd asks the configured agent what it actually loaded.
//
// Only the configured one: the answer costs a subprocess and a session, and
// the agent opentree would launch is the one whose reading of these
// directories decides what a workspace can do.
func (m Model) probeSkillsCmd() tea.Cmd {
	agent := config.FindAgent(m.cfg.Agent.Command)
	if agent == nil {
		return m.transientErrCmd("no agent configured to ask")
	}
	return func() tea.Msg {
		// Generous: a cold agent has an npm package to load and a login to
		// check before it says anything.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		commands, err := skills.Probe(ctx, *agent, m.repoRoot)
		return skillProbedMsg{agent: agent.Name, commands: commands, err: err}
	}
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
	if m.skillProbe == nil {
		return ""
	}
	loaded := m.skillProbe[s.Name]

	if !slices.Contains(s.Agents, m.skillProbed) {
		if !loaded {
			return ""
		}
		// Stated as the observation rather than the cause. It may be a missing
		// registry entry, a tree opentree does not know, or a skill sharing a
		// name with one of the agent's own commands — and guessing between
		// those is what produced the wrong answer in the first place.
		mark, _, _ := config.Brand(m.skillProbed)
		return "⚠ " + mark + " reads it anyway"
	}

	switch expected := s.State(m.skillProbed) != skills.StateOff; {
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

// updateSkills handles keys while the Skills tab has focus.
//
// It consumes every key, including the ones it does nothing with. Falling
// through to the workspace handler would let a key like "n" open a dialog that
// View never draws while this tab is up — a text input collecting keystrokes
// behind a screen that gives no sign it exists.
func (m Model) updateSkills(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Delete confirmation. Removing a skill deletes a directory no git history
	// is necessarily holding, so it is confirmed rather than undone.
	if m.skillDeleting != nil {
		switch msg.String() {
		case "y", "Y":
			target := *m.skillDeleting
			m.skillDeleting = nil
			if err := skills.Delete(target); err != nil {
				return m, m.transientErrCmd(err.Error())
			}
			return m, tea.Batch(m.scanSkillsCmd, m.noticeCmd("deleted "+target.Name))
		case "n", "esc", "q":
			m.skillDeleting = nil
		}
		return m, nil
	}

	// Typing the git URL of a skill to add.
	if m.skillAdding {
		switch msg.String() {
		case "enter":
			url := strings.TrimSpace(m.skillAddURL)
			m.skillAdding = false
			if url == "" {
				m.skillAddURL = ""
				return m, nil
			}
			if len(m.addTargets(url)) == 0 {
				m.skillAddURL = ""
				return m, m.transientErrCmd(skills.CloneName(url) + " is already in every tree")
			}
			m.skillAddURL = url
			m.skillCopyCursor = 0
		case "esc":
			m.skillAdding, m.skillAddURL = false, ""
		case "backspace":
			if m.skillAddURL != "" {
				m.skillAddURL = m.skillAddURL[:len(m.skillAddURL)-1]
			}
		default:
			// Every rune of the key, not one: a pasted URL arrives as a single
			// message carrying all of it, and a URL is far more often pasted
			// than typed.
			if msg.Type == tea.KeyRunes {
				m.skillAddURL += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Picking the tree a cloned skill lands in.
	if m.skillAddURL != "" {
		targets := m.addTargets(m.skillAddURL)
		switch msg.String() {
		case "up", "k":
			if m.skillCopyCursor > 0 {
				m.skillCopyCursor--
			}
		case "down", "j":
			if m.skillCopyCursor < len(targets)-1 {
				m.skillCopyCursor++
			}
		case "enter":
			if m.skillCopyCursor >= len(targets) {
				return m, nil
			}
			url, target := m.skillAddURL, targets[m.skillCopyCursor]
			m.skillAddURL = ""
			return m, tea.Batch(
				m.noticeCmd("cloning "+truncate(url, 60)+"…"),
				cloneSkillCmd(url, target.Dir))
		case "esc", "q":
			m.skillAddURL = ""
		}
		return m, nil
	}

	// Copy destination picker.
	if m.skillCopying != nil {
		targets := m.copyTargets(*m.skillCopying)
		switch msg.String() {
		case "up", "k":
			if m.skillCopyCursor > 0 {
				m.skillCopyCursor--
			}
		case "down", "j":
			if m.skillCopyCursor < len(targets)-1 {
				m.skillCopyCursor++
			}
		case "enter":
			if m.skillCopyCursor >= len(targets) {
				return m, nil
			}
			source, target := *m.skillCopying, targets[m.skillCopyCursor]
			m.skillCopying = nil
			if err := skills.CopyTo(source, target.Dir); err != nil {
				return m, m.transientErrCmd(err.Error())
			}
			return m, tea.Batch(m.scanSkillsCmd,
				m.noticeCmd(fmt.Sprintf("copied %s to %s", source.Name, target.Label)))
		case "esc", "q":
			m.skillCopying = nil
		}
		return m, nil
	}

	if m.skillFiltering {
		switch msg.String() {
		case "enter", "esc":
			m.skillFiltering = false
		case "backspace":
			if m.skillFilter != "" {
				m.skillFilter = m.skillFilter[:len(m.skillFilter)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.skillFilter += string(msg.Runes)
			}
		}
		m.skillCursor = 0
		return m, nil
	}

	visible := m.visibleSkills()
	switch msg.String() {
	case "tab", "shift+tab", "esc":
		if msg.String() == "esc" && m.skillFilter != "" {
			m.skillFilter = ""
			m.skillCursor = 0
			return m, nil
		}
		m.tab = tabWorkspaces
	case "up", "k":
		if m.skillCursor > 0 {
			m.skillCursor--
		}
	case "down", "j":
		if m.skillCursor < len(visible)-1 {
			m.skillCursor++
		}
	case "/":
		m.skillFiltering = true
	case "r":
		return m, m.scanSkillsCmd
	case "a":
		m.skillAdding, m.skillAddURL = true, ""
	case "v":
		if m.skillProbing {
			return m, nil
		}
		m.skillProbing = true
		return m, tea.Batch(
			m.noticeCmd("asking "+m.cfg.Agent.Command+" what it loaded…"),
			m.probeSkillsCmd())
	case "enter":
		if s, ok := m.currentSkill(); ok {
			return m, editSkillCmd(s)
		}
	case "x":
		if s, ok := m.currentSkill(); ok {
			m.skillDeleting = &s
		}
	case "c":
		s, ok := m.currentSkill()
		if !ok {
			return m, nil
		}
		if len(m.copyTargets(s)) == 0 {
			return m, m.transientErrCmd(s.Name + " is already in every tree")
		}
		m.skillCopying = &s
		m.skillCopyCursor = 0
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

	if m.skillDeleting != nil {
		return appStyle.Render(m.skillDeleteView())
	}
	if m.skillCopying != nil {
		return appStyle.Render(m.pickTreeView("Copy "+m.skillCopying.Name+" to…",
			m.copyTargets(*m.skillCopying)))
	}
	// Typing first: the URL fills in a character at a time, so a picker drawn on
	// "is there a URL yet" appears on the first keystroke and hides the prompt
	// still collecting the rest of it.
	if m.skillAdding {
		return appStyle.Render(s.String() + m.skillAddView())
	}
	if m.skillAddURL != "" {
		return appStyle.Render(m.pickTreeView("Clone "+skills.CloneName(m.skillAddURL)+" into…",
			m.addTargets(m.skillAddURL)))
	}

	if m.skillFiltering {
		s.WriteString(filterPromptStyle.Render("/") + " " + m.skillFilter + "█\n\n")
	} else if m.skillFilter != "" {
		s.WriteString(filterPromptStyle.Render(
			fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.skillFilter)) + "\n\n")
	}

	visible := m.visibleSkills()
	if len(visible) == 0 {
		if m.skillFilter != "" {
			s.WriteString(itemStyle.Render("No skills match the filter.") + "\n")
		} else {
			s.WriteString(itemStyle.Render("No skills installed.") + "\n")
			s.WriteString(diffStyle.Render(
				"  Agents read them from "+m.knownTrees()) + "\n")
		}
	} else {
		shared := sharedNames(visible)
		budget := m.height - lipgloss.Height(s.String()) - skillsChromeLines
		start, end := skillWindow(len(visible), m.skillCursor, budget)
		if start > 0 {
			s.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
		}
		for i := start; i < end; i++ {
			s.WriteString(m.renderSkillRow(visible[i], i == m.skillCursor, shared[visible[i].Name]))
		}
		if end < len(visible) {
			s.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↓ %d more", len(visible)-end)) + "\n")
		}
	}

	s.WriteString(m.toastLine() + "\n")
	s.WriteString("\n" + m.skillsStatusBar() + "\n")
	// Two lines rather than one long one: the keys no longer fit an 80-column
	// terminal, and a help line that wraps costs the list a row it has already
	// budgeted for.
	s.WriteString(helpStyle.Render(
		"↑/k ↓/j move • enter edit • a add from git • c copy • x delete • t on/off\n" +
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
		skillNameWidth, truncate(s.Name, skillNameWidth),
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
	return style.Render(title+"\n"+diffStyle.Render("  "+truncate(desc, m.panelWidth()-4))) + "\n"
}

func (m Model) skillDeleteView() string {
	s := *m.skillDeleting
	return deleteDialogStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
		dangerStyle.Render(fmt.Sprintf("Delete skill %q?", s.Name)),
		confirmLabelStyle.Render(s.Dir+" and everything in it will be removed."),
		fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("confirm"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel")),
	))
}

// pickTreeView is the tree picker, shared by copying a skill and cloning one:
// both end in "which of these directories does it go in", and answering it
// twice in two layouts would be two things to keep in step.
func (m Model) pickTreeView(title string, targets []skillTarget) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	for i, t := range targets {
		cursor, style := "  ", itemStyle
		if i == m.skillCopyCursor {
			cursor, style = "▶ ", selectedItemStyle
		}
		b.WriteString(style.Render(cursor+t.Label) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("↑/↓ navigate • Enter confirm • Esc cancel"))
	return b.String()
}

// skillAddView prompts for the git URL to clone.
func (m Model) skillAddView() string {
	return filterPromptStyle.Render("clone skill from") + " " + m.skillAddURL + "█\n\n" +
		diffStyle.Render("  A repository whose root holds a SKILL.md. Esc cancels.")
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
	parts := []string{plural(len(m.skills), "skill")}
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
		for _, s := range m.skills {
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
	if m.skillProbe != nil {
		confirmed, expected, unexpected := 0, 0, 0
		for _, s := range m.skills {
			// Read by an agent this row does not name. Counted separately
			// because it answers a different question: the ratio says whether
			// what the list claims is true, and this says whether it is
			// complete. Only the first was ever checked, and a row can be
			// wrong without making any claim to check.
			if !slices.Contains(s.Agents, m.skillProbed) {
				if m.skillProbe[s.Name] {
					unexpected++
				}
				continue
			}
			if s.State(m.skillProbed) == skills.StateOff {
				continue
			}
			expected++
			if m.skillProbe[s.Name] {
				confirmed++
			}
		}
		label := fmt.Sprintf("%s confirmed %d/%d", m.skillProbed, confirmed, expected)
		if unexpected > 0 {
			label += fmt.Sprintf(" · %d unexpected", unexpected)
		}
		parts = append(parts, label)
	}
	return statusBarStyle.Render(strings.Join(parts, "  •  "))
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
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// skillNameWidth is the name column. Wide enough for almost every skill name,
// and truncated past it so the badge column stays where the eye left it.
const skillNameWidth = 28

// skillAgentWidth holds the agent marks: one glyph and a space per agent.
const skillAgentWidth = 6

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
