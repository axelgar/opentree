package chat

import (
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/acp"
)

// rowWith is the screen row of the first log line containing text, or -1.
func rowWith(m Model, text string) int {
	for i, line := range m.logLines {
		if strings.Contains(line, text) {
			return headerHeight + i - m.viewport.YOffset
		}
	}
	return -1
}

func click(m Model, x, y int) Model {
	m, _ = applyUpdate(m, press(x, y))
	m, _ = applyUpdate(m, release(x, y))
	return m
}

func TestClick_OnAHeldBackRowOpensIt(t *testing.T) {
	m := newTestModel()
	body := strings.TrimSuffix(strings.Repeat("line\n", 30), "\n") + "\nthe last line"
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusCompleted, body)))
	if strings.Contains(m.View(), "the last line") {
		t.Fatal("the cap is not capping; the test is testing nothing")
	}
	y := rowWith(m, "more lines")
	if y < 0 {
		t.Fatal("no held-back row on screen")
	}

	m = click(m, 6, y)
	if !strings.Contains(m.View(), "the last line") {
		t.Errorf("clicking the held-back row did not open it:\n%s", m.View())
	}
	// And the same row, now saying how much it still holds back past the
	// expanded ceiling or nothing at all, folds on a second click only if it
	// still says "more lines" — a fully opened row has nothing to fold from.
	if strings.Contains(m.View(), "more lines") {
		m = click(m, 6, rowWith(m, "more lines"))
		if strings.Contains(m.View(), "the last line") {
			t.Error("a second click did not fold the row")
		}
	}
}

func TestClick_OnOrdinaryTextDoesNothing(t *testing.T) {
	m := replyModel()
	before := m.View()
	m = click(m, 4, headerHeight)
	if m.View() != before {
		t.Error("a click on prose changed the log")
	}
}

func TestClick_OnAPermissionOptionAnswersIt(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, allowAlways, rejectOnce)
	m, _ = applyUpdate(m, perm)

	footer := strings.Split(m.footer(), "\n")
	row := -1
	for i, line := range footer {
		if strings.Contains(line, "[d]") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("no [d] row in the dialog:\n%s", m.footer())
	}

	m, _ = applyUpdate(m, press(4, headerHeight+m.viewport.Height+row))
	if m.perm() != nil {
		t.Fatal("the permission is still waiting after a click on its option")
	}
	select {
	case got := <-perm.reply:
		if got != "reject" {
			t.Errorf("reply = %q, want reject", got)
		}
	default:
		t.Error("nothing was sent to the agent")
	}
}

func TestClick_OnTheDialogsTitleAnswersNothing(t *testing.T) {
	m := newTestModel()
	perm := permission(allowOnce, rejectOnce)
	m, _ = applyUpdate(m, perm)
	// The box's top border, then the tool's own label: neither is a choice.
	for _, row := range []int{1, 2} {
		m, _ = applyUpdate(m, press(4, headerHeight+m.viewport.Height+row))
	}
	if m.perm() == nil || len(perm.reply) != 0 {
		t.Error("a click outside the options answered the permission")
	}
}

func TestBracketedKey(t *testing.T) {
	cases := map[string]string{
		"│ [a] Allow once     │": "a",
		"  [ctrl+c] back":        "ctrl+c",
		"│ rm -rf dist        │": "",
		"permission needed":      "",
	}
	for row, want := range cases {
		if got := bracketedKey(row); got != want {
			t.Errorf("bracketedKey(%q) = %q, want %q", row, got, want)
		}
	}
}
