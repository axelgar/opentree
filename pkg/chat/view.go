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
	switch m.overlay() {
	case overlaySettings:
		return m.settingsHeight()
	case overlaySessions:
		return m.sessionsHeight()
	case overlayStopped:
		return len(m.stoppedLines()) + 4
	case overlayPermission:
		return len(m.perm().req.Options) + len(m.permDetail()) + 5
	case overlayHelp:
		return lipgloss.Height(m.helpView())
	default:
		return inputHeight + 2 + m.completionHeight()
	}
}

// completionHeight is how many lines the palette occupies: the visible window,
// plus the line saying where in the list the cursor is.
func (m Model) completionHeight() int {
	n := min(len(m.completion.items), completionWindow)
	if len(m.completion.items) > completionWindow {
		n++
	}
	if m.awaitingCommands() {
		n++
	}
	return n
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
	switch m.overlay() {
	case overlaySettings:
		return m.settingsView()
	case overlaySessions:
		return m.sessionsView()
	case overlayStopped:
		return m.stoppedView()
	case overlayPermission:
		return m.permissionView()
	case overlayHelp:
		return m.helpView()
	}

	var b strings.Builder
	b.WriteString("\n")
	// The waiting line stands on its own when the agent's commands are all
	// there is to match — typing "/" at an agent that has sent nothing yet
	// should not look like "/" matches nothing.
	if m.completion.active() || m.awaitingCommands() {
		b.WriteString(m.completionView())
		b.WriteString("\n")
	}
	b.WriteString(inputBoxStyle.Render(m.input.View()))
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	return b.String()
}

// completionView lists the palette above the input, closest match first,
// scrolled to keep the cursor visible. opencode advertises ninety-odd commands
// and only six fit; arrowing past the sixth scrolls rather than stopping, so
// the whole list is reachable without typing a narrowing prefix.
func (m Model) completionView() string {
	items := m.completion.items
	start := 0
	if m.completion.cursor >= completionWindow {
		start = m.completion.cursor - completionWindow + 1
	}
	end := min(start+completionWindow, len(items))

	// Descriptions share one column so the eye can run straight down them. The
	// widest name in the whole list sets it, not the widest on screen, or the
	// column would jump every time the window scrolls.
	col := 0
	for _, item := range items {
		col = max(col, lipgloss.Width(item.value))
	}
	col = min(col, m.width/3)

	lines := make([]string, 0, completionWindow+1)
	for i := start; i < end; i++ {
		style, mark := completionItemStyle, "  "
		if i == m.completion.cursor {
			style, mark = completionSelectedStyle, "› "
		}
		row := mark + items[i].value
		if items[i].desc != "" {
			row += strings.Repeat(" ", max(col-lipgloss.Width(items[i].value), 0)+4) + items[i].desc
		}
		lines = append(lines, style.Render(truncate(row, m.width-2)))
	}
	if len(items) > completionWindow {
		lines = append(lines, helpStyle.Render(fmt.Sprintf("  %d of %d — ↑/↓ to scroll",
			m.completion.cursor+1, len(items))))
	}
	if m.awaitingCommands() {
		lines = append(lines, helpStyle.Render("  asking "+m.opts.Agent+" for its own commands…"))
	}
	return strings.Join(lines, "\n")
}

