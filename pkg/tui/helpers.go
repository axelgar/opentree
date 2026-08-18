package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/worktree"
)

// renderFileChanges builds the per-file changes panel content.
func (m Model) renderFileChanges(files []worktree.FileChange, width int) string {
	var sb strings.Builder

	uncommittedCount := 0
	for _, f := range files {
		if f.Uncommitted {
			uncommittedCount++
		}
	}

	title := fmt.Sprintf("Changed files (%d)", len(files))
	if uncommittedCount > 0 {
		title += uncommittedFileStyle.Render(fmt.Sprintf(" · %d uncommitted", uncommittedCount))
	}
	sb.WriteString(fileChangesTitleStyle.Render(title))
	sb.WriteString("\n")

	maxName := 0
	for _, f := range files {
		name := shortenPath(f.FileName, width-24)
		if len(name) > maxName {
			maxName = len(name)
		}
	}

	for _, f := range files {
		name := shortenPath(f.FileName, width-24)
		padding := strings.Repeat(" ", maxName-len(name)+2)

		addStr := fileAddedStyle.Render(fmt.Sprintf("+%d", f.Added))
		remStr := fileRemovedStyle.Render(fmt.Sprintf("-%d", f.Removed))

		marker := "  "
		if f.Uncommitted {
			marker = uncommittedFileStyle.Render("● ")
		}

		fmt.Fprintf(&sb, " %s%s%s%s %s\n", marker, fileNameStyle.Render(name), padding, addStr, remStr)
	}

	if uncommittedCount > 0 {
		sb.WriteString(uncommittedFileStyle.Render(" ● = uncommitted"))
		sb.WriteString("\n")
	}

	return sb.String()
}

// shortenPath truncates a file path from the left, keeping the filename and nearest directories.
func shortenPath(path string, maxLen int) string {
	if len(path) <= maxLen || maxLen <= 0 {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 1 {
		return path
	}
	result := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		candidate := parts[i] + "/" + result
		if len(candidate)+4 > maxLen {
			return ".../" + result
		}
		result = candidate
	}
	return result
}

// actionHint is the one-line "what this row can do right now" shown under the
// selected workspace. First match wins: the point is the next action, not a
// catalogue of every key that would work. The merged cleanup hint that used
// to be the only state with a hint is now one case among several. Every case
// speaks in one voice — key, then what it does — so the line reads the same
// wherever the row happens to be.
func (ws WorkspaceItem) actionHint() string {
	switch {
	case ws.pendingPermission() != nil:
		return "a answer permission • m message • c interrupt"
	case ws.ChatStatus != nil && ws.ChatStatus.State == chat.StateStopped:
		return "enter attach and restart the stopped agent"
	case ws.PRStatus == "merged":
		return "x clean up this merged workspace"
	case ws.PRStatus == "open":
		return "o open PR • R send reviews • m message"
	case ws.UncommittedCount > 0 || (ws.DiffStat != "" && ws.DiffStat != "No changes"):
		return "p create PR • d diff • m message"
	}
	return ""
}

// deletionLosses is one line per workspace the delete dialog is about, naming
// the branch that goes with it and what is on it that is not anywhere else.
//
// Every number here has already been loaded for the row behind the dialog —
// DiffStat and FileChanges by the refresh, UncommittedCount beside them,
// BranchPushed by the 30s status poll — so this costs no git calls and nothing
// async. Which matters: the TUI is the only delete path that never calls
// HasChanges, so it was the one asking "are you sure?" while showing the least.
//
// Workspaces with nothing to lose contribute no line rather than a reassuring
// one. A dialog that says "clean" four times teaches people to press y.
func (m Model) deletionLosses() []string {
	targets := []string{m.deleteTarget}
	if m.deleteTarget == "" {
		targets = targets[:0]
		for name := range m.selected {
			targets = append(targets, name)
		}
		sort.Strings(targets)
	}

	var lines []string
	for _, name := range targets {
		i := m.workspaceIndex(name)
		if i < 0 {
			continue
		}
		ws := m.workspaces[i]

		var loss []string
		if ws.UncommittedCount > 0 {
			loss = append(loss, dangerStyle.Render(plural(ws.UncommittedCount, "uncommitted file")))
		}
		if len(ws.FileChanges) > 0 {
			loss = append(loss, ws.renderDiffStat())
		}
		if !ws.BranchPushed {
			loss = append(loss, dangerStyle.Render("never pushed"))
		}
		if len(loss) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s",
			confirmLabelStyle.Render(ws.Branch), strings.Join(loss, confirmLabelStyle.Render(" · "))))
	}
	return lines
}

// renderDiffStat is the change summary in a row's detail line. The raw git
// --stat sentence (" 3 files changed, 6 insertions(+)") reads as one more grey
// word in a grey line; counts in the palette's add/remove colours are scannable
// instead. "clean" replaces "No changes": same fact, less noise.
func (ws WorkspaceItem) renderDiffStat() string {
	if ws.DiffStat == "diff unavailable" {
		return warnStyle.Render("diff unavailable")
	}
	if len(ws.FileChanges) == 0 {
		return "clean"
	}
	added, removed := 0, 0
	for _, f := range ws.FileChanges {
		added += f.Added
		removed += f.Removed
	}
	return fmt.Sprintf("%s · %s %s",
		plural(len(ws.FileChanges), "file"),
		diffAddStyle.Render(fmt.Sprintf("+%d", added)),
		diffRemoveStyle.Render(fmt.Sprintf("-%d", removed)))
}

