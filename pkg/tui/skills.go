package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

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

// relinkSkillsCmd repairs every workspace that cannot see the repo's skills.
func (m Model) relinkSkillsCmd() tea.Cmd {
	return func() tea.Msg {
		n := 0
		for _, ws := range m.workspaces {
			if len(ws.MissingSkills) == 0 {
				continue
			}
			linked, err := skills.Link(m.repoRoot, m.svc.WorktreePath(ws.Name))
			if err != nil {
				return errMsg{err}
			}
			if len(linked) > 0 {
				n++
			}
		}
		return skillsRelinkedMsg{count: n}
	}
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
			if len(msg.String()) == 1 {
				m.skillFilter += msg.String()
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
		return appStyle.Render(m.skillCopyView())
	}

	if m.skillFiltering {
		s.WriteString(filterPromptStyle.Render("/") + " " + m.skillFilter + "█\n\n")
	} else if m.skillFilter != "" {
		s.WriteString(filterPromptStyle.Render(
			fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.skillFilter)) + "\n\n")
	}

	if m.err != nil {
		s.WriteString(dangerStyle.Render(fmt.Sprintf("Error: %v", m.err)) + "\n\n")
	}
	if m.notice != "" {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(m.notice) + "\n\n")
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

	s.WriteString("\n" + m.skillsStatusBar() + "\n")
	s.WriteString(helpStyle.Render(
		"↑/k ↓/j move • enter edit • t enable/disable • c copy to agent • x delete • " +
			"l link worktrees • / filter • r rescan • tab workspaces"))
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
	if tag := stateTag(s); tag != "" {
		title += "  " + skillOffStyle.Render(tag)
	}
	if isShared {
		title += "  " + sharedTagStyle.Render("duplicate")
	}
	if n := m.workspacesMissing(s); n > 0 {
		title += "  " + uncommittedStyle.Render(fmt.Sprintf("⚠ missing in %s (l to link)", plural(n, "workspace")))
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

func (m Model) skillCopyView() string {
	s := *m.skillCopying
	var b strings.Builder
	b.WriteString(titleStyle.Render("Copy "+s.Name+" to…") + "\n\n")
	for i, t := range m.copyTargets(s) {
		cursor, style := "  ", itemStyle
		if i == m.skillCopyCursor {
			cursor, style = "▶ ", selectedItemStyle
		}
		b.WriteString(style.Render(cursor+t.Label) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("↑/↓ navigate • Enter copy • Esc cancel"))
	return b.String()
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

// truncate shortens plain text to width, marking the cut. Never called on
// styled text: slicing runes through an escape sequence would corrupt it.
func truncate(s string, width int) string {
	if width < 4 || lipgloss.Width(s) <= width {
		return s
	}
	return string([]rune(s)[:width-1]) + "…"
}

// skillsChromeLines is what the list must leave for the status bar, the help
// line, and the blanks around them.
const skillsChromeLines = 5

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
