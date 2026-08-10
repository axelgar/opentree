package chat

import (
	"fmt"
	"os"
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

	// outputMaxLines caps what one tool prints into the log, for the same
	// reason: a build log runs to thousands of lines and the conversation has
	// to stay readable around it.
	outputMaxLines = 8

	// compactLogoWidth separates a mark, which can stand beside the agent's
	// name, from a wordmark, which cannot.
	compactLogoWidth = 12
)

func newViewport(width, height int) viewport.Model {
	return viewport.New(width, height)
}

// footerHeight is how many lines the footer occupies, which is what the
// viewport has to give back.
func (m Model) footerHeight() int {
	switch {
	case m.settings.open:
		return m.settingsHeight()
	case m.stopped():
		return len(m.stoppedLines()) + 4
	case m.perm != nil:
		return len(m.perm.req.Options) + 5
	case m.showHelp:
		return lipgloss.Height(m.helpView())
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
	// The workspace keeps the banner — it is what distinguishes this window
	// from the one next to it — and the agent sits beside it in its own colour
	// rather than inside the banner, where a second colour would fight it.
	b := m.brand()
	left := headerStyle.Render(m.opts.Workspace) + " " +
		b.paint(lipgloss.NewStyle()).Render(b.mark+" "+b.name)

	meta := m.settingsSummary()
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
	case m.settings.open:
		return m.settingsView()
	case m.stopped():
		return m.stoppedView()
	case m.perm != nil:
		return m.permissionView()
	case m.showHelp:
		return m.helpView()
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
// but the last turn failed — a case the stopped panel does not cover. The
// agent's live flags sit right-aligned against it.
func (m Model) statusLine() string {
	flags := m.flagsSummary()
	if len(flags) == 0 {
		if m.err != nil {
			return errorStyle.Render("✕ " + m.errorText())
		}
		return helpStyle.Render(m.help.ShortHelpView(m.keys.ShortHelp()))
	}

	// The flags are the point of the line; the help gives way when the terminal
	// is too narrow for both.
	right := flagStyle.Render(strings.Join(flags, " · ")) +
		helpStyle.Render("  shift+tab")
	room := max(m.width-lipgloss.Width(right)-2, 0)

	left := m.shortHelp(room)
	if m.err != nil {
		left = errorStyle.Render(truncate("✕ "+m.errorText(), room))
	}

	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// shortHelp renders as many bindings as fit, dropping whole ones from the end.
// Cutting the rendered line instead leaves a dangling "•" hanging off the last
// binding that survived, which reads as a rendering fault rather than as the
// deliberate omission it is.
func (m Model) shortHelp(room int) string {
	bindings := m.keys.ShortHelp()
	for len(bindings) > 0 {
		line := helpStyle.Render(m.help.ShortHelpView(bindings))
		if lipgloss.Width(line) <= room {
			return line
		}
		bindings = bindings[:len(bindings)-1]
	}
	return ""
}

// stoppedLines describes why the agent is unusable and what to do about it.
func (m Model) stoppedLines() []string {
	// Trimmed to the box: an exec failure names a path and blows the border
	// past the terminal edge otherwise.
	lines := []string{errorStyle.Render(truncate("✕ "+m.errorText(), m.width-6))}
	if m.authNeed {
		if hint := m.authHint(); hint != "" {
			lines = append(lines, noticeStyle.Render(hint))
		}
	}

	var actions []string
	if m.opts.InstallHint != "" && m.adapterMissing() {
		lines = append(lines, noticeStyle.Render(m.opts.InstallHint))
	}
	if m.canLogIn() {
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
	b.WriteString(helpStyle.Render(m.help.ShortHelpView(m.keys.StoppedHelp(m.canLogIn()))))
	return b.String()
}

// helpView is the whole key list, shown in place of the input. It exists
// because the status line has room for five bindings and the chat has twelve.
func (m Model) helpView() string {
	h := m.help
	h.ShowAll = true
	if m.width > 4 {
		h.Width = m.width - 4
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(permBoxStyle.Render(h.FullHelpView(m.keys.FullHelp())))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("any key to close"))
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
	if !m.conversationStarted() && !m.turn {
		b.WriteString(m.emptyState())
	}
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

// emptyState orients someone who has just landed in a chat they did not set
// up: what they are talking to, and the things typing alone will not reveal.
// conversationStarted reports whether anyone has said anything yet. Notices
// are opentree talking to itself — a failed resume is the common one, and it
// arrives before the first word, exactly when the hint is still wanted.
func (m Model) conversationStarted() bool {
	for _, e := range m.entries {
		if e.kind != entryNotice {
			return true
		}
	}
	return false
}

// The agent's version is deliberately absent. ACP reports the version of
// whatever serves the protocol, which for Claude Code is the adapter — so
// "Claude Code 0.66.0" would name a release of Claude Code that does not exist.
func (m Model) emptyState() string {
	// Logo left, three lines of identity right: what you are talking to, where
	// it is standing, and which worktree that is. Someone attaching to a window
	// they did not open learns all three without typing.
	b := m.brand()
	logo := b.paint(logoStyle).Render(strings.Join(b.logo, "\n"))
	who := strings.Join([]string{
		b.paint(agentNameStyle).Render(b.name),
		cwdStyle.Render(m.opts.Workspace),
		cwdStyle.Render(shortHome(m.opts.Cwd)),
	}, "\n")

	// A compact mark stands beside the identity, the way Claude Code's own
	// banner does. A wordmark takes its own line and lets the identity sit
	// underneath — which is how opencode shows its own, and it would read
	// oddly beside the name it already spells.
	banner := lipgloss.JoinVertical(lipgloss.Left, logo, "", who)
	if w := lipgloss.Width(logo); w <= compactLogoWidth && w+3+lipgloss.Width(who) <= m.width {
		banner = lipgloss.JoinHorizontal(lipgloss.Top, logo, "   ", who)
	}

	hints := []string{
		hintLine("/", "the agent's own commands"),
		hintLine("@", "point it at a file in this worktree"),
		hintLine("?", "every key"),
	}
	// Trailing blank line: a notice can follow the hint, and the two run
	// together otherwise.
	return "\n" + banner + "\n\n" + strings.Join(hints, "\n") + "\n\n"
}

// shortHome keeps the worktree path readable by trimming the part of it that is
// the same for everything the user owns.
func shortHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel := strings.TrimPrefix(path, home); rel != path {
			return "~" + rel
		}
	}
	return path
}

func hintLine(sigil, desc string) string {
	return "  " + permKeyStyle.Render(sigil) + "  " + toolTitleStyle.Render(desc)
}

func (m Model) renderEntry(e entry, width int) string {
	wrap := lipgloss.NewStyle().Width(width)

	switch e.kind {
	case entryUser:
		// Width is set on the box so the band runs the full column rather than
		// stopping at the end of the text, where it would read as a highlight.
		// Less one for the border, which lipgloss adds outside the width.
		return "\n" + userBoxStyle.Width(width-1).Render(e.text) + "\n"
	case entryAgent:
		return m.bulleted(wrap.Width(width - 2).Inherit(agentTextStyle).Render(e.text))
	case entryThought:
		return wrap.Inherit(thoughtStyle).Render(e.text)
	case entryNotice:
		return noticeStyle.Render("  " + e.text)
	case entryTool:
		return m.renderTool(e.tool, width)
	}
	return ""
}

// bulleted hangs a coloured mark off the agent's first line and indents the
// rest to clear it, so a wrapped paragraph stays one visual block instead of
// dissolving into the tool rows around it.
func (m Model) bulleted(body string) string {
	b := m.brand()
	lines := strings.Split(body, "\n")
	out := b.paint(agentMarkStyle).Render(b.mark) + " " + lines[0]
	for _, l := range lines[1:] {
		out += "\n  " + l
	}
	return out
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
	diffs := renderDiffs(call, width)
	lines = append(lines, diffs...)

	// ponytail: a call that drew a diff has already shown what it did, and its
	// content block is a receipt — opencode sends "Edit applied successfully."
	// beside the diff of the edit it applied. Printing both says it twice.
	if len(diffs) == 0 {
		out := toolOutputStyle
		if call.Status == acp.StatusFailed {
			out = toolFailedStyle
		}
		lines = append(lines, renderOutput(call, width, out)...)
	}
	return strings.Join(lines, "\n")
}

// renderOutput shows what a tool produced. Agents put it in content blocks on
// every status, not only on failures: a command's stdout, a file preview, a
// subagent's report all arrive this way.
//
// ponytail: capped, with no key to expand. Expanding wants a cursor in a
// viewport that has none plus per-entry state; the count says how much was
// held back, and the agent still has all of it.
func renderOutput(call acp.ToolCall, width int, style lipgloss.Style) []string {
	lines := splitLines(unfence(toolOutput(call)))
	if len(lines) == 0 {
		return nil
	}

	shown := lines
	if len(shown) > outputMaxLines {
		shown = shown[:outputMaxLines]
	}
	out := make([]string, 0, len(shown)+1)
	for _, line := range shown {
		out = append(out, style.Render(truncate("    "+line, width)))
	}
	if hidden := len(lines) - len(shown); hidden > 0 {
		out = append(out, noticeStyle.Render(fmt.Sprintf("    … %d more lines", hidden)))
	}
	return out
}

// toolOutput joins a call's content blocks. The wrapping is real: a text block
// arrives as {"type":"content","content":{"type":"text","text":"…"}}.
func toolOutput(call acp.ToolCall) string {
	var parts []string
	for _, c := range call.Content {
		if c.Type == "content" && c.Content != nil && c.Content.Text != "" {
			parts = append(parts, strings.TrimRight(c.Content.Text, "\n"))
		}
	}
	return strings.Join(parts, "\n")
}

// unfence strips a markdown code fence wrapping the whole block. The Claude
// Code adapter fences command output whenever the client has not asked for
// streamed terminals, which opentree never does — so without this the first
// line a failed command shows is "```console" and the reason itself is what
// gets cut.
func unfence(s string) string {
	lines := splitLines(s)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "```") {
		return s
	}
	if last := len(lines) - 1; strings.TrimSpace(lines[last]) == "```" {
		lines = lines[:last]
	}
	return strings.Join(lines[1:], "\n")
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
