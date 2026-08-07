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
	headerHeight = 2  // title line plus the blank beneath it
	inputHeight  = 3  // textarea rows
	diffMaxLines = 12 // per tool call, so one large edit cannot bury the log
)

func newViewport(width, height int) viewport.Model {
	return viewport.New(width, height)
}

// footerHeight is how many lines the footer occupies, which is what the
// viewport has to give back.
func (m Model) footerHeight() int {
	switch {
	case m.stopped():
		return len(m.stoppedLines()) + 4
	case m.perm != nil:
		return len(m.perm.req.Options) + 5
	default:
		return inputHeight + 2 + len(m.completion.items)
	}
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
	switch {
	case m.stopped():
		return m.stoppedView()
	case m.perm != nil:
		return m.permissionView()
	}

	var b strings.Builder
	b.WriteString("\n")
	if m.completion.active() {
		b.WriteString(m.completionView())
		b.WriteString("\n")
	}
	b.WriteString(inputBoxStyle.Render(m.input.View()))
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	return b.String()
}

// completionView lists the palette above the input, closest match first.
func (m Model) completionView() string {
	lines := make([]string, 0, len(m.completion.items))
	for i, item := range m.completion.items {
		style, mark := completionItemStyle, "  "
		if i == m.completion.cursor {
			style, mark = completionSelectedStyle, "› "
		}
		row := mark + item.value
		if item.desc != "" {
			row += "  " + item.desc
		}
		lines = append(lines, style.Render(truncate(row, m.width-2)))
	}
	return strings.Join(lines, "\n")
}

// statusLine carries the help text, or an error when the agent is still alive
// but the last turn failed — a case the stopped panel does not cover.
func (m Model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render("✕ " + m.errorText())
	}
	return helpStyle.Render(m.help.ShortHelpView(m.keys.ShortHelp()))
}

// stoppedLines describes why the agent is unusable and what to do about it.
func (m Model) stoppedLines() []string {
	lines := []string{errorStyle.Render("✕ " + m.errorText())}
	if m.authNeed {
		if hint := m.authHint(); hint != "" {
			lines = append(lines, noticeStyle.Render(hint))
		}
	}

	var actions []string
	if m.authNeed && len(m.opts.AuthCommand) > 0 {
		actions = append(actions, permKeyStyle.Render("[l]")+" "+
			permLabelStyle.Render(m.opts.Command+" "+strings.Join(m.opts.AuthCommand, " ")))
	}
	actions = append(actions,
		permKeyStyle.Render("[r]")+" "+permLabelStyle.Render("restart agent"),
		permKeyStyle.Render("[ctrl+c]")+" "+permLabelStyle.Render("quit"))
	return append(lines, strings.Join(actions, "   "))
}

func (m Model) stoppedView() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(errorBoxStyle.Render(strings.Join(m.stoppedLines(), "\n")))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(m.help.ShortHelpView(m.keys.StoppedHelp())))
	return b.String()
}

func (m Model) errorText() string {
	if m.err == nil {
		return "agent unavailable"
	}
	// Agent stderr is folded into the error; the first line carries the cause
	// and the rest would swamp a footer.
	return strings.SplitN(m.err.Error(), "\n", 2)[0]
}

// authHint surfaces the agent's own login instructions rather than opentree
// guessing at a remedy the protocol deliberately leaves to the agent.
func (m Model) authHint() string {
	for _, a := range m.authMethods {
		if a.Description != "" {
			return a.Description
		}
	}
	return ""
}

// permissionView renders the escalation. The options come from the wire, so an
// agent that offers three choices gets three rows and no phantom fourth.
func (m Model) permissionView() string {
	req := m.perm.req

	// The dialog is a fixed box, so a long command or path is trimmed rather
	// than allowed to blow the border out past the terminal edge.
	lines := []string{permLabelStyle.Render(truncate(toolLabel(req.ToolCall, m.opts.Cwd), m.width-6))}
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
		if e.kind == entryThought && m.hideThoughts {
			continue
		}
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
		return m.renderTool(e.tool, width)
	}
	return ""
}

func (m Model) renderTool(call acp.ToolCall, width int) string {
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

	row := "  " + style.Render(glyph) + " " + toolTitleStyle.Render(toolLabel(call, m.opts.Cwd))
	if added, removed := diffStat(call); added+removed > 0 {
		row += " " + diffAddStyle.Render(fmt.Sprintf("+%d", added)) +
			" " + diffRemoveStyle.Render(fmt.Sprintf("-%d", removed))
	}

	lines := []string{row}
	lines = append(lines, renderDiffs(call, width)...)
	if call.Status == acp.StatusFailed {
		if reason := toolFailure(call); reason != "" {
			lines = append(lines, toolFailedStyle.Render("    "+reason))
		}
	}
	return strings.Join(lines, "\n")
}

// renderDiffs expands a call's diff blocks into coloured lines. ACP gives the
// changed region as old and new text rather than a patch, so the region's old
// lines are the removals and its new lines the additions.
func renderDiffs(call acp.ToolCall, width int) []string {
	var out []string
	budget := diffMaxLines

	for _, c := range call.Content {
		if c.Type != "diff" {
			continue
		}
		for _, line := range splitLines(c.OldText) {
			if budget == 0 {
				return append(out, noticeStyle.Render("    … truncated"))
			}
			out = append(out, diffRemoveStyle.Render(truncate("    - "+line, width)))
			budget--
		}
		for _, line := range splitLines(c.NewText) {
			if budget == 0 {
				return append(out, noticeStyle.Render("    … truncated"))
			}
			out = append(out, diffAddStyle.Render(truncate("    + "+line, width)))
			budget--
		}
	}
	return out
}

func diffStat(call acp.ToolCall) (added, removed int) {
	for _, c := range call.Content {
		if c.Type != "diff" {
			continue
		}
		added += len(splitLines(c.NewText))
		removed += len(splitLines(c.OldText))
	}
	return added, removed
}

// toolFailure pulls the agent's explanation out of a failed call so a rejected
// permission or a failing command says why on screen.
func toolFailure(call acp.ToolCall) string {
	for _, c := range call.Content {
		if c.Type == "content" && c.Content != nil && c.Content.Text != "" {
			return strings.SplitN(strings.TrimSpace(c.Content.Text), "\n", 2)[0]
		}
	}
	return ""
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func truncate(s string, width int) string {
	if width < 4 || lipgloss.Width(s) <= width {
		return s
	}
	return string([]rune(s)[:width-1]) + "…"
}

// toolLabel is the one-line description of a call. Titles are already
// human-facing — agents rewrite "bash" into the actual command as the call
// resolves — so the kind only earns a place when the title is missing.
func toolLabel(call acp.ToolCall, cwd string) string {
	// Titles are frequently an absolute path — permission requests for an edit
	// arrive that way — so they get the same shortening as diff paths.
	label := shortPath(strings.TrimSpace(call.Title), cwd)
	if label == "" {
		label = call.Kind
	}

	var extra []string
	for _, p := range diffPaths(call, cwd) {
		// Agents rename an edit's title to the file it touched, so appending
		// the diff path would read "main.go (main.go)".
		if p != label {
			extra = append(extra, p)
		}
	}
	if len(extra) > 0 {
		return label + " (" + strings.Join(extra, ", ") + ")"
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
