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
