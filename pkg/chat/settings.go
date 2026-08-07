package chat

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// settingsWindow caps how many rows the picker shows at once. Model lists run
// to thirty entries and the footer cannot grow to meet them.
const settingsWindow = 8

// settings is the picker over an agent's declared config options. It is written
// against whatever the agent sends rather than against model-and-mode, so an
// agent offering different controls — or none — needs no code here.
type settings struct {
	open bool

	// configID is empty while choosing which option to change, and set to that
	// option's id while choosing its value.
	configID string
	cursor   int
}

func (s settings) choosingValue() bool { return s.configID != "" }

// configOption finds a declared option by id.
func configOption(options []acp.ConfigOption, id string) (acp.ConfigOption, bool) {
	for _, o := range options {
		if o.ID == id {
			return o, true
		}
	}
	return acp.ConfigOption{}, false
}

// settingsRows is what the picker currently lists: the options themselves, or
// the values of the one being changed.
func (m Model) settingsRows() []completionItem {
	if !m.settings.choosingValue() {
		rows := make([]completionItem, 0, len(m.configOptions))
		for _, o := range m.configOptions {
			rows = append(rows, completionItem{value: o.Name, desc: o.CurrentValue})
		}
		return rows
	}

	opt, ok := configOption(m.configOptions, m.settings.configID)
	if !ok {
		return nil
	}
	rows := make([]completionItem, 0, len(opt.Options))
	for _, v := range opt.Options {
		desc := firstLine(v.Description)
		if v.Value == opt.CurrentValue {
			desc = "current"
		}
		rows = append(rows, completionItem{value: v.Name, desc: desc})
	}
	return rows
}

func (m Model) openSettings() (tea.Model, tea.Cmd) {
	if len(m.configOptions) == 0 {
		m.err = fmt.Errorf("%s declares no settings to change", m.opts.Agent)
		return m.relayout(), nil
	}
	m.settings = settings{open: true}
	return m.relayout(), nil
}

// handleSettingsKey drives the picker. Escape steps back a level rather than
// closing outright, so a wrong turn into a thirty-item model list costs one key.
func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.settingsRows()

	switch msg.String() {
	case "esc", "ctrl+c":
		if m.settings.choosingValue() {
			m.settings = settings{open: true}
			return m.relayout(), nil
		}
		m.settings = settings{}
		return m.relayout(), nil

	case "up", "ctrl+p":
		if len(rows) > 0 {
			m.settings.cursor = (m.settings.cursor - 1 + len(rows)) % len(rows)
		}
		return m.relayout(), nil

	case "down", "ctrl+n":
		if len(rows) > 0 {
			m.settings.cursor = (m.settings.cursor + 1) % len(rows)
		}
		return m.relayout(), nil

	case "enter":
		return m.chooseSetting(m.settings.cursor, rows)
	}

	if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 && n <= len(rows) {
		return m.chooseSetting(n-1, rows)
	}
	return m, nil
}

func (m Model) chooseSetting(i int, rows []completionItem) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(rows) {
		return m, nil
	}

	if !m.settings.choosingValue() {
		m.settings = settings{open: true, configID: m.configOptions[i].ID}
		// Land on whatever is currently set, so the picker opens where you are.
		if opt, ok := configOption(m.configOptions, m.settings.configID); ok {
			for j, v := range opt.Options {
				if v.Value == opt.CurrentValue {
					m.settings.cursor = j
				}
			}
		}
		return m.relayout(), nil
	}

	opt, ok := configOption(m.configOptions, m.settings.configID)
	if !ok || i >= len(opt.Options) {
		return m, nil
	}
	configID, value := opt.ID, opt.Options[i].Value
	m.settings = settings{}
	return m.relayout(), m.setConfigCmd(configID, value)
}

func (m Model) setConfigCmd(configID, value string) tea.Cmd {
	client, sessionID := m.client, m.sessionID
	return func() tea.Msg {
		options, err := client.SetConfigOption(m.ctx, sessionID, configID, value)
		return configChangedMsg{configID: configID, value: value, options: options, err: err}
	}
}

