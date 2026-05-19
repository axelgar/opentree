package tui

import "testing"

// Bug C regression: when the user escapes out of ModePRGenerating before the
// async prContentGeneratedMsg arrives, the late message must be dropped
// rather than silently flipping the mode into ModePRCreating.
func TestPRGenerating_EscCancelsAndLateContentIsDropped(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.mode = ModePRGenerating
	m.pr.wsName = "ws"
	m.pr.branch = "feat/ws"
	m.pr.base = "main"

	// User hits esc while waiting for generation.
	m, _ = applyUpdate(m, keyMsg("esc"))

	if m.mode != ModeList {
		t.Fatalf("after esc: mode = %v, want ModeList", m.mode)
	}
	if !m.pr.cancelled {
		t.Fatal("after esc: pr.cancelled should be true")
	}

	// Async message arrives after the user has already moved on.
	m, _ = applyUpdate(m, prContentGeneratedMsg{title: "generated title", body: "generated body"})

	if m.mode != ModeList {
		t.Errorf("after late prContentGeneratedMsg: mode = %v, want ModeList (the message must be dropped)", m.mode)
	}
	if m.pr.cancelled {
		t.Error("after late prContentGeneratedMsg: pr.cancelled should be reset to false")
	}
}

// Bug B regression: while in ModePRGenerating, non-esc keys must NOT fall
// through to the list-mode handlers (which would, e.g., open a create dialog
// on top of the generating screen).
func TestPRGenerating_NonEscKeysAreSwallowed(t *testing.T) {
	m := newTestModel(testWS("ws"))
	m.mode = ModePRGenerating
	m.pr.wsName = "ws"
	m.pr.branch = "feat/ws"
	m.pr.base = "main"

	// 'n' would open the create dialog if the mode didn't gate keys.
	m, _ = applyUpdate(m, keyMsg("n"))

	if m.mode != ModePRGenerating {
		t.Errorf("after 'n' in ModePRGenerating: mode = %v, want ModePRGenerating (key must be swallowed)", m.mode)
	}
}

// Bug A regression: errMsg during a modal dialog must fully return to ModeList,
// zeroing per-mode sub-structs.
func TestErrMsg_ResetsAllModesAndSubStructs(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
		set  func(*Model)
	}{
		{"delete", ModeDelete, func(m *Model) { m.del.target = "x" }},
		{"diff", ModeDiff, func(m *Model) { m.diff.content = "y"; m.diff.wsName = "x" }},
		{"agentselect", ModeAgentSelect, func(m *Model) { m.agentSel.cursor = 2 }},
		{"errorlog", ModeErrorLog, nil},
		{"pr-generating", ModePRGenerating, func(m *Model) { m.pr.wsName = "x" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.mode = tc.mode
			if tc.set != nil {
				tc.set(&m)
			}

			m, _ = applyUpdate(m, errMsg{err: fmtError("boom")})

			if m.mode != ModeList {
				t.Errorf("after errMsg from %s: mode = %v, want ModeList", tc.name, m.mode)
			}
			if m.del.target != "" {
				t.Errorf("after errMsg from %s: del.target = %q, want empty", tc.name, m.del.target)
			}
			if m.diff.content != "" || m.diff.wsName != "" {
				t.Errorf("after errMsg from %s: diff state not zeroed (content=%q wsName=%q)", tc.name, m.diff.content, m.diff.wsName)
			}
			if m.agentSel.cursor != 0 {
				t.Errorf("after errMsg from %s: agentSel.cursor = %d, want 0", tc.name, m.agentSel.cursor)
			}
			if m.pr.wsName != "" {
				t.Errorf("after errMsg from %s: pr.wsName = %q, want empty", tc.name, m.pr.wsName)
			}
		})
	}
}

// fmtError is a tiny adapter so the regression test doesn't need to import fmt.
type fmtError string

func (e fmtError) Error() string { return string(e) }
