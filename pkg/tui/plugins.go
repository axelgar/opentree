package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/plugins"
	"github.com/axelgar/opentree/pkg/skills"
	"github.com/axelgar/opentree/pkg/ui"
)

// The Plugins tab is a place, like Skills and Servers: the inventory of what
// this machine has installed from the Agent Plugins standard, and the one
// screen that shows a whole plugin — its skills already appear on the Skills
// tab as rows among the others, but the plugin as a unit, with the MCP
// servers it declares and whatever failed validation, only lists here.

// pluginsTab is the tab's own state.
type pluginsTab struct {
	list   []plugins.Plugin
	cursor int
	// adding collects the git URL a keypress at a time; addURL is what has
	// arrived so far.
	adding bool
	addURL string
	// removing is the plugin x asked about, waiting on y/n.
	removing *plugins.Plugin
	// busy is an install or removal in flight. One at a time: both end in a
	// rescan, and two racing would interleave their store writes.
	busy bool
}

type pluginsScannedMsg struct {
	list []plugins.Plugin
}

type pluginInstalledMsg struct {
	plugin plugins.Plugin
	linked int
	err    error
}

type pluginRemovedMsg struct {
	name string
	err  error
}

type pluginEditedMsg struct {
	err error
}

// scanPluginsCmd re-reads the store. A plain function rather than a method:
// the store is per machine and the model holds nothing it needs.
func scanPluginsCmd() tea.Msg {
	return pluginsScannedMsg{list: plugins.Installed()}
}

// installPluginCmd clones, validates and links in one command: the links are
// what make the install mean something, and a plugin installed but not linked
// would look identical to one that worked.
func installPluginCmd(url string) tea.Cmd {
	return func() tea.Msg {
		p, err := plugins.Install(url)
		if err != nil {
			return pluginInstalledMsg{err: err}
		}
		linked, err := skills.LinkPlugins()
		return pluginInstalledMsg{plugin: p, linked: len(linked), err: err}
	}
}

// removePluginCmd unlinks before it removes — links first, matched by where
// they resolve, so the agents are never left holding entries that point at a
// store directory that no longer exists.
func removePluginCmd(p plugins.Plugin) tea.Cmd {
	return func() tea.Msg {
		if err := skills.UnlinkPlugin(p.Dir); err != nil {
			return pluginRemovedMsg{name: p.Name, err: err}
		}
		return pluginRemovedMsg{name: p.Name, err: plugins.Remove(p.Name)}
	}
}

// editPluginCmd opens the manifest in $EDITOR — enter on a row answers "what
// exactly did this plugin declare" with the file itself.
func editPluginCmd(p plugins.Plugin) tea.Cmd {
	name, args := editorCommand()
	args = append(args, filepath.Join(p.Dir, "plugin.json"))
	// #nosec G702 -- the editor is the user's own $VISUAL/$EDITOR, the same
	// value git and every other terminal tool hands a file to.
	c := exec.Command(name, args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return pluginEditedMsg{err: err}
	})
}

