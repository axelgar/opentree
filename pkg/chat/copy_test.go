package chat

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

// captureClipboard swaps the clipboard for a string and returns where the
// text will land.
func captureClipboard(t *testing.T) *string {
	t.Helper()
	var got string
	prev := writeClipboard
	writeClipboard = func(text string) error {
		got = text
		return nil
	}
	t.Cleanup(func() { writeClipboard = prev })
	return &got
}

const replyWithCode = "Two things.\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\nand then\n\n```bash\ngo test ./...\n```"

func ctrlY() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlY} }

func TestCopy_ListsTheReplyItsBlocksTheToolAndTheWhole(t *testing.T) {
	m := newTestModel()
	m.entries = append(m.entries, entry{kind: entryUser, text: "write main"})
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, replyWithCode))
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusCompleted, "ok\tpkg/auth\t0.4s")))

	m, _ = applyUpdate(m, ctrlY())
	view := m.View()
	for _, want := range []string{
		"copy to the clipboard",
		"last reply", "code block 1", "go · 3 lines", "code block 2", "bash · 1 line",
		"last tool output", "go test ./...", "whole conversation",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("picker is missing %q:\n%s", want, view)
		}
	}
}

func TestCopy_ABlockArrivesVerbatim(t *testing.T) {
	got := captureClipboard(t)
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, replyWithCode))
	m, _ = applyUpdate(m, ctrlY())

	// Row 2: the first block. Chosen by digit, the way the settings picker
	// takes a row.
	m, cmd := applyUpdate(m, keyMsg("2"))
	if cmd == nil {
		t.Fatal("choosing a row returned no command")
	}
	if strings.Contains(m.View(), "copy to the clipboard") {
		t.Error("the picker stayed open after choosing")
	}
	msg := cmd()
	if want := "func main() {\n\tfmt.Println(\"hi\")\n}\n"; *got != want {
		t.Errorf("clipboard got %q, want the block as written %q", *got, want)
	}

	m, _ = applyUpdate(m, msg)
	if !strings.Contains(m.View(), "copied code block 1") {
		t.Errorf("the status line did not say what was copied:\n%s", m.View())
	}
}

func TestCopy_TheLastReplyIsItsMarkdown(t *testing.T) {
	got := captureClipboard(t)
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "**Done.** See `main.go`."))
	m, _ = applyUpdate(m, ctrlY())
	_, cmd := applyUpdate(m, keyMsg("enter"))
	cmd()
	if *got != "**Done.** See `main.go`.\n" {
		t.Errorf("clipboard got %q, want the reply's own markdown", *got)
	}
}

func TestCopy_BlocksFromAnEarlierReplyAreOfferedAndSaySo(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, replyWithCode))
	m.entries = append(m.entries, entry{kind: entryUser, text: "thanks"})
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "Done."))

	m, _ = applyUpdate(m, ctrlY())
	view := m.View()
	if !strings.Contains(view, "code block 1") {
		t.Fatalf("a block two replies up was not offered:\n%s", view)
	}
	if !strings.Contains(view, "an earlier reply") {
		t.Errorf("the row did not say the block is from an earlier reply:\n%s", view)
	}
}

func TestCopy_ToolOutputIsUnfencedAndDiffsKeepTheirSigns(t *testing.T) {
	got := captureClipboard(t)
	m := newTestModel()
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusFailed, "```console\nFAIL\tpkg/auth\n```")))
	m, _ = applyUpdate(m, ctrlY())
	m, cmd := applyUpdate(m, keyMsg("enter"))
	cmd()
	if *got != "FAIL\tpkg/auth\n" {
		t.Errorf("clipboard got %q, want the output without its fence", *got)
	}

	edit := acp.ToolCall{ToolCallID: "t2", Title: "main.go", Kind: "edit", Status: acp.StatusCompleted,
		Content: []acp.ToolCallContent{{Type: "diff", Path: "/repo/main.go", OldText: "a\nb\n", NewText: "a\nc\n"}}}
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, edit))
	m, _ = applyUpdate(m, ctrlY())
	_, cmd = applyUpdate(m, keyMsg("enter"))
	cmd()
	if *got != "- b\n+ c\n" {
		t.Errorf("clipboard got %q, want the diff as +/- lines", *got)
	}
}

