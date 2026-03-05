package agent

import (
	"testing"
)

func TestNewOpenCodeAgent_DefaultCommand(t *testing.T) {
	a := NewOpenCodeAgent("", nil)
	if a == nil {
		t.Fatal("NewOpenCodeAgent() returned nil")
	}
	if a.command != "opencode" {
		t.Errorf("command = %q, want %q", a.command, "opencode")
	}
}

func TestNewOpenCodeAgent_CustomCommand(t *testing.T) {
	a := NewOpenCodeAgent("my-agent", []string{"--flag"})
	if a.command != "my-agent" {
		t.Errorf("command = %q, want %q", a.command, "my-agent")
	}
	if len(a.args) != 1 || a.args[0] != "--flag" {
		t.Errorf("args = %v, want [--flag]", a.args)
	}
}

func TestName(t *testing.T) {
	a := NewOpenCodeAgent("", nil)
	if a.Name() != "opencode" {
		t.Errorf("Name() = %q, want %q", a.Name(), "opencode")
	}
}

func TestStart_SetsWorkdir(t *testing.T) {
	a := NewOpenCodeAgent("echo", []string{"hello"})
	workdir := "/tmp"

	cmd, err := a.Start(workdir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if cmd == nil {
		t.Fatal("Start() returned nil cmd")
	}
	if cmd.Dir != workdir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, workdir)
	}
}

func TestStart_CommandArgs(t *testing.T) {
	args := []string{"--arg1", "--arg2"}
	a := NewOpenCodeAgent("my-cmd", args)

	cmd, err := a.Start("/tmp")
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// cmd.Args[0] is the command name; subsequent elements are the args.
	if len(cmd.Args) != 3 {
		t.Fatalf("cmd.Args = %v, want [my-cmd --arg1 --arg2]", cmd.Args)
	}
	if cmd.Args[1] != "--arg1" || cmd.Args[2] != "--arg2" {
		t.Errorf("cmd.Args = %v, want [my-cmd --arg1 --arg2]", cmd.Args)
	}
}

func TestStart_NoArgs(t *testing.T) {
	a := NewOpenCodeAgent("opencode", nil)
	cmd, err := a.Start("/some/dir")
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	// cmd.Args should only contain the command itself.
	if len(cmd.Args) != 1 {
		t.Errorf("cmd.Args = %v, want [opencode]", cmd.Args)
	}
}

func TestAgentImplementsInterface(t *testing.T) {
	// Compile-time check that *OpenCodeAgent implements Agent.
	var _ Agent = (*OpenCodeAgent)(nil)
}