// currentPlugin is the row under the cursor.
func (m Model) currentPlugin() (plugins.Plugin, bool) {
	if len(m.pluginsTab.list) == 0 || m.pluginsTab.cursor >= len(m.pluginsTab.list) {
		return plugins.Plugin{}, false
	}
	return m.pluginsTab.list[m.pluginsTab.cursor], true
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

// updatePlugins drives the tab. Like the other tabs it consumes every key,
// including the ones it ignores — falling through would feed a workspace
// dialog the view never draws.
func (m Model) updatePlugins(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Typing the git URL of a plugin to add.
	if m.pluginsTab.adding {
		switch msg.String() {
		case "enter":
			url := strings.TrimSpace(m.pluginsTab.addURL)
			m.pluginsTab.adding, m.pluginsTab.addURL = false, ""
			if url == "" {
				return m, nil
			}
			m.pluginsTab.busy = true
			return m, tea.Batch(m.noticeCmd("installing "+url+"…"), installPluginCmd(url))
		case "esc":
			m.pluginsTab.adding, m.pluginsTab.addURL = false, ""
		case "backspace":
			if m.pluginsTab.addURL != "" {
				m.pluginsTab.addURL = m.pluginsTab.addURL[:len(m.pluginsTab.addURL)-1]
			}
		default:
			// Every rune of the key, not one: a pasted URL arrives as a single
			// message carrying all of it.
			if msg.Type == tea.KeyRunes {
				m.pluginsTab.addURL += string(msg.Runes)
			}
		}
		return m, nil
	}

	// Remove confirmation. Removing deletes a store directory and every link
	// pointing into it, so it is confirmed rather than undone.
	if m.pluginsTab.removing != nil {
		switch msg.String() {
		case "y", "Y":
			doomed := *m.pluginsTab.removing
			m.pluginsTab.removing = nil
			m.pluginsTab.busy = true
			return m, tea.Batch(m.noticeCmd("removing "+doomed.Name+"…"), removePluginCmd(doomed))
		case "n", "esc", "q":
			m.pluginsTab.removing = nil
		}
		return m, nil
	}

	if pickerMove(msg.String(), &m.pluginsTab.cursor, len(m.pluginsTab.list)) {
		return m, nil
	}

	switch msg.String() {
	case "tab", "right":
		m.tab = tabServers
	case "shift+tab", "left":
		m.tab = tabSkills
		return m, m.scanSkillsCmd
	case "esc":
		m.tab = tabWorkspaces
	case "r":
		return m, scanPluginsCmd
	case "a":
		if m.pluginsTab.busy {
			return m, m.transientErrCmd("an install or removal is already running")
		}
		m.pluginsTab.adding, m.pluginsTab.addURL = true, ""
	case "x":
		if m.pluginsTab.busy {
			return m, m.transientErrCmd("an install or removal is already running")
		}
		if p, ok := m.currentPlugin(); ok {
			m.pluginsTab.removing = &p
		}
	case "enter":
		if p, ok := m.currentPlugin(); ok {
			return m, editPluginCmd(p)
		}
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

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// pluginsView renders the Plugins tab.
func (m Model) pluginsView() string {
	var s strings.Builder
	s.WriteString(renderLogo())
	s.WriteString("\n\n")
	s.WriteString(m.tabBar())
	s.WriteString("\n\n")

	if m.pluginsTab.adding {
		return appStyle.Render(s.String() + m.pluginAddView())
	}
	if m.pluginsTab.removing != nil {
		return m.pluginRemoveView()
	}

	if len(m.pluginsTab.list) == 0 {
		s.WriteString(itemStyle.Render("No plugins installed.") + "\n")
		s.WriteString(diffStyle.Render(
			"  a adds one from a git repository holding a plugin.json —\n"+
				"  the Agent Plugins format (agent-plugins.org). Its skills reach\n"+
				"  every agent in every worktree from one install.") + "\n")
		s.WriteString("\n" + m.toastLine() + "\n")
		s.WriteString(m.pluginsHelp())
		return appStyle.Render(s.String())
	}

	for i, p := range m.pluginsTab.list {
		s.WriteString(m.renderPluginRow(p, i == m.pluginsTab.cursor))
	}
	s.WriteString("\n" + m.toastLine() + "\n")
	s.WriteString(m.pluginsHelp())
	return appStyle.Render(s.String())
}

// renderPluginRow draws one plugin: name, version and counts, then whatever
// the counts cannot say — the description, where each declared MCP server
// points, and every validation problem. Variable height on purpose: this list
// is a handful of rows with no scroll window, and a plugin's servers and
// failures are exactly what the tab exists to show.
func (m Model) renderPluginRow(p plugins.Plugin, selected bool) string {
	style, cursor := itemStyle, "  "
	if selected {
		style, cursor = selectedItemStyle, "> "
	}

	title := cursor + p.Name
	if p.Version != "" {
		title += " " + p.Version
	}
	title += "  " + skillScopeStyle.Render(
		plural(len(p.Skills), "skill")+" · "+fmt.Sprintf("%d mcp", len(p.Servers)))
	if len(p.Problems) > 0 {
		title += "  " + uncommittedStyle.Render("⚠ "+plural(len(p.Problems), "problem"))
	}

	out := style.Render(ui.Truncate(title, m.panelWidth()))
	if p.Description != "" {
		out += "\n" + diffStyle.Render("    "+ui.Truncate(p.Description, m.panelWidth()-4))
	}
	// The servers are configuration somebody may later be asked to trust, so
	// each says where it would point. Env and header values were masked when
	// the store was read; nothing here could print them if it wanted to.
	for _, server := range p.Servers {
		line := "    mcp " + server.Name + " · " + server.Type
		if target := pluginServerTarget(server); target != "" {
			line += " · " + target
		}
		out += "\n" + diffStyle.Render(ui.Truncate(line, m.panelWidth()-4))
	}
	for _, problem := range p.Problems {
		out += "\n" + uncommittedStyle.Render(ui.Truncate("    ⚠ "+problem, m.panelWidth()-4))
	}
	return out + "\n"
}

// pluginServerTarget is the one line of a server entry that is safe and
// useful to show: the command for a local one, the URL for a remote one.
func pluginServerTarget(s plugins.Server) string {
	if s.Type == "stdio" {
		return s.Command
	}
	return s.URL
}

// pluginAddView prompts for the repository to install from.
func (m Model) pluginAddView() string {
	return filterPromptStyle.Render("add plugin from") + " " + m.pluginsTab.addURL + "█\n\n" +
		diffStyle.Render("  A git repository whose root holds a plugin.json.\n"+
			"  Esc cancels.")
}

// pluginRemoveView confirms a removal, saying what goes with it.
func (m Model) pluginRemoveView() string {
	p := *m.pluginsTab.removing
	body := confirmLabelStyle.Render(p.Dir+" and everything in it will be removed,") + "\n" +
		confirmLabelStyle.Render("along with its "+pluralLabel(len(p.Skills), "skill link")+
			" in every agent's tree.")
	return m.dialogCard(fmt.Sprintf("Remove plugin %q?", p.Name), body,
		fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("confirm"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel")),
		dialogDanger)
}

// pluginsHelp is this tab's keys, which are not the workspace list's.
func (m Model) pluginsHelp() string {
	return helpStyle.Render(
		"↑/k ↓/j move • a add • x remove • enter edit manifest • r rescan • tab workspaces")
}
