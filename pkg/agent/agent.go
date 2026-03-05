package agent

import (
	"os/exec"
)

// Agent represents a coding agent interface
type Agent interface {
	Name() string
	Start(workdir string) (*exec.Cmd, error)
}

// OpenCodeAgent implements the Agent interface for opencode
type OpenCodeAgent struct {
	command string
	args    []string
}

// NewOpenCodeAgent creates a new opencode agent
func NewOpenCodeAgent(command string, args []string) *OpenCodeAgent {
	if command == "" {
		command = "opencode"
	}
	return &OpenCodeAgent{
		command: command,
		args:    args,
	}
}

// Name returns the agent name
func (a *OpenCodeAgent) Name() string {
	return "opencode"
}

// Start launches the opencode agent in the given working directory
func (a *OpenCodeAgent) Start(workdir string) (*exec.Cmd, error) {
	cmd := exec.Command(a.command, a.args...)
	cmd.Dir = workdir
	return cmd, nil
}