// settingsView renders the picker, scrolled to keep the cursor visible.
func (m Model) settingsView() string {
	rows := m.settingsRows()
	title := m.opts.Agent + " settings"
	if m.settings.choosingValue() {
		if opt, ok := configOption(m.configOptions, m.settings.configID); ok {
			title = opt.Name
		}
	}

	start := 0
	if m.settings.cursor >= settingsWindow {
		start = m.settings.cursor - settingsWindow + 1
	}
	end := min(start+settingsWindow, len(rows))

	lines := []string{permLabelStyle.Render(title)}
	for i := start; i < end; i++ {
		mark, style := "  ", completionItemStyle
		if i == m.settings.cursor {
			mark, style = "› ", completionSelectedStyle
		}
		row := mark + rows[i].value
		if rows[i].desc != "" {
			row += "  " + rows[i].desc
		}
		lines = append(lines, style.Render(truncate(row, m.width-6)))
	}
	if len(rows) > settingsWindow {
		lines = append(lines, noticeStyle.Render(fmt.Sprintf("  %d of %d", m.settings.cursor+1, len(rows))))
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(permBoxStyle.Render(strings.Join(lines, "\n")))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ move • enter select • esc back"))
	return b.String()
}

// settingsHeight is the footer space the picker needs.
func (m Model) settingsHeight() int {
	rows := len(m.settingsRows())
	if rows > settingsWindow {
		return settingsWindow + 6 // plus the "n of N" counter
	}
	return rows + 5
}

// clientCommands are opentree's own slash commands, derived from the settings
// the agent declares: /model, /mode and /effort exist exactly where those
// settings do, and an agent declaring something else gets a command for it too.
//
// They are opentree's because the protocol has no equivalent — an agent's
// available_commands are its prompt-level skills, and opencode advertises
// thirty-five of them without a /model among them.
func (m Model) clientCommands() []acp.Command {
	advertised := make(map[string]bool, len(m.commands))
	for _, c := range m.commands {
		advertised[c.Name] = true
	}

	var out []acp.Command
	for _, o := range m.configOptions {
		// An agent that advertises the same name keeps it; its own command is
		// the more specific thing.
		if advertised[o.ID] {
			continue
		}
		desc := "change " + strings.ToLower(o.Name)
		if o.CurrentValue != "" {
			desc += " (now " + o.CurrentValue + ")"
		}
		out = append(out, acp.Command{Name: o.ID, Description: desc})
	}
	return out
}

// paletteCommands is everything the slash palette offers.
func (m Model) paletteCommands() []acp.Command {
	return append(m.clientCommands(), m.commands...)
}

// clientCommandFor reports whether typed text is one of opentree's own
// commands, so it opens a picker instead of being sent to the agent.
func (m Model) clientCommandFor(text string) (string, bool) {
	name := strings.TrimPrefix(strings.TrimSpace(text), "/")
	if name == text || name == "" {
		return "", false
	}
	for _, c := range m.clientCommands() {
		if c.Name == name {
			return name, true
		}
	}
	return "", false
}

// openSettingsAt jumps straight into one option's values, which is what
// /model is for.
func (m Model) openSettingsAt(configID string) (tea.Model, tea.Cmd) {
	opt, ok := configOption(m.configOptions, configID)
	if !ok {
		return m, nil
	}
	m.settings = settings{open: true, configID: configID}
	for j, v := range opt.Options {
		if v.Value == opt.CurrentValue {
			m.settings.cursor = j
		}
	}
	return m.relayout(), nil
}

// nextMode is the mode that follows the current one, or ok=false when the agent
// declares no mode to cycle.
func nextMode(options []acp.ConfigOption) (configID, value string, ok bool) {
	for _, o := range options {
		if o.Category != "mode" || len(o.Options) < 2 {
			continue
		}
		next := 0
		for i, v := range o.Options {
			if v.Value == o.CurrentValue {
				next = (i + 1) % len(o.Options)
			}
		}
		return o.ID, o.Options[next].Value, true
	}
	return "", "", false
}

// cycleMode advances the session mode by one, so the common flip between
// build and plan does not need the picker at all.
func (m Model) cycleMode() (tea.Model, tea.Cmd) {
	configID, value, ok := nextMode(m.configOptions)
	if !ok {
		return m, nil
	}
	return m, m.setConfigCmd(configID, value)
}

// settingsSummary is the model and mode shown in the header. It reads the
// agent's own current values, so it stays right after a change without the
// header knowing what a "model" is.
func (m Model) settingsSummary() []string {
	var out []string
	for _, o := range m.configOptions {
		// Effort and anything else the agent declares stays out of the header;
		// the two worth a permanent glance are what you are talking to and what
		// it is allowed to do.
		if o.Category == "model" || o.Category == "mode" {
			if o.CurrentValue != "" {
				out = append(out, o.CurrentValue)
			}
		}
	}
	return out
}
