package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/workspace"
	"github.com/axelgar/opentree/pkg/worktree"
)

// AgentStatus represents the signal an agent writes to .opentree-status.json in
// its worktree directory. The installed hooks only ever write "in_progress"
// (a turn started) or "needs_input" (a turn ended, or the agent hit a prompt);
// mtime is the file's modification time, i.e. when that last event happened.
type AgentStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`

	mtime time.Time // when the agent last wrote the file (from os.Stat)
}

// readAgentStatus reads the .opentree-status.json file from a worktree directory.
// Returns nil if the file is missing, unreadable, or has an invalid status value.
func readAgentStatus(worktreeDir string) *AgentStatus {
	path := filepath.Join(worktreeDir, workspace.StatusFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s AgentStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	switch s.Status {
	case "in_progress", "needs_input":
		if fi, err := os.Stat(path); err == nil {
			s.mtime = fi.ModTime()
		}
		return &s
	default:
		return nil
	}
}

// staleAfter is how long since an agent's last status event before opentree
// treats a worktree as parked (idle/stalled) rather than freshly working or
// waiting on you.
// ponytail: single global knob — raise it if agents run long silent turns.
const staleAfter = 15 * time.Minute

type agentLiveness int

const (
	livenessNone    agentLiveness = iota
	livenessWorking               // actively generating
	livenessStalled               // turn started but no recent activity — likely dead session
	livenessWaiting               // just stopped / hit a prompt — your turn
	livenessIdle                  // stopped a while ago — parked/stale
)

// liveness collapses the agent's last status event and its age into the coarse
// working-vs-stale state shown as a badge. The returned time is the reference
// instant for the "· Xh ago" age on the stale states (zero when unused). A live
// tmux pane keeps a long in-progress turn from reading as stalled.
func (ws WorkspaceItem) liveness() (agentLiveness, time.Time) {
	if ws.AgentStatus == nil {
		return livenessNone, time.Time{}
	}
	mtime := ws.AgentStatus.mtime
	statusFresh := !mtime.IsZero() && time.Since(mtime) < staleAfter
	paneFresh := !ws.LastActivity.IsZero() && time.Since(ws.LastActivity) < staleAfter
	switch ws.AgentStatus.Status {
	case "in_progress":
		if statusFresh || paneFresh {
			return livenessWorking, time.Time{}
		}
		return livenessStalled, mtime
	case "needs_input":
		if statusFresh {
			return livenessWaiting, time.Time{}
		}
		return livenessIdle, mtime
	}
	return livenessNone, time.Time{}
}

// badgeWithAge renders "label · 2h ago", falling back to just "label" when the
// reference time is unknown (zero) so the badge never trails a bare "· ".
func badgeWithAge(label string, t time.Time) string {
	if age := formatAge(t); age != "" {
		return label + " · " + age
	}
	return label
}

// cleanPreview strips ANSI codes and returns the last 5 non-empty lines.
func cleanPreview(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if trimmed := strings.TrimRight(l, " \t"); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) > 5 {
		out = out[len(out)-5:]
	}
	return strings.Join(out, "\n")
}

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

// openURLCmd opens a URL in the system default browser (fire-and-forget).
// Only opens http/https URLs to prevent command injection.
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
		_ = cmd.Start()
		return nil
	}
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