func TestCopy_WholeConversationIsADocument(t *testing.T) {
	got := captureClipboard(t)
	m := newTestModel()
	m.entries = append(m.entries, entry{kind: entryUser, text: "add a test"})
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "Adding one."))
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusCompleted, "ok")))
	m, _ = applyUpdate(m, ctrlY())
	// The last row, whatever else is offered.
	rows := len(m.copying.items)
	_, cmd := applyUpdate(m, keyMsg(string(rune('0'+rows))))
	cmd()

	for _, want := range []string{"# fix-auth — OpenCode", "## You\n\nadd a test", "## OpenCode\n\nAdding one.", "**✓ go test ./...**", "```\nok\n```"} {
		if !strings.Contains(*got, want) {
			t.Errorf("transcript is missing %q:\n%s", want, *got)
		}
	}
}

func TestCopy_NothingToCopySaysSoWithoutAPicker(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, ctrlY())
	view := m.View()
	if strings.Contains(view, "copy to the clipboard") {
		t.Error("an empty conversation opened a picker with nothing in it")
	}
	if !strings.Contains(view, "nothing to copy yet") {
		t.Errorf("the status line did not say there is nothing to copy:\n%s", view)
	}
}

func TestCopy_AFailureIsReportedOnTheStatusLine(t *testing.T) {
	prev := writeClipboard
	writeClipboard = func(string) error { return errors.New("no clipboard tool found") }
	t.Cleanup(func() { writeClipboard = prev })

	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "hi"))
	m, _ = applyUpdate(m, ctrlY())
	m, cmd := applyUpdate(m, keyMsg("enter"))
	m, _ = applyUpdate(m, cmd())
	if !strings.Contains(m.View(), "could not copy: no clipboard tool found") {
		t.Errorf("the failure did not reach the status line:\n%s", m.View())
	}
	if m.err != nil {
		t.Error("a clipboard failure was recorded as a failed turn")
	}
}

func TestCopy_TheFlashClearsOnItsOwnTimerOnly(t *testing.T) {
	captureClipboard(t)
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "hi"))
	m, _ = applyUpdate(m, ctrlY())
	m, cmd := applyUpdate(m, keyMsg("enter"))
	m, _ = applyUpdate(m, cmd())
	if !strings.Contains(m.View(), "copied last reply") {
		t.Fatalf("no flash to clear:\n%s", m.View())
	}

	m, _ = applyUpdate(m, flashClearMsg{seq: m.flash.seq - 1})
	if !strings.Contains(m.View(), "copied last reply") {
		t.Error("a stale timer cleared a newer flash")
	}
	m, _ = applyUpdate(m, flashClearMsg{seq: m.flash.seq})
	if strings.Contains(m.View(), "copied last reply") {
		t.Error("the flash outlived its own timer")
	}
}

func TestCopy_EscClosesThePicker(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "hi"))
	m, _ = applyUpdate(m, ctrlY())
	m, _ = applyUpdate(m, keyMsg("esc"))
	if strings.Contains(m.View(), "copy to the clipboard") {
		t.Error("esc left the picker open")
	}
}

func TestCodeBlocks_AnOpenFenceIsStillABlock(t *testing.T) {
	blocks := codeBlocks("text\n\n```python\nprint(1)\nprint(2)")
	if len(blocks) != 1 || blocks[0].lang != "python" || blocks[0].text != "print(1)\nprint(2)\n" {
		t.Errorf("codeBlocks = %+v, want the half-arrived block", blocks)
	}
	if got := codeBlocks("no code here"); len(got) != 0 {
		t.Errorf("codeBlocks = %+v, want none", got)
	}
}
