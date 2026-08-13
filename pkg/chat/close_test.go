package chat

import (
	"testing"

	"github.com/axelgar/opentree/pkg/acp"
)

// Leaving a chat SIGKILLs the agent's whole process group. An agent that takes
// being told the conversation is over gets told first — and an empty session it
// discards on being told must stop being offered, or the next launch resumes
// into "not found" and reads as a conversation that was lost.
func TestCloseOnExit(t *testing.T) {
	tests := []struct {
		name        string
		session     string
		canClose    bool
		spoken      bool
		say, forget bool
	}{
		{name: "an agent that takes it, and nothing was said", session: "s1", canClose: true, say: true, forget: true},
		{name: "an agent that takes it, and something was", session: "s1", canClose: true, spoken: true, say: true},
		{name: "an agent that does not take it", session: "s1"},
		{name: "no conversation to end", canClose: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.client = &acp.Client{}
			m.sessionID, m.canCloseSession, m.titled = tt.session, tt.canClose, tt.spoken

			say, forget := m.closeOnExit()
			if say != tt.say || forget != tt.forget {
				t.Errorf("closeOnExit() = %v %v, want %v %v", say, forget, tt.say, tt.forget)
			}
		})
	}
}

// A resumed conversation has a history whether or not anything has been said to
// it today, so its id survives being closed.
func TestCloseOnExit_AResumedConversationIsNotForgotten(t *testing.T) {
	m := newTestModel()
	m.client = &acp.Client{}
	m.sessionID, m.canCloseSession = "s1", true
	m, _ = applyUpdate(m, sessionReadyMsg{id: "s1", resumed: true})

	if _, forget := m.closeOnExit(); forget {
		t.Error("a conversation with a history behind it must not be dropped from the ledger")
	}
}