// renderDiffLine colorizes a single line of unified diff output.
func renderDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "══"):
		return diffSectionStyle.Render(line)
	case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
		return diffFileStyle.Render(line)
	case strings.HasPrefix(line, "@@"):
		return diffHunkStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return diffAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return diffRemoveStyle.Render(line)
	default:
		return line
	}
}

// countUncommitted counts files with uncommitted changes in a worktree.
func countUncommitted(worktreePath string) int {
	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// cmdStart and cmdWait are variables so tests can stub the actual exec.
// cmdWait is read on the goroutine that starts the opener rather than inside
// the reaper it spawns, so a test restoring the stub cannot race a reaper it
// left running.
var (
	cmdStart = func(c *exec.Cmd) error { return c.Start() }
	cmdWait  = func(c *exec.Cmd) error { return c.Wait() }
)

// openURLCmd opens a URL in the system default browser.
// Only opens http/https URLs to prevent command injection.
//
// The result comes back as a message — browserOpenedMsg or errMsg — because a
// key that appears to do nothing is indistinguishable from a broken one, and
// "nothing happened" used to be the only answer both on success and on a
// missing opener.
func openURLCmd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
			return nil
		}
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", rawURL)
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", rawURL)
		default:
			cmd = exec.Command("xdg-open", rawURL)
		}
		if err := cmdStart(cmd); err != nil {
			return errMsg{fmt.Errorf("could not open browser: %w", err)}
		}
		// Reap the opener. open and xdg-open hand the URL to the desktop and
		// exit within milliseconds, but nobody was collecting them, so each
		// press of o left a zombie behind for as long as the dashboard ran —
		// an afternoon spent opening pull requests is dozens of them.
		// Process.Release is not the alternative it looks like: on Unix it
		// drops the handle without waiting, which is how the zombie is made
		// in the first place.
		reap := cmdWait
		go func() { _ = reap(cmd) }()
		return browserOpenedMsg{url: rawURL}
	}
}

// workspaceIndex is the row for a name, or -1. Commands finish long after the
// list was built and arrive carrying only the name they were started with.
func (m Model) workspaceIndex(name string) int {
	for i, ws := range m.workspaces {
		if ws.Name == name {
			return i
		}
	}
	return -1
}

// formatAge returns a human-readable age string for a given timestamp, or ""
// for the zero time (unknown) so callers never render a bogus multi-century age.
func formatAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// renderLogo returns the opencode-style block-pixel logo for opentree.
// The logo uses raw ANSI escape codes to render two panels: "open" (dim left)
// and "tree" (bright right), matching the opencode CLI colour scheme.
func renderLogo() string {
	const reset = "\x1b[0m"
	type panel struct{ fg, shadow, bg string }
	left := panel{
		fg:     "\x1b[90m",
		shadow: "\x1b[38;5;235m",
		bg:     "\x1b[48;5;235m",
	}
	right := panel{
		fg:     reset,
		shadow: "\x1b[38;5;238m",
		bg:     "\x1b[48;5;238m",
	}
	drawLine := func(line, fg, shadow, bg string) string {
		var b strings.Builder
		for _, ch := range line {
			switch ch {
			case '_':
				b.WriteString(bg + " " + reset)
			case '^':
				b.WriteString(fg + bg + "\u2580" + reset)
			case '~':
				b.WriteString(shadow + "\u2580" + reset)
			case ' ':
				b.WriteRune(' ')
			default:
				b.WriteString(fg + string(ch) + reset)
			}
		}
		return b.String()
	}
	glyphsLeft := []string{
		"                   ",
		"\u2588\u2580\u2580\u2588 \u2588\u2580\u2580\u2588 \u2588\u2580\u2580\u2588 \u2588\u2580\u2580\u2584",
		"\u2588__\u2588 \u2588__\u2588 \u2588^^^ \u2588__\u2588",
		"\u2580\u2580\u2580\u2580 \u2588\u2580\u2580\u2580 \u2580\u2580\u2580\u2580 \u2580~~\u2580",
	}
	glyphsRight := []string{
		" \u2584               ",
		"\u2580\u2588\u2580\u2580 \u2588\u2580\u2580\u2584 \u2588\u2580\u2580\u2588 \u2588\u2580\u2580\u2588",
		"_\u2588__ \u2588^^^ \u2588^^^ \u2588^^^",
		"_\u2580\u2580\u2580 \u2580    \u2580\u2580\u2580\u2580 \u2580\u2580\u2580\u2580",
	}
	var sb strings.Builder
	for i, row := range glyphsLeft {
		other := ""
		if i < len(glyphsRight) {
			other = glyphsRight[i]
		}
		sb.WriteString(drawLine(row, left.fg, left.shadow, left.bg))
		sb.WriteString(" ")
		sb.WriteString(drawLine(other, right.fg, right.shadow, right.bg))
		if i < len(glyphsLeft)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
