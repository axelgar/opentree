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

// AgentStatus represents the completion signal an agent writes to
// .opentree-status.json in its worktree directory.
type AgentStatus struct {
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// readAgentStatus reads the .opentree-status.json file from a worktree directory.
// Returns nil if the file is missing, unreadable, or has an invalid status value.
func readAgentStatus(worktreeDir string) *AgentStatus {
	data, err := os.ReadFile(filepath.Join(worktreeDir, workspace.StatusFileName))
	if err != nil {
		return nil
	}
	var s AgentStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	switch s.Status {
	case "success", "failure", "error", "in_progress":
		return &s
	default:
		return nil
	}
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

		sb.WriteString(fmt.Sprintf(" %s%s%s%s %s\n", marker, fileNameStyle.Render(name), padding, addStr, remStr))
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

// keyBytesMap maps Bubble Tea key types to their raw PTY byte sequences.
var keyBytesMap = map[tea.KeyType][]byte{
	tea.KeyEnter:     {'\r'},
	tea.KeyBackspace: {0x7f},
	tea.KeyTab:       {'\t'},
	tea.KeyShiftTab:  []byte("\x1b[Z"),
	tea.KeySpace:     {' '},
	tea.KeyEscape:    {0x1b},
	tea.KeyCtrlC:     {0x03},
	tea.KeyCtrlD:     {0x04},
	tea.KeyCtrlZ:     {0x1a},
	tea.KeyCtrlL:     {0x0c},
	tea.KeyCtrlA:     {0x01},
	tea.KeyCtrlE:     {0x05},
	tea.KeyCtrlU:     {0x15},
	tea.KeyCtrlK:     {0x0b},
	tea.KeyCtrlW:     {0x17},
	tea.KeyUp:        []byte("\x1b[A"),
	tea.KeyDown:      []byte("\x1b[B"),
	tea.KeyRight:     []byte("\x1b[C"),
	tea.KeyLeft:      []byte("\x1b[D"),
	tea.KeyHome:      []byte("\x1b[H"),
	tea.KeyEnd:       []byte("\x1b[F"),
	tea.KeyPgUp:      []byte("\x1b[5~"),
	tea.KeyPgDown:    []byte("\x1b[6~"),
	tea.KeyDelete:    []byte("\x1b[3~"),
}

// keyToBytes converts a Bubble Tea key message to raw bytes for PTY input.
func keyToBytes(msg tea.KeyMsg) []byte {
	if msg.Type == tea.KeyRunes {
		return []byte(string(msg.Runes))
	}
	return keyBytesMap[msg.Type]
}

// formatAge returns a human-readable age string for a given timestamp.
func formatAge(t time.Time) string {
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