// awaitingCommands is whether the open palette is still short of the agent's
// commands — they only arrive as a session update, after the session opens, so
// a palette opened before then is a partial answer that should say so.
//
// ponytail: an empty list is the only signal there is. An agent that genuinely
// advertises none keeps the waiting line; none of the six do.
func (m Model) awaitingCommands() bool {
	return len(m.commands) == 0 && m.completion.kind == completionCommand
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
	// A restart already under way says so instead of offering itself again: the
	// panel is otherwise identical before and after the key, which is what made
	// pressing it twice look like the first press had not registered.
	if m.restarting {
		actions = append(actions, noticeStyle.Render("restarting…"))
	} else {
		actions = append(actions, permKeyStyle.Render("[r]")+" "+permLabelStyle.Render("restart agent"))
	}
	actions = append(actions, permKeyStyle.Render("[ctrl+c]")+" "+permLabelStyle.Render("back to opentree"))
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
	req := m.perm().req

	// The dialog is a fixed box, so a long command or path is trimmed rather
	// than allowed to blow the border out past the terminal edge.
	lines := []string{permLabelStyle.Render(truncate(toolLabel(req.ToolCall, m.opts.Cwd), m.width-6))}
	lines = append(lines, m.permDetail()...)
	for i, o := range req.Options {
		lines = append(lines, fmt.Sprintf("%s %s",
			permKeyStyle.Render("["+optionHint(req.Options, i)+"]"),
			permLabelStyle.Render(o.Name)))
	}

	// Anything queued behind this one is said on the hint line rather than in
	// the box, which would change the footer's height as escalations arrive and
	// resize the conversation under the reader.
	hint := "permission needed · esc to cancel"
	if waiting := len(m.perms) - 1; waiting > 0 {
		hint = fmt.Sprintf("permission needed · %d more waiting · esc to cancel", waiting)
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(permBoxStyle.Render(strings.Join(lines, "\n")))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(hint))
	return b.String()
}

// permDetail is what the call would actually do. A title on its own is not
// enough to decide on: the Claude Code adapter asks "Ready to code?" with the
// whole plan in a content block, and an edit's diff arrives the same way — so
// approving meant approving something never shown. The request carries the
// same blocks a finished call does, and they render the same way here.
func (m Model) permDetail() []string {
	// The box sits over the conversation, so this is capped tighter than a
	// tool row: a hundred-line diff would push the transcript off the screen to
	// ask one yes/no question.
	const maxLines = 8

	call, width := m.perm().req.ToolCall, m.width-6
	detail := renderDiffs(call, width)
	detail = append(detail, renderOutput(call, width, toolOutputStyle)...)
	if len(detail) <= maxLines {
		return detail
	}
	// Both renderers have their own budget and may have added a marker already,
	// so this one does not try to count what is left — only to say that the box
	// is not the whole story.
	return append(detail[:maxLines], noticeStyle.Render("    …"))
}

// optionHint is the key that selects an option: a stable letter for the kinds
// users answer by reflex, and the position for anything else.
//
// The letter only belongs to the first option of its kind. Agents do offer two
// ways to allow always — the Claude Code adapter's plan dialog offers "yes" and
// "yes, and auto-accept edits" — and labelling both [A] pointed both rows at
// the first, which is the more permissive one.
func optionHint(options []acp.PermissionOption, i int) string {
	key := kindKey(options[i].Kind)
	if key == "" {
		return strconv.Itoa(i + 1)
	}
	for j := range i {
		if options[j].Kind == options[i].Kind {
			return strconv.Itoa(i + 1)
		}
	}
	return key
}

// kindKey is the reflex key for a permission kind, empty for a kind with none.
func kindKey(kind string) string {
	switch kind {
	case acp.PermissionAllowOnce:
		return "a"
	case acp.PermissionAllowAlways:
		return "A"
	case acp.PermissionRejectOnce:
		return "d"
	}
	return ""
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
	case entryPlan:
		return renderPlan(e.plan, width)
	}
	return ""
}

