package bootstrap

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// The name is given to portless rather than inferred by it: its own inference
// reads package.json, the git root or the directory, and all three are the same
// for every worktree of one repository — so every worktree would collide.
func TestPortlessHost(t *testing.T) {
	tests := []struct {
		workspace, repo, want string
	}{
		{"feat-dark-mode", "opentree", "feat-dark-mode.opentree.localhost"},
		{"feat/dark mode", "opentree", "feat-dark-mode.opentree.localhost"},
		{"Release/1.2", "my.app", "release-1-2.my-app.localhost"},
		{"feature", "", "feature.localhost"},
	}
	for _, tt := range tests {
		if got := PortlessHost(tt.workspace, tt.repo); got != tt.want {
			t.Errorf("PortlessHost(%q, %q) = %q, want %q", tt.workspace, tt.repo, got, tt.want)
		}
	}
}

// Dots would silently deepen the subdomain, and a wildcard certificate covers
// one level.
func TestPortlessHost_IsOneLabelPerPart(t *testing.T) {
	host := PortlessHost("release/1.2.3", "my.project")
	if strings.Count(host, ".") != 2 {
		t.Errorf("PortlessHost = %q, want exactly branch.repo.localhost", host)
	}
}

// Not installed is the common case, and it is not a reason to say anything is
// wrong: opentree serves on a port and moves on.
func TestCheckPortless_AbsentIsSilent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	p := CheckPortless()
	if p.Installed || p.Ready {
		t.Errorf("CheckPortless with nothing on PATH = %+v", p)
	}
	if p.Reason != "" {
		t.Errorf("Reason = %q, want nothing to explain", p.Reason)
	}
}

// Installed but with no proxy answering: opentree must not launch it, because
// getting it running asks for sudo — and in a detached tmux window nobody
// would ever see the prompt.
func TestCheckPortless_InstalledWithoutAProxy(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "portless")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { // #nosec G306 -- a fake binary for this test
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	p := CheckPortless()
	if !p.Installed {
		t.Fatal("CheckPortless did not find portless on PATH")
	}
	// Skipped rather than failed when the machine really is serving on 443:
	// this test cannot unbind someone's proxy.
	if Listening(443) || Listening(80) {
		t.Skip("something is already serving on 443/80")
	}
	if p.Ready {
		t.Error("CheckPortless called an unstarted proxy ready")
	}
	if p.Reason == "" {
		t.Error("an installed but unusable portless says nothing about why")
	}
}
