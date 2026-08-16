package bootstrap

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// Linux's ephemeral range starts at 32768 and macOS's at 49152, so a port
// allocated above those collides with the kernel's own choices intermittently —
// the worst kind of bug, since it reproduces for nobody.
func TestAssignPort_StaysBelowTheEphemeralRange(t *testing.T) {
	for _, name := range []string{"feat-dark-mode", "fix/header", "a", strings.Repeat("x", 200)} {
		port, err := AssignPort(name, nil)
		if err != nil {
			t.Fatalf("AssignPort(%q): %v", name, err)
		}
		if port < portFirst || port >= portLast {
			t.Errorf("AssignPort(%q) = %d, want %d–%d", name, port, portFirst, portLast)
		}
	}
}

// Stable, so the same workspace keeps the port an OAuth redirect URI was
// registered against.
func TestAssignPort_IsStableForAName(t *testing.T) {
	first, err := AssignPort("feat-dark-mode", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AssignPort("feat-dark-mode", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("the same workspace got %d then %d", first, second)
	}
}

func TestAssignPort_AvoidsWhatSiblingsHold(t *testing.T) {
	taken, err := AssignPort("feat-dark-mode", nil)
	if err != nil {
		t.Fatal(err)
	}

	next, err := AssignPort("feat-dark-mode", map[int]bool{taken: true})
	if err != nil {
		t.Fatal(err)
	}
	if next == taken {
		t.Errorf("AssignPort returned %d, which another workspace already holds", next)
	}
}

// A port free in state can still be busy on the machine — another project, a
// database, anything. Binding is the only honest test.
func TestAssignPort_AvoidsAPortInUseOnTheMachine(t *testing.T) {
	wanted, err := AssignPort("feat-dark-mode", nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", wanted))
	if err != nil {
		t.Skipf("could not occupy port %d: %v", wanted, err)
	}
	defer func() { _ = ln.Close() }()

	got, err := AssignPort("feat-dark-mode", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == wanted {
		t.Errorf("AssignPort handed out %d while something was listening on it", got)
	}
}
