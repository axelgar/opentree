package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/axelgar/opentree/pkg/acp"
)

// transcriptMarkdown is the conversation as a document: what was said, what
// was run and what it printed, in markdown that reads the same in a file, a
// pull request, or an issue. It is what "whole conversation" copies and what
// /export writes, and it is built from the entries rather than from the
// rendered log so the code in it is code again — no bullet, no wrap, no
// colour.
func (m Model) transcriptMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", m.opts.Workspace, m.opts.Agent.Name)
	if m.opts.Cwd != "" {
		fmt.Fprintf(&b, "_%s · %s_\n\n", shortHome(m.opts.Cwd), time.Now().Format("2006-01-02 15:04"))
	}
	for _, e := range m.entries {
		b.WriteString(transcriptEntry(e, m.opts.Agent.Name, m.opts.Cwd))
	}
	return b.String()
}

// transcriptEntry is one entry as a section of the document. Each kind keeps
// the shape it had on screen: a reply is the markdown it always was, a tool
// row is its label with what it did fenced underneath, a thought is quoted.
func transcriptEntry(e entry, agent, cwd string) string {
	switch e.kind {
	case entryUser:
		return "## You\n\n" + strings.TrimRight(e.text, "\n") + "\n\n"
	case entryAgent:
		return "## " + agent + "\n\n" + strings.TrimRight(e.text, "\n") + "\n\n"
	case entryThought:
		return "> _" + strings.ReplaceAll(strings.TrimRight(e.text, "\n"), "\n", "_\n> _") + "_\n\n"
	case entryNotice:
		return "_" + strings.ReplaceAll(strings.TrimRight(e.text, "\n"), "\n", " ") + "_\n\n"
	case entrySetup:
		return "```\n" + strings.TrimRight(e.text, "\n") + "\n```\n\n"
	case entryTool:
		return transcriptTool(e.tool, cwd)
	case entryPlan:
		return transcriptPlan(e.plan)
	}
	return ""
}

func transcriptTool(call acp.ToolCall, cwd string) string {
	glyph := "•"
	switch call.Status {
	case acp.StatusCompleted:
		glyph = "✓"
	case acp.StatusFailed:
		glyph = "✗"
	}
	out := "**" + glyph + " " + toolLabel(call, cwd) + "**\n\n"
	if changes := callDiff(call); len(changes) > 0 {
		out += "```diff\n" + strings.TrimRight(toolCopyText(call), "\n") + "\n```\n\n"
		return out
	}
	if text := strings.TrimRight(unfence(toolOutput(call)), "\n"); text != "" {
		out += "```\n" + text + "\n```\n\n"
	}
	return out
}

func transcriptPlan(entries []acp.PlanEntry) string {
	var b strings.Builder
	for _, e := range entries {
		box := "[ ]"
		if e.Status == acp.PlanCompleted {
			box = "[x]"
		}
		fmt.Fprintf(&b, "- %s %s\n", box, e.Content)
	}
	b.WriteString("\n")
	return b.String()
}
