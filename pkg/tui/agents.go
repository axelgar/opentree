package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/registry"
	"github.com/axelgar/opentree/pkg/ui"
)

// The Agents tab is where `opentree agents` lives in the dashboard: every
// agent this machine knows — the built-in four and whatever was installed
// from the ACP Registry — with the same verbs the command line has. The A
// picker on the workspace list stays the quick switch; this is the place
// you come back to when the question is "what have I got, and what else is
// there".
//
// Wording is the command line's, on purpose. A person who has read the
// README's registry section, or run `agents add` once, should meet the same
// refusals and the same consent text here, so nothing they learned in one
// place is wrong in the other.

// agentRow is one line of the tab: an agent, or the id of a store entry
// that no longer loads. The broken kind exists so x can clear it — the
// same reason `agents remove` accepts an id whose record is unreadable.
type agentRow struct {
	agent  config.PredefinedAgent
	id     string // the registry id of a broken install; "" otherwise
	broken string // why it does not load, as the store reports it
}

// registryConfirm is a plan waiting on y: the consent card the command line
// prints before `agents add` and `agents update` fetch anything.
type registryConfirm struct {
	plan   registry.Plan
	update bool   // PlanUpdate rather than NewPlan: the title and verb differ
	from   string // the installed version an update replaces
}

// indexPurpose is why the index was fetched, so the answer knows what to do
// with itself: open the browser, or compare versions for one install or all.
type indexPurpose int

const (
	indexBrowse indexPurpose = iota
	indexUpdateOne
	indexUpdateAll
)

// agentsTab is the tab's own state.
type agentsTab struct {
	rows   []agentRow
	cursor int
	// busy is a fetch, install, update or removal in flight. One at a time:
	// every one of them ends in a reload of the runtime agent list, and two
	// racing would interleave their store writes.
	busy bool

	// The index browser: what the registry lists, filtered by what has been
	// typed after /, with the staleness note FetchOrCached returned.
	browsing     bool
	index        []registry.Entry
	note         string
	filter       string
	filtering    bool
	browseCursor int

	// confirm is the plan waiting on y; queue is what U still has to ask
	// about after it, one card at a time, because one agent's new version
	// must never smuggle in another's.
	confirm *registryConfirm
	queue   []registryConfirm

	// removing is the row x asked about, waiting on y/n.
	removing *agentRow
}

type registryIndexMsg struct {
	index   registry.Index
	note    string
	err     error
	purpose indexPurpose
	id      string // indexUpdateOne: the install being checked
}

// registryRanMsg is the outcome of a plan, add or update alike.
type registryRanMsg struct {
	id      string
	version string
	from    string // the version an update replaced; "" for an add
	update  bool
	output  string // what the install printed, for the error log
	err     error
}

type registryRemovedMsg struct {
	id  string
	err error
}

// reloadAgents makes the rows describe the store as it is now. It runs on
// the update loop rather than as a command because it replaces
// config.PredefinedAgents, which the whole program reads: a goroutine
// swapping it under the renderer would be a data race, and the read is a
// handful of small files.
func (m *Model) reloadAgents() {
	problems := registry.LoadInstalled()
	rows := make([]agentRow, 0, len(config.PredefinedAgents)+len(problems))
	for _, a := range config.PredefinedAgents {
		rows = append(rows, agentRow{agent: a})
	}
	for _, p := range problems {
		// The store names the directory first, the same way `agents update`
		// completes it: "<id>: <what went wrong>".
		id, why, ok := strings.Cut(p, ":")
		if !ok || registry.ValidateID(id) != nil {
			continue
		}
		// The store's sentence ends by naming the command that clears it;
		// here the key does, so the command-line remedy is trimmed off.
		why, _, _ = strings.Cut(why, " — `opentree")
		why, _, _ = strings.Cut(why, "; `opentree")
		rows = append(rows, agentRow{id: id, broken: strings.TrimSpace(why)})
	}
	m.agentsTab.rows = rows
	if m.agentsTab.cursor >= len(rows) {
		m.agentsTab.cursor = max(len(rows)-1, 0)
	}
}

