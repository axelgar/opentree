package chat

import (
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/acp"
)

// An agent says things on stderr that the protocol has no place for, and that
// matter: Gemini reports it is skipping a worktree's own agents and hooks
// because the folder is not trusted, and says it only there.
func TestOutputCommand_OnlyWithAnAgentToHavePrintedIt(t *testing.T) {
	m := newTestModel()
	if _, ok := m.clientCommandFor("/output"); ok {
		t.Error("offered /output with no agent to have printed anything")
	}

	// With one, the command exists whether or not it has said anything:
	// "it printed nothing" is an answer, and a missing command is not.
	m.client = &acp.Client{}
	if _, ok := m.clientCommandFor("/output"); !ok {
		t.Fatalf("no /output in %v", m.clientCommands())
	}

	next, _ := m.showOutput()
	if !strings.Contains(next.(Model).View(), "printed nothing") {
		t.Errorf("silence and nothing-to-show must not look identical:\n%s", next.(Model).View())
	}
}

// The stopped panel keeps one line of the death, deliberately — the rest would
// swamp a footer. An agent that printed nothing gets no paragraph either way.
func TestAgentGone_AddsNothingWhenNothingWasPrinted(t *testing.T) {
	m := newTestModel()
	m.client = &acp.Client{}

	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})
	if !m.dead {
		t.Fatal("expected the agent to be gone")
	}
	for _, e := range m.entries {
		if strings.Contains(e.text, "printed") {
			t.Errorf("added %q for an agent that printed nothing", e.text)
		}
	}
}
