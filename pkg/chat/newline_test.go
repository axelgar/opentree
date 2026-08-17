package chat

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// csiMsg stands in for the message bubbletea hands over for a CSI sequence it
// has no name for. That type is unexported, and what it prints is the whole of
// what opentree can see of it — so printing it the way bubbletea does is what
// makes this a stand-in rather than a different thing wearing the same name.
type csiMsg string

func (c csiMsg) String() string { return fmt.Sprintf("?CSI%+v?", []byte(c)) }

// newlineKeys is every way a modified enter reaches opentree, plus the two keys
// that stand in for it where the terminal reports no modifiers at all.
//
// The modifier codes are the bitmask plus one: 2 shift, 3 alt, 5 ctrl, 6
// ctrl+shift. All of them break the line — which one was held is not a
// distinction the message box makes.
var newlineKeys = []struct {
	name string
	msg  tea.Msg
}{
	{"shift+enter over modifyOtherKeys", csiMsg("27;2;13~")},
	{"shift+enter over CSI u", csiMsg("13;2u")},
	{"ctrl+enter over modifyOtherKeys", csiMsg("27;5;13~")},
	{"ctrl+shift+enter over CSI u", csiMsg("13;6u")},
	{"alt+enter", tea.KeyMsg{Type: tea.KeyEnter, Alt: true}},
	{"ctrl+j", tea.KeyMsg{Type: tea.KeyCtrlJ}},
}

func TestNewline_BreaksTheLineWithoutSending(t *testing.T) {
	for _, tt := range newlineKeys {
		t.Run(tt.name, func(t *testing.T) {
			m := typeInto(newTestModel(), "first")
			m, _ = applyUpdate(m, tt.msg)
			m = typeInto(m, "second")

			if got, want := m.input.Value(), "first\nsecond"; got != want {
				t.Errorf("the box holds %q, want %q", got, want)
			}
			if m.turn {
				t.Error("a newline should not have sent the message")
			}
		})
	}
}

// A CSI sequence that is not a modified enter is left alone, so a terminal
// reporting something else does not quietly break the message in two.
func TestNewline_LeavesOtherSequencesAlone(t *testing.T) {
	for _, seq := range []string{
		"1;2A",     // shift+up
		"27;2;65~", // shift+a
		"65;2u",    // shift+a, the other format
	} {
		m := typeInto(newTestModel(), "first")
		m, _ = applyUpdate(m, csiMsg(seq))
		m = typeInto(m, "second")

		if got, want := m.input.Value(), "firstsecond"; got != want {
			t.Errorf("after CSI %s the box holds %q, want %q", seq, got, want)
		}
	}
}

// The request the terminal is sent is xterm's level 1, and the undo is paired
// with it: level 2 would re-encode keys nothing here can read, and a terminal
// left in either reports keys to whatever runs next.
func TestNewline_RequestsModifiedKeysConservatively(t *testing.T) {
	if modifyOtherKeysOn != "\x1b[>4;1m" {
		t.Errorf("request = %q, want xterm modifyOtherKeys level 1", modifyOtherKeysOn)
	}
	if modifyOtherKeysOff != "\x1b[>4;0m" {
		t.Errorf("undo = %q, want modifyOtherKeys off", modifyOtherKeysOff)
	}
}

// Nothing is written to a terminal that is not one, and the undo is safe to
// call whether or not the request went.
func TestNewline_RequestSkipsANonTerminal(t *testing.T) {
	restore := extendedKeys()
	if restore == nil {
		t.Fatal("extendedKeys must always return an undo")
	}
	restore()
}

// enter still sends: the newline keys are additions to it, not replacements.
// An enter the terminal spelled out in full sends as well — it should never
// arrive that way at level 1, and one that did and was dropped would read as a
// chat that cannot send.
func TestNewline_EnterStillSends(t *testing.T) {
	for _, key := range []tea.Msg{
		keyMsg("enter"),
		csiMsg("27;1;13~"),
		csiMsg("13;1u"),
	} {
		m := typeInto(newTestModel(), "sent")
		m, _ = applyUpdate(m, key)

		if got := m.input.Value(); got != "" {
			t.Errorf("after %v the box holds %q, want it empty", key, got)
		}
		if !m.turn {
			t.Errorf("%v should have started a turn", key)
		}
	}
}