// fetchIndexCmd asks the registry, or the cache when it cannot. The same
// budget `agents add` gives the fetch.
func fetchIndexCmd(purpose indexPurpose, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		index, note, err := registry.FetchOrCached(ctx, registry.DefaultIndexURL)
		return registryIndexMsg{index: index, note: note, err: err, purpose: purpose, id: id}
	}
}

// runPlanCmd performs an install or update. The install's own output goes
// to a buffer rather than the terminal — the dashboard owns the screen —
// and is shown only if the run fails, where it is the whole diagnosis.
func runPlanCmd(c registryConfirm) tea.Cmd {
	return func() tea.Msg {
		var out strings.Builder
		plan := c.plan
		plan.Stdout, plan.Stderr = &out, &out
		rec, err := plan.Run(context.Background())
		return registryRanMsg{
			id: c.plan.Entry.ID, version: rec.Entry.Version, from: c.from,
			update: c.update, output: out.String(), err: err,
		}
	}
}

func removeRegistryCmd(id string) tea.Cmd {
	return func() tea.Msg {
		return registryRemovedMsg{id: id, err: registry.Remove(id)}
	}
}

// currentAgentRow is the row under the cursor.
func (m Model) currentAgentRow() (agentRow, bool) {
	rows := m.agentsTab.rows
	if len(rows) == 0 || m.agentsTab.cursor >= len(rows) {
		return agentRow{}, false
	}
	return rows[m.agentsTab.cursor], true
}

