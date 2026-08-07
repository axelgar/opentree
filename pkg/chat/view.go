package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/acp"
)

const (
	headerHeight = 2 // title line plus the blank beneath it
	inputHeight  = 3 // textarea rows
)

func newViewport(width, height int) viewport.Model {
	return viewport.New(width, height)
}

// footerHeight is how many lines the footer occupies, which is what the
// viewport has to give back. A permission dialog is taller than the input box
// by however many options the agent offered.
func (m Model) footerHeight() int {
	if m.perm != nil {
		return len(m.perm.req.Options) + 5
	}
	return inputHeight + 2
}

func (m Model) View() string {
	if !m.ready {
		return "starting agent…"
	}

	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m Model) header() string {
	left := headerStyle.Render(fmt.Sprintf("%s · %s", m.opts.Workspace, m.opts.Agent))

	var meta []string
	if m.modelName != "" {
		meta = append(meta, m.modelName)
	}
	if m.usage != nil {
		if m.usage.Size > 0 {
			meta = append(meta, fmt.Sprintf("%d%% ctx", m.usage.Used*100/m.usage.Size))
		}
		if m.usage.Cost != nil {
			meta = append(meta, fmt.Sprintf("$%.4f", m.usage.Cost.Amount))
		}
	}
	right := metaStyle.Render(strings.Join(meta, " · "))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) footer() string {
	if m.perm != nil {
		return m.permissionView()
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(inputBoxStyle.Render(m.input.View()))
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	return b.String()
}

func (m Model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render("✕ " + m.err.Error())
	}
	return helpStyle.Render(m.help.ShortHelpView(m.keys.ShortHelp()))
}

// permissionView renders the escalation. The options come from the wire, so an
// agent that offers three choices gets three rows and no phantom fourth.
func (m Model) permissionView() string {
	req := m.perm.req

	lines := []string{permLabelStyle.Render(toolLabel(req.ToolCall, m.opts.Cwd))}
	for i, o := range req.Options {
		lines = append(lines, fmt.Sprintf("%s %s",
			permKeyStyle.Render("["+optionHint(o, i)+"]"),
			permLabelStyle.Render(o.Name)))
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(permBoxStyle.Render(strings.Join(lines, "\n")))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("permission needed · esc to cancel"))
	return b.String()
}

// optionHint is the key that selects an option: a stable letter for the kinds
// users answer by reflex, and the position for anything else.
func optionHint(o acp.PermissionOption, i int) string {
	switch o.Kind {
	case acp.PermissionAllowOnce:
		return "a"
	case acp.PermissionAllowAlways:
		return "A"
	case acp.PermissionRejectOnce:
		return "d"
	}
	return strconv.Itoa(i + 1)
}

func (m Model) renderLog() string {
	width := m.width - 2
	if width < 20 {
		width = 20
	}

	var b strings.Builder
	for _, e := range m.entries {
		b.WriteString(m.renderEntry(e, width))
		b.WriteString("\n")
	}
	if m.turn {
		b.WriteString(toolRunningStyle.Render(spinnerFrames[m.spinnerFrame] + " thinking…"))
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) renderEntry(e entry, width int) string {
	wrap := lipgloss.NewStyle().Width(width)

	switch e.kind {
	case entryUser:
		return "\n" + promptMarkStyle.Render("› ") + wrap.Inherit(userTextStyle).Render(e.text)
	case entryAgent:
		return wrap.Inherit(agentTextStyle).Render(e.text)
	case entryThought:
		return wrap.Inherit(thoughtStyle).Render(e.text)
	case entryNotice:
		return noticeStyle.Render("  " + e.text)
	case entryTool:
		return m.renderTool(e.tool)
	}
	return ""
}

func (m Model) renderTool(call acp.ToolCall) string {
	var glyph string
	var style lipgloss.Style
	switch call.Status {
	case acp.StatusCompleted:
		glyph, style = "✓", toolDoneStyle
	case acp.StatusFailed:
		glyph, style = "✗", toolFailedStyle
	default:
		glyph, style = spinnerFrames[m.spinnerFrame], toolRunningStyle
	}
	return "  " + style.Render(glyph) + " " + toolTitleStyle.Render(toolLabel(call, m.opts.Cwd))
}

// toolLabel is the one-line description of a call. Titles are already
// human-facing — agents rewrite "bash" into the actual command as the call
// resolves — so the kind only earns a place when the title is missing.
func toolLabel(call acp.ToolCall, cwd string) string {
	label := call.Title
	if label == "" {
		label = call.Kind
	}
	if paths := diffPaths(call, cwd); len(paths) > 0 {
		return label + " (" + strings.Join(paths, ", ") + ")"
	}
	return label
}

func diffPaths(call acp.ToolCall, cwd string) []string {
	var paths []string
	for _, c := range call.Content {
		if c.Type == "diff" && c.Path != "" {
			paths = append(paths, shortPath(c.Path, cwd))
		}
	}
	return paths
}

// shortPath trims the worktree prefix so a row reads pkg/auth/session.go rather
// than an absolute path that pushes everything else off the line.
func shortPath(path, cwd string) string {
	if cwd == "" {
		return path
	}
	if rel := strings.TrimPrefix(path, cwd+"/"); rel != path {
		return rel
	}
	return path
}