// renderPlan draws the agent's plan as the checklist it is. Only agents that
// send one get this — opencode never does.
func renderPlan(entries []acp.PlanEntry, width int) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		glyph, style := "☐", toolTitleStyle
		switch e.Status {
		case acp.PlanCompleted:
			glyph, style = "☑", toolDoneStyle
		case acp.PlanInProgress:
			glyph, style = "▸", toolRunningStyle
		}
		lines = append(lines, style.Render(truncate("  "+glyph+" "+e.Content, width)))
	}
	return strings.Join(lines, "\n")
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
		if c.Type != "content" || c.Content == nil {
			continue
		}
		// Through blockText, so a tool that returns a screenshot says so rather
		// than finishing with nothing underneath it.
		if text := blockText(*c.Content); text != "" {
			parts = append(parts, strings.TrimRight(text, "\n"))
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

// renderDiffs expands a call's changed lines into coloured ones.
func renderDiffs(call acp.ToolCall, width int) []string {
	changes := callDiff(call)
	out := make([]string, 0, len(changes))
	for i, ch := range changes {
		if i == diffMaxLines {
			return append(out, noticeStyle.Render(fmt.Sprintf("    … %d more lines", len(changes)-i)))
		}
		style, sign := diffRemoveStyle, "-"
		if ch.add {
			style, sign = diffAddStyle, "+"
		}
		out = append(out, style.Render(truncate("    "+sign+" "+ch.text, width)))
	}
	return out
}

func diffStat(call acp.ToolCall) (added, removed int) {
	for _, ch := range callDiff(call) {
		if ch.add {
			added++
		} else {
			removed++
		}
	}
	return added, removed
}

// change is one line the agent added or removed.
type change struct {
	add  bool
	text string
}

// callDiff is every changed line across a call's diff blocks.
func callDiff(call acp.ToolCall) []change {
	var out []change
	for _, c := range call.Content {
		if c.Type != "diff" {
			continue
		}
		out = append(out, diffLines(splitLines(c.OldText), splitLines(c.NewText))...)
	}
	return out
}

// maxDiffCells bounds the matching table. Past it the region already dwarfs the
// dozen lines that will be shown, so exact matching stops paying for itself.
//
// ponytail: the fallback is the old behaviour — the whole region reported as
// changed. It overstates a big edit, which is the harmless direction.
const maxDiffCells = 1 << 16

// diffLines matches two versions of a region line by line.
//
// ACP hands over the before and after text rather than a patch, so the client
// does the diffing. Not doing it at all was a lie in both directions: a
// one-line insertion into a two-line region reported "+3 -2", and the Claude
// Code adapter pads every hunk with context on both sides, so a single changed
// line rendered as seven removals and seven additions and blew the display
// budget before reaching the change.
func diffLines(old, updated []string) []change {
	// Lines shared at either end are context the agent did not touch.
	for len(old) > 0 && len(updated) > 0 && old[0] == updated[0] {
		old, updated = old[1:], updated[1:]
	}
	for len(old) > 0 && len(updated) > 0 && old[len(old)-1] == updated[len(updated)-1] {
		old, updated = old[:len(old)-1], updated[:len(updated)-1]
	}
	if len(old)*len(updated) > maxDiffCells {
		return append(changes(false, old), changes(true, updated)...)
	}

	// common[i][j] is the length of the longest common subsequence of old[i:]
	// and updated[j:], which is what says whether a line was replaced or merely
	// moved past.
	common := make([][]int, len(old)+1)
	for i := range common {
		common[i] = make([]int, len(updated)+1)
	}
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(updated) - 1; j >= 0; j-- {
			if old[i] == updated[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}

	var out []change
	i, j := 0, 0
	for i < len(old) && j < len(updated) {
		switch {
		case old[i] == updated[j]:
			// Unchanged, and unchanged lines are not what the row is reporting.
			i, j = i+1, j+1
		case common[i+1][j] >= common[i][j+1]:
			out = append(out, change{text: old[i]})
			i++
		default:
			out = append(out, change{add: true, text: updated[j]})
			j++
		}
	}
	out = append(out, changes(false, old[i:])...)
	return append(out, changes(true, updated[j:])...)
}

func changes(add bool, lines []string) []change {
	out := make([]change, len(lines))
	for i, l := range lines {
		out[i] = change{add: add, text: l}
	}
	return out
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