// visibleEntries is the index as filtered: the same substring rule over id,
// name and description that `agents search <term>` applies.
func (m Model) visibleEntries() []registry.Entry {
	term := strings.ToLower(strings.TrimSpace(m.agentsTab.filter))
	if term == "" {
		return m.agentsTab.index
	}
	var out []registry.Entry
	for _, e := range m.agentsTab.index {
		if strings.Contains(strings.ToLower(e.ID), term) ||
			strings.Contains(strings.ToLower(e.Name), term) ||
			strings.Contains(strings.ToLower(e.Description), term) {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

const registryBusy = "an install or removal is already running"

// updateAgents drives the tab. Like the other tabs it consumes every key,
// including the ones it ignores — falling through would feed a workspace
// dialog the view never draws. Modal states answer first, innermost out:
// the adapter card, the registry consent card, the removal card, the
// browser's filter, the browser itself, and only then the list.
func (m Model) updateAgents(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.agentInstallConfirm != nil {
		return m.handleAdapterConfirm(msg)
	}

	if c := m.agentsTab.confirm; c != nil {
		switch msg.String() {
		case "y", "Y":
			m.agentsTab.confirm = nil
			m.agentsTab.busy = true
			verb := "installing"
			if c.update {
				verb = "updating"
			}
			return m, tea.Batch(m.noticeCmd(verb+" "+c.plan.Entry.ID+"…"), runPlanCmd(*c))
		case "n":
			// n is "not this one": with more queued, the next is asked about;
			// `agents update` prints "skipped" and moves on the same way.
			m.agentsTab.confirm = nil
			m.advanceUpdateQueue()
		case "esc", "q":
			m.agentsTab.confirm = nil
			m.agentsTab.queue = nil
		}
		return m, nil
	}

	if r := m.agentsTab.removing; r != nil {
		switch msg.String() {
		case "y", "Y":
			id := r.id
			if r.agent.Origin != nil {
				id = r.agent.Origin.ID
			}
			m.agentsTab.removing = nil
			m.agentsTab.busy = true
			return m, tea.Batch(m.noticeCmd("removing "+id+"…"), removeRegistryCmd(id))
		case "n", "esc", "q":
			m.agentsTab.removing = nil
		}
		return m, nil
	}

	if m.agentsTab.browsing {
		return m.updateAgentBrowser(msg)
	}

	if pickerMove(msg.String(), &m.agentsTab.cursor, len(m.agentsTab.rows)) {
		return m, nil
	}

	switch msg.String() {
	case "tab", "right":
		// Rescanned on the way in, like every entry to the Skills tab.
		m.tab = tabSkills
		return m, m.scanSkillsCmd
	case "shift+tab", "left", "esc":
		m.tab = tabWorkspaces
	case "r":
		m.reloadAgents()
	case "a":
		if m.agentsTab.busy {
			return m, m.transientErrCmd(registryBusy)
		}
		m.agentsTab.busy = true
		return m, tea.Batch(m.noticeCmd("fetching "+registry.DefaultIndexURL), fetchIndexCmd(indexBrowse, ""))
	case "enter":
		return m.useAgent(config.FindConfigFile())
	case "g":
		return m.useAgent(config.GlobalConfigPath())
	case "i":
		return m.setupAgent()
	case "u":
		return m.updateOne()
	case "U":
		return m.updateAll()
	case "x":
		return m.askRemove()
	case "E":
		m.showErrLog = true
	case "q":
		// Same guard the other tabs apply: quitting mid-create would orphan a
		// half-built workspace, and tabbing over here does not make that safe.
		if m.workspaceCreating || m.workspaceDeleting {
			return m, m.transientErrCmd("an operation is in progress — ctrl+c to force quit")
		}
		return m, tea.Quit
	}
	return m, nil
}

// useAgent is enter (this repository) and g (everywhere): the readiness
// branching the picker's enter does, with the same remedies.
func (m Model) useAgent(configPath string) (Model, tea.Cmd) {
	row, ok := m.currentAgentRow()
	if !ok {
		return m, nil
	}
	if row.broken != "" {
		return m, m.transientErrCmd(fmt.Sprintf("%s does not load — x clears the install", row.id))
	}
	agent := row.agent
	switch status, _ := m.readiness(agent); status {
	case agentNotFound:
		remedy := fmt.Sprintf("install %s first", agent.Command)
		if agent.Origin != nil {
			remedy = "u reinstalls it"
		}
		return m, m.transientErrCmd(fmt.Sprintf("%s is not installed — %s", agent.Name, remedy))
	case agentAdapterMissing:
		if m.agentsTab.busy {
			return m, m.transientErrCmd(registryBusy)
		}
		m.agentInstallConfirm = &m.agentsTab.rows[m.agentsTab.cursor].agent
		m.agentPendingSelect = &m.agentsTab.rows[m.agentsTab.cursor].agent
		m.agentPendingPath = configPath
		return m, nil
	}
	if errMsg := m.selectAgentIn(agent, configPath); errMsg != "" {
		return m, m.transientErrCmd(errMsg)
	}
	where := "for this repository"
	if configPath == config.GlobalConfigPath() {
		where = "everywhere"
	}
	return m, m.noticeCmd("now using " + agent.Name + " " + where)
}

// setupAgent is i, which is `agents setup`: fetch a built-in's adapter, and
// for a registry install say which key manages it instead.
func (m Model) setupAgent() (Model, tea.Cmd) {
	row, ok := m.currentAgentRow()
	if !ok {
		return m, nil
	}
	if row.broken != "" {
		return m, m.transientErrCmd(fmt.Sprintf("%s does not load — x clears the install", row.id))
	}
	agent := row.agent
	if agent.Origin != nil {
		return m, m.transientErrCmd(fmt.Sprintf("%s was installed from the ACP Registry — u updates it", agent.Name))
	}
	if len(agent.ACPInstallCommand()) == 0 {
		return m, m.transientErrCmd(fmt.Sprintf("%s needs no adapter", agent.Name))
	}
	if agent.ACPInstalled() {
		return m, m.transientErrCmd(fmt.Sprintf("%s is already installed", agent.ACPCommand()))
	}
	if m.agentsTab.busy {
		return m, m.transientErrCmd(registryBusy)
	}
	m.agentInstallConfirm = &m.agentsTab.rows[m.agentsTab.cursor].agent
	return m, nil
}

// updateOne is u: `agents update <id>` for the row under the cursor.
func (m Model) updateOne() (Model, tea.Cmd) {
	row, ok := m.currentAgentRow()
	if !ok {
		return m, nil
	}
	if row.broken != "" {
		return m, m.transientErrCmd(fmt.Sprintf("%s does not load — x clears the install", row.id))
	}
	if row.agent.Origin == nil {
		return m, m.transientErrCmd(fmt.Sprintf("%s is built into opentree — i manages its adapter", row.agent.Name))
	}
	if m.agentsTab.busy {
		return m, m.transientErrCmd(registryBusy)
	}
	m.agentsTab.busy = true
	return m, tea.Batch(m.noticeCmd("fetching "+registry.DefaultIndexURL),
		fetchIndexCmd(indexUpdateOne, row.agent.Origin.ID))
}

// updateAll is U: `agents update` with no id, every install checked against
// one fetch.
func (m Model) updateAll() (Model, tea.Cmd) {
	if m.agentsTab.busy {
		return m, m.transientErrCmd(registryBusy)
	}
	if len(m.registryInstalls()) == 0 {
		return m, m.transientErrCmd("nothing is installed from the registry — a installs an agent")
	}
	m.agentsTab.busy = true
	return m, tea.Batch(m.noticeCmd("fetching "+registry.DefaultIndexURL), fetchIndexCmd(indexUpdateAll, ""))
}

// askRemove is x: refused for a built-in, confirmed for everything else.
func (m Model) askRemove() (Model, tea.Cmd) {
	row, ok := m.currentAgentRow()
	if !ok {
		return m, nil
	}
	if row.broken == "" && row.agent.Origin == nil {
		return m, m.transientErrCmd(fmt.Sprintf("%q is built into opentree and cannot be removed", row.agent.Command))
	}
	if m.agentsTab.busy {
		return m, m.transientErrCmd(registryBusy)
	}
	m.agentsTab.removing = &row
	return m, nil
}

// registryInstalls is the rows that came from the registry, in list order.
func (m Model) registryInstalls() []config.PredefinedAgent {
	var out []config.PredefinedAgent
	for _, r := range m.agentsTab.rows {
		if r.broken == "" && r.agent.Origin != nil {
			out = append(out, r.agent)
		}
	}
	return out
}

// advanceUpdateQueue puts the next queued update in front of the user, or
// nothing when U has asked about every one.
func (m *Model) advanceUpdateQueue() {
	if len(m.agentsTab.queue) == 0 {
		return
	}
	next := m.agentsTab.queue[0]
	m.agentsTab.queue = m.agentsTab.queue[1:]
	m.agentsTab.confirm = &next
}

// updateAgentBrowser drives the index browser: the filter prompt while it is
// open, and the list otherwise.
func (m Model) updateAgentBrowser(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.agentsTab.filtering {
		switch msg.String() {
		case "enter", "esc":
			m.agentsTab.filtering = false
		case "backspace":
			if m.agentsTab.filter != "" {
				m.agentsTab.filter = m.agentsTab.filter[:len(m.agentsTab.filter)-1]
				m.agentsTab.browseCursor = 0
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.agentsTab.filter += string(msg.Runes)
				m.agentsTab.browseCursor = 0
			}
		}
		return m, nil
	}

	entries := m.visibleEntries()
	if pickerMove(msg.String(), &m.agentsTab.browseCursor, len(entries)) {
		return m, nil
	}
	switch msg.String() {
	case "/":
		m.agentsTab.filtering = true
	case "esc", "q":
		// esc clears the filter first, and only then closes: a filter is
		// what esc most recently did something to.
		if m.agentsTab.filter != "" {
			m.agentsTab.filter = ""
			m.agentsTab.browseCursor = 0
			return m, nil
		}
		m.agentsTab.browsing = false
	case "enter":
		if len(entries) == 0 || m.agentsTab.browseCursor >= len(entries) {
			return m, nil
		}
		return m.planAdd(entries[m.agentsTab.browseCursor])
	}
	return m, nil
}

// planAdd is `agents add` up to the question: the same gate, in the same
// order, with the same sentences, ending in the consent card rather than a
// prompt on stdin.
func (m Model) planAdd(entry registry.Entry) (Model, tea.Cmd) {
	if agent := config.FindAgent(entry.ID); agent != nil {
		if agent.Origin != nil {
			return m, m.transientErrCmd(fmt.Sprintf("%s %s is already installed — u refreshes it",
				agent.Origin.ID, agent.Origin.Version))
		}
		return m, m.transientErrCmd(fmt.Sprintf("%q is built into opentree — i manages its adapter", entry.ID))
	}
	// The loader will skip an install whose display name a built-in answers
	// to, so refusing here beats installing something that can never load.
	if config.FindAgent(entry.Name) != nil {
		return m, m.transientErrCmd(fmt.Sprintf("%q is named %q, which opentree's built-in %s already answers to",
			entry.ID, entry.Name, entry.Name))
	}
	plan, err := registry.NewPlan(entry, registry.DefaultIndexURL)
	if err != nil {
		return m, m.transientErrCmd(err.Error())
	}
	m.agentsTab.browsing = false
	m.agentsTab.confirm = &registryConfirm{plan: plan}
	return m, nil
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// handleRegistryIndex is the fetched index, put to whatever purpose asked
// for it.
func (m Model) handleRegistryIndex(msg registryIndexMsg) (Model, tea.Cmd) {
	m.agentsTab.busy = false
	if msg.err != nil {
		return m, m.transientErrCmd(msg.err.Error())
	}
	// A stale index is worth knowing about, but not worth the toast slot: the
	// browser shows the note in its card, and for an update check it goes to
	// the log rather than covering the answer.
	if msg.note != "" {
		m.appendErrLog(msg.note)
	}
	switch msg.purpose {
	case indexBrowse:
		m.agentsTab.browsing = true
		m.agentsTab.index = msg.index.Agents
		m.agentsTab.note = msg.note
		m.agentsTab.filter, m.agentsTab.filtering = "", false
		m.agentsTab.browseCursor = 0
		return m, nil
	case indexUpdateOne, indexUpdateAll:
		entries := map[string]registry.Entry{}
		for _, e := range msg.index.Agents {
			entries[e.ID] = e
		}
		var queue []registryConfirm
		var upToDate, unlisted []string
		for _, agent := range m.registryInstalls() {
			id := agent.Origin.ID
			if msg.purpose == indexUpdateOne && id != msg.id {
				continue
			}
			entry, listed := entries[id]
			switch {
			case !listed:
				// Not an error and not a removal: the install still works,
				// and deleting somebody's agent because an index dropped it
				// would be the index deciding what runs on this machine.
				unlisted = append(unlisted, id)
			case entry.Version == agent.Origin.Version:
				upToDate = append(upToDate, id)
			default:
				plan, err := registry.PlanUpdate(entry, registry.DefaultIndexURL)
				if err != nil {
					return m, m.transientErrCmd(fmt.Sprintf("%s: %v", id, err))
				}
				queue = append(queue, registryConfirm{plan: plan, update: true, from: agent.Origin.Version})
			}
		}
		switch {
		case msg.purpose == indexUpdateOne && len(unlisted) == 1:
			return m, m.transientErrCmd(fmt.Sprintf(
				"%s is no longer in the registry — it stays installed; x deletes it", msg.id))
		case msg.purpose == indexUpdateOne && len(upToDate) == 1:
			return m, m.noticeCmd(fmt.Sprintf("%s — up to date", msg.id))
		case len(queue) == 0:
			return m, m.noticeCmd(updateSummary(upToDate, unlisted))
		}
		m.agentsTab.queue = queue
		m.advanceUpdateQueue()
		if msg.purpose == indexUpdateAll && (len(upToDate) > 0 || len(unlisted) > 0) {
			return m, m.noticeCmd(updateSummary(upToDate, unlisted))
		}
	}
	return m, nil
}

// updateSummary is U's one line about the installs that needed no card.
func updateSummary(upToDate, unlisted []string) string {
	if len(upToDate) == 0 && len(unlisted) == 0 {
		return "nothing to update"
	}
	var parts []string
	if len(upToDate) > 0 {
		parts = append(parts, plural(len(upToDate), "agent")+" up to date")
	}
	if len(unlisted) > 0 {
		parts = append(parts, strings.Join(unlisted, ", ")+" no longer in the registry — kept")
	}
	return strings.Join(parts, "; ")
}

// handleRegistryRan is the outcome of an install or update. Both reload the
// runtime list — the store changed, and the picker and the next chat should
// see it — and then U's queue, if any, asks about the next one.
func (m Model) handleRegistryRan(msg registryRanMsg) (Model, tea.Cmd) {
	m.agentsTab.busy = false
	m.reloadAgents()
	if msg.err != nil {
		if out := strings.TrimSpace(msg.output); out != "" {
			m.appendErrLog(msg.id + ": " + out)
		}
		text := fmt.Sprintf("✗ %s: %v", msg.id, msg.err)
		if msg.update {
			// The swap only happens on success, so the old install is still
			// whole — worth saying, or a failed update reads as a broken agent.
			text += fmt.Sprintf(" — the installed %s is untouched", msg.from)
		}
		m.advanceUpdateQueue()
		return m, m.transientErrCmd(text)
	}
	m.advanceUpdateQueue()
	if msg.update {
		return m, m.noticeCmd(fmt.Sprintf("%s updated to %s", msg.id, msg.version))
	}
	return m, m.noticeCmd(fmt.Sprintf("%s %s installed — enter uses it", msg.id, msg.version))
}

// handleRegistryRemoved is the outcome of x. The config may still name what
// was just removed — not an error, but saying it now beats discovering it
// at the next `opentree new`.
func (m Model) handleRegistryRemoved(msg registryRemovedMsg) (Model, tea.Cmd) {
	m.agentsTab.busy = false
	m.reloadAgents()
	if msg.err != nil {
		return m, m.transientErrCmd(msg.err.Error())
	}
	notice := "removed " + msg.id
	if m.cfg.Agent.Command == msg.id {
		notice += " — your config still names it; enter on another agent picks one"
	}
	return m, m.noticeCmd(notice)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// agentsView renders the Agents tab.
func (m Model) agentsView() string {
	if m.agentInstallConfirm != nil {
		return m.adapterConfirmView()
	}
	if m.agentsTab.confirm != nil {
		return m.registryConfirmView()
	}
	if m.agentsTab.removing != nil {
		return m.agentRemoveView()
	}
	if m.agentsTab.browsing {
		return m.agentBrowseView()
	}

	var s strings.Builder
	s.WriteString(renderLogo())
	s.WriteString("\n\n")
	s.WriteString(m.tabBar())
	s.WriteString("\n\n")

	for i, row := range m.agentsTab.rows {
		s.WriteString(m.renderAgentRow(row, i == m.agentsTab.cursor))
	}
	if len(m.registryInstalls()) == 0 {
		s.WriteString(diffStyle.Render(
			"\n  Those are the built-in four. a installs more from the ACP Registry —\n"+
				"  the same index Zed and JetBrains install from.") + "\n")
	}
	s.WriteString("\n" + m.toastLine() + "\n")
	s.WriteString(m.agentsHelp())
	return appStyle.Render(s.String())
}

// Column widths, the picker's plus a source column.
const (
	agentNameWidth   = 20
	agentCmdWidth    = 14
	agentStatusWidth = 15
	agentSourceWidth = 18
)

// renderAgentRow draws one agent: mark and name, command, readiness, where
// it came from, and the description in whatever room is left. Padded before
// styling, as the picker does, so escape codes never widen a column.
func (m Model) renderAgentRow(row agentRow, selected bool) string {
	style, cursor := itemStyle, "  "
	if selected {
		style, cursor = selectedItemStyle, "> "
	}
	width := m.panelWidth()

	if row.broken != "" {
		head := fmt.Sprintf("%s⚠ %-*s %-*s ", cursor, agentNameWidth, row.id, agentCmdWidth, row.id)
		line := head + uncommittedStyle.Render(fmt.Sprintf("%-*s", agentStatusWidth, "broken install")) +
			" " + fmt.Sprintf("%-*s", agentSourceWidth, "registry")
		out := style.Render(ui.Truncate(line, width))
		out += "\n" + uncommittedStyle.Render(ui.Truncate("    ⚠ "+row.broken+" — x clears it", width-4))
		return out + "\n"
	}

	agent := row.agent
	name := agent.Name
	if agent.IsActive(m.cfg) {
		name += " (active)"
	}
	status, ready := m.readiness(agent)
	statusSt := lipgloss.NewStyle().Foreground(ui.Faint)
	switch {
	case ready:
		statusSt = successStyle
	case status == agentAdapterMissing:
		statusSt = warnStyle
	}
	source := "built-in"
	if agent.Origin != nil {
		source = "registry " + agent.Origin.Version
	}
	mark := agent.Brand.Mark
	if agent.Brand.Colour != "" {
		mark = lipgloss.NewStyle().Foreground(lipgloss.Color(agent.Brand.Colour)).Render(mark)
	}
	head := fmt.Sprintf("%s%s %-*s %-*s %-*s %-*s ",
		cursor, agent.Brand.Mark, agentNameWidth, name, agentCmdWidth, agent.Command,
		agentStatusWidth, status, agentSourceWidth, source)
	room := width - lipgloss.Width(head) - 3
	line := fmt.Sprintf("%s%s %-*s %-*s %s %-*s %s",
		cursor, mark, agentNameWidth, ui.Truncate(name, agentNameWidth),
		agentCmdWidth, ui.Truncate(agent.Command, agentCmdWidth),
		statusSt.Render(fmt.Sprintf("%-*s", agentStatusWidth, status)),
		agentSourceWidth, source,
		diffStyle.Render(ui.Truncate(agent.Description, max(room, 8))))
	return style.Render(line) + "\n"
}

// agentBrowseView is the index browser: what the registry lists, one entry
// per two lines, windowed like the skills entry picker so a forty-entry
// index still fits a short terminal.
func (m Model) agentBrowseView() string {
	var b strings.Builder
	width := m.dialogMaxWidth() - 2*dialogPadding - 3
	if m.agentsTab.note != "" {
		b.WriteString(warnStyle.Render(ui.Truncate(m.agentsTab.note, width)) + "\n\n")
	}
	if m.agentsTab.filtering || m.agentsTab.filter != "" {
		b.WriteString(filterPromptStyle.Render("/") + " " + m.agentsTab.filter)
		if m.agentsTab.filtering {
			b.WriteString("█")
		}
		b.WriteString("\n\n")
	}

	entries := m.visibleEntries()
	if len(entries) == 0 {
		b.WriteString(diffStyle.Render(fmt.Sprintf("  nothing in the registry matches %q", m.agentsTab.filter)))
	}
	start, end := skillWindow(len(entries), m.agentsTab.browseCursor, max(m.height-18, skillRowLines))
	if start > 0 {
		b.WriteString(scrollHintStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	sawUvx := false
	for i := start; i < end; i++ {
		e := entries[i]
		sawUvx = sawUvx || e.Distribution.Uvx != nil
		cursor, style := "  ", itemStyle
		if i == m.agentsTab.browseCursor {
			cursor, style = "▶ ", selectedItemStyle
		}
		status := registry.Status(e)
		statusSt := successStyle
		if status == "" && e.Distribution.Npx == nil && len(e.Distribution.Binary) == 0 {
			status, statusSt = "uvx — not supported yet", warnStyle
		}
		head := fmt.Sprintf("%s%-*s %-10s %-12s ", cursor, agentNameWidth, e.ID, e.Version, e.Via())
		row := head + statusSt.Render(status)
		if e.Description != "" {
			row += "\n" + diffStyle.Render("  "+ui.Truncate(e.Description, width-4))
		}
		if i > start {
			b.WriteString("\n")
		}
		b.WriteString(style.Render(row))
	}
	if end < len(entries) {
		b.WriteString("\n" + scrollHintStyle.Render(fmt.Sprintf("  ↓ %d more", len(entries)-end)))
	}
	if sawUvx {
		b.WriteString("\n\n" + diffStyle.Render("  * uvx — a distribution opentree does not support yet"))
	}
	// The card covers the toast slot, so a refusal has nowhere else to
	// appear — without this, enter on an unusable entry looks like a dead key.
	if m.err != nil {
		b.WriteString("\n\n" + dangerStyle.Render(m.err.Error()))
	}
	hint := "↑/↓ navigate • / filter • Enter install • Esc close"
	if m.agentsTab.filtering {
		hint = "type to filter • Enter done • Esc done"
	}
	return m.dialogCard(fmt.Sprintf("ACP Registry — %s", plural(len(m.agentsTab.index), "agent")),
		b.String(), dialogHintStyle.Render(hint), dialogAccent)
}

// registryConfirmView is the consent card: what `agents add` and `agents
// update` print before asking, verbatim, because the words the user agreed
// to on the command line are the words they should agree to here.
func (m Model) registryConfirmView() string {
	c := *m.agentsTab.confirm
	title := fmt.Sprintf("Install into %s?", c.plan.Dir)
	verb := "install"
	body := c.plan.Describe()
	if c.update {
		title = fmt.Sprintf("Update %s?", c.plan.Entry.ID)
		verb = "update"
		body = fmt.Sprintf("%s %s → %s\n\n%s", c.plan.Entry.ID, c.from, c.plan.Entry.Version, body)
	}
	body = strings.TrimRight(body, "\n")
	footer := fmt.Sprintf("%s %s  •  %s %s",
		confirmKeyStyle.Render("y"), confirmLabelStyle.Render(verb),
		confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel"))
	if len(m.agentsTab.queue) > 0 {
		footer = fmt.Sprintf("%s %s  •  %s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render(verb),
			confirmKeyStyle.Render("n"), confirmLabelStyle.Render(fmt.Sprintf("skip (%d more)", len(m.agentsTab.queue))),
			confirmKeyStyle.Render("esc"), confirmLabelStyle.Render("cancel all"))
	}
	return m.dialogCard(title, body, footer, dialogAccent)
}

// agentRemoveView confirms a removal, saying what goes with it.
func (m Model) agentRemoveView() string {
	r := *m.agentsTab.removing
	id, dir := r.id, ""
	if r.agent.Origin != nil {
		id, dir = r.agent.Origin.ID, r.agent.Origin.Dir
	}
	body := confirmLabelStyle.Render("Its directory under " + registry.Dir() + " and everything in it will be removed.")
	if dir != "" {
		body = confirmLabelStyle.Render(dir + " and everything in it will be removed.")
	}
	if r.broken == "" && r.agent.IsActive(m.cfg) {
		body += "\n" + confirmLabelStyle.Render("This repository's config names it; pick another agent afterwards.")
	}
	return m.dialogCard(fmt.Sprintf("Remove agent %q?", id), body,
		fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("confirm"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel")),
		dialogDanger)
}

// agentsHelp is this tab's keys, which are not the workspace list's.
func (m Model) agentsHelp() string {
	return helpStyle.Render(
		"↑/k ↓/j move • enter use • g use everywhere • a add from registry • u/U update • i adapter • x remove • r rescan • tab skills")
}
