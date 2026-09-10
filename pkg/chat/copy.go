package chat

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
	"github.com/axelgar/opentree/pkg/clipboard"
)

// The copy picker is how text leaves the chat without a mouse. The chat holds
// the mouse for the wheel, which takes the terminal's own selection with it,
// and the thing the agent just wrote — a command to run, a block of code, the
// failure a test printed — is exactly what a reader most often wants to carry
// somewhere else. ctrl+y offers the few things worth copying and puts the one
// chosen on the clipboard, whole: a code block arrives with its indentation,
// not with the bullet and the wrap the log drew it with.

// copyItem is one thing the picker offers: how it is listed, and the text that
// goes to the clipboard when it is chosen.
type copyItem struct {
	label, desc, text string
}

// copying is the picker's state.
type copying struct {
	open   bool
	cursor int
	items  []copyItem
}

// copiedMsg is what the clipboard said.
type copiedMsg struct {
	what string
	err  error
}

// writeClipboard is the clipboard, as a variable so a test can read what was
// sent without a pbcopy of its own.
var writeClipboard = clipboard.Write

// openCopy lists what there is to copy, or says there is nothing yet.
func (m Model) openCopy() (tea.Model, tea.Cmd) {
	items := m.copyItems()
	if len(items) == 0 {
		next, cmd := m.withFlash("nothing to copy yet", false)
		return next, cmd
	}
	m.copying = copying{open: true, items: items}
	return m.relayout(), nil
}

// copyItems is what the picker offers, in the order a reader is likely to
// want them: the last reply, the code blocks in it, the last tool's output,
// and the whole conversation.
//
// The blocks come from the newest reply that has any. The last reply is often
// a one-line "done" under a run of tool calls, and the block worth copying
// was two replies up; when that happens the row says so.
func (m Model) copyItems() []copyItem {
	var items []copyItem

	last, lastAt := m.lastEntry(entryAgent)
	if lastAt >= 0 {
		items = append(items, copyItem{
			label: "last reply",
			desc:  countLines(last.text),
			text:  strings.TrimRight(last.text, "\n") + "\n",
		})
	}

	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.kind != entryAgent {
			continue
		}
		blocks := codeBlocks(e.text)
		if len(blocks) == 0 {
			continue
		}
		earlier := ""
		if i != lastAt {
			earlier = " · an earlier reply"
		}
		for n, b := range blocks {
			desc := countLines(b.text)
			if b.lang != "" {
				desc = b.lang + " · " + desc
			}
			items = append(items, copyItem{
				label: fmt.Sprintf("code block %d", n+1),
				desc:  desc + earlier,
				text:  b.text,
			})
		}
		break
	}

	if tool, at := m.lastEntry(entryTool); at >= 0 {
		if text := toolCopyText(tool.tool); text != "" {
			items = append(items, copyItem{
				label: "last tool output",
				desc:  joinMeta(toolLabel(tool.tool, m.opts.Cwd), countLines(text)),
				text:  text,
			})
		}
	}

	if m.conversationStarted() {
		items = append(items, copyItem{
			label: "whole conversation",
			desc:  "as markdown",
			text:  m.transcriptMarkdown(),
		})
	}
	return items
}

// lastEntry is the newest entry of a kind, and where it sits; -1 for none.
func (m Model) lastEntry(kind entryKind) (entry, int) {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind == kind {
			return m.entries[i], i
		}
	}
	return entry{}, -1
}

// codeBlock is one fenced block out of a reply: the language its fence named,
// and its lines as written — tabs, indentation and all.
type codeBlock struct {
	lang string
	text string
}

// codeBlocks cuts every fenced block out of a reply, with the same fence rules
// the renderer paints by, so what the picker offers is exactly what the log
// showed as code. A fence still open at the end of the text is a block too:
// a reply that stopped mid-block is the reply there is.
func codeBlocks(text string) []codeBlock {
	var out []codeBlock
	var fence *fenceState
	for _, line := range strings.Split(text, "\n") {
		if fence != nil {
			if fence.closes(line) {
				out = append(out, blockOf(*fence))
				fence = nil
				continue
			}
			fence.lines = append(fence.lines, line)
			continue
		}
		if f, ok := fenceOpen(line); ok {
			fence = &f
		}
	}
	if fence != nil {
		out = append(out, blockOf(*fence))
	}
	return out
}

func blockOf(f fenceState) codeBlock {
	return codeBlock{lang: f.lang, text: strings.Join(f.lines, "\n") + "\n"}
}

// toolCopyText is what a tool row has to give: its diff as the +/- lines the
// row drew, or what it printed, unfenced — the same choice renderTool makes,
// so the text copied is the text that was on screen.
func toolCopyText(call acp.ToolCall) string {
	if changes := callDiff(call); len(changes) > 0 {
		lines := make([]string, 0, len(changes))
		for _, ch := range changes {
			sign := "-"
			if ch.add {
				sign = "+"
			}
			lines = append(lines, sign+" "+ch.text)
		}
		return strings.Join(lines, "\n") + "\n"
	}
	out := strings.TrimRight(unfence(toolOutput(call)), "\n")
	if out == "" {
		return ""
	}
	return out + "\n"
}

// countLines says how much a row would copy, in the unit a reader thinks in.
func countLines(text string) string {
	n := len(splitLines(text))
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

func (m Model) copyRows() []completionItem {
	rows := make([]completionItem, 0, len(m.copying.items))
	for _, it := range m.copying.items {
		rows = append(rows, completionItem{value: it.label, desc: it.desc})
	}
	return rows
}

func (m Model) copyView() string {
	return pickerView("copy to the clipboard", m.copyRows(), m.copying.cursor, m.width)
}

func (m Model) copyHeight() int { return pickerHeight(len(m.copying.items)) }

// handleCopyKey drives the picker with the shared keys.
func (m Model) handleCopyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch action, i := pickerKey(msg, &m.copying.cursor, len(m.copying.items)); action {
	case pickerClosed:
		m.copying = copying{}
		return m.relayout(), nil
	case pickerMoved:
		return m.relayout(), nil
	case pickerChose:
		return m.chooseCopy(i)
	}
	return m, nil
}

// chooseCopy sends the chosen text to the clipboard and closes the picker.
// The write runs off the event loop: a clipboard tool is a subprocess, and
// the chat keeps drawing while it runs.
func (m Model) chooseCopy(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.copying.items) {
		return m, nil
	}
	item := m.copying.items[i]
	m.copying = copying{}
	return m.relayout(), copyCmd(item.label+" ("+item.desc+")", item.text)
}

// copyCmd puts text on the clipboard and reports what went, or why not.
func copyCmd(what, text string) tea.Cmd {
	return func() tea.Msg {
		return copiedMsg{what: what, err: writeClipboard(text)}
	}
}

// copied is the clipboard's answer on the status line, where the picker was
// a moment ago. Not in the log: a copy is not something that was said.
func (m Model) copied(msg copiedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		next, cmd := m.withFlash("could not copy: "+firstLine(msg.err.Error()), true)
		return next, cmd
	}
	next, cmd := m.withFlash("copied "+msg.what, false)
	return next, cmd
}
