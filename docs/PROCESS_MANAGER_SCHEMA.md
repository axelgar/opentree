# ProcessManager Abstraction Schema

## Current Architecture: Direct tmux Coupling

Every consumer directly instantiates and calls `tmux.Controller`:

```
┌─────────────────────────────────────────────────────────────┐
│                        CONSUMERS                            │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌───────────┐  │
│  │ cmd/new  │  │cmd/delete│  │cmd/attach│  │  pkg/tui   │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └─────┬─────┘  │
│       │              │              │              │         │
│       │   tmux.New() │   tmux.New() │   tmux.New() │         │
│       ▼              ▼              ▼              ▼         │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              tmux.Controller (CONCRETE)               │   │
│  │                                                       │   │
│  │  CreateWindow(name, workdir, command, args...)         │   │
│  │  KillWindow(name)                                     │   │
│  │  AttachWindow(name)    ← handles 3 tmux environments  │   │
│  │  AttachCmd(name)       ← returns *exec.Cmd for TUI    │   │
│  │  ListWindows()                                        │   │
│  │  CapturePane(name, lines)                             │   │
│  │  GetWindowActivity(name)                              │   │
│  │  KillSession()                                        │   │
│  │  SelectWindow(name)                                   │   │
│  └──────────────────────┬───────────────────────────────┘   │
│                         │                                    │
│                         │  os/exec                           │
│                         ▼                                    │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                   tmux binary                         │   │
│  │                                                       │   │
│  │  new-session, new-window, send-keys, kill-window,     │   │
│  │  capture-pane, list-windows, attach-session,          │   │
│  │  switch-client, select-window, display-message,       │   │
│  │  has-session                                          │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘

Problems:
  ✗ Every consumer imports "pkg/tmux" directly
  ✗ Can't test any consumer without a running tmux server
  ✗ Can't swap tmux for another backend
  ✗ Agent launch (send-keys) has no confirmation or exit code
  ✗ ulimit hack injected into every command string
  ✗ sanitizeWindowName() duplicated in worktree package
```

---

## Proposed Architecture: ProcessManager Interface

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           CONSUMERS                                     │
│                                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────────────────┐  │
│  │ cmd/new  │  │cmd/delete│  │cmd/attach│  │       pkg/tui          │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └──────────┬─────────────┘  │
│       │              │              │                   │                │
│       └──────────────┴──────┬───────┴───────────────────┘                │
│                             │                                            │
│                             ▼                                            │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │              process.Manager (INTERFACE)                          │   │
│  │                                                                   │   │
│  │  Launch(name, workdir, command string, args ...string) error      │   │
│  │  Stop(name string) error                                          │   │
│  │  StopAll() error                                                  │   │
│  │  IsRunning(name string) bool                                      │   │
│  │  List() ([]ProcessInfo, error)                                    │   │
│  │  CaptureOutput(name string, lines int) (string, error)            │   │
│  │  LastActivity(name string) (time.Time, error)                     │   │
│  │  Attach(name string) error              ← interactive terminal    │   │
│  │  AttachCmd(name string) (*exec.Cmd, error) ← for Bubble Tea      │   │
│  └──────────────────────────┬───────────────────────────────────────┘   │
│                             │                                            │
│               ┌─────────────┼─────────────┐                             │
│               │             │             │                              │
│               ▼             ▼             ▼                              │
│  ┌────────────────┐ ┌──────────────┐ ┌───────────────┐                  │
│  │ TmuxManager    │ │ MockManager  │ │ Future: PTY / │                  │
│  │ (production)   │ │ (tests)      │ │ Zellij / etc  │                  │
│  └───────┬────────┘ └──────────────┘ └───────────────┘                  │
│          │                                                               │
│          ▼                                                               │
│  ┌──────────────┐                                                       │
│  │ tmux binary  │                                                       │
│  └──────────────┘                                                       │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Interface Definition: `pkg/process/process.go`

```go
package process

import (
    "os/exec"
    "time"
)

// ProcessInfo represents a running or managed process.
type ProcessInfo struct {
    Name   string
    Active bool   // has recent activity
}

// Manager abstracts process lifecycle for workspace agents.
// Production uses tmux; tests use a mock.
type Manager interface {
    // Launch starts a named process in the given workdir.
    // The implementation decides HOW to run it (tmux window, PTY, etc.)
    Launch(name, workdir, command string, args ...string) error

    // Stop terminates a single named process.
    Stop(name string) error

    // StopAll terminates all managed processes (session cleanup).
    StopAll() error

    // IsRunning checks whether the named process is still alive.
    IsRunning(name string) bool

    // List returns all managed processes.
    List() ([]ProcessInfo, error)

    // CaptureOutput returns recent terminal output (for preview).
    CaptureOutput(name string, lines int) (string, error)

    // LastActivity returns when the process last had terminal activity.
    LastActivity(name string) (time.Time, error)

    // Attach opens an interactive terminal session to the process.
    // Blocks until the user detaches.
    Attach(name string) error

    // AttachCmd returns an *exec.Cmd for attaching, so the caller
    // can control execution (needed by Bubble Tea's ExecProcess).
    AttachCmd(name string) (*exec.Cmd, error)
}
```

---

## Implementation: `pkg/tmux/manager.go`

```go
package tmux

// TmuxManager implements process.Manager using tmux.
// This is a refactor of the existing Controller — same behavior, new interface.
type TmuxManager struct {
    sessionPrefix  string
    repoName       string        // injected, no longer self-discovered
    repoNameOnce   sync.Once
    cachedSession  string
}

func NewManager(sessionPrefix, repoName string) *TmuxManager { ... }

// ── Interface methods map 1:1 to existing Controller methods ──

// Launch       → CreateWindow   (wraps send-keys, adds ulimit)
// Stop         → KillWindow
// StopAll      → KillSession
// IsRunning    → sessionExists + list-windows check
// List         → ListWindows    (returns []ProcessInfo)
// CaptureOutput→ CapturePane
// LastActivity → GetWindowActivity
// Attach       → AttachWindow   (handles env detection)
// AttachCmd    → AttachCmd       (returns *exec.Cmd)
```

**Key change**: `repoName` is now injected via constructor instead of each package
independently calling `git rev-parse --show-toplevel`.

---

## Mock for Testing: `pkg/process/mock.go`

```go
package process

// MockManager is an in-memory implementation for unit tests.
type MockManager struct {
    Processes map[string]*MockProcess
    Launches  []LaunchCall   // records all Launch() calls for assertions
}

type MockProcess struct {
    Name    string
    Workdir string
    Command string
    Running bool
    Output  string         // simulated capture output
}

type LaunchCall struct {
    Name, Workdir, Command string
    Args                   []string
}

func NewMockManager() *MockManager { ... }

func (m *MockManager) Launch(name, workdir, command string, args ...string) error {
    m.Processes[name] = &MockProcess{Name: name, Workdir: workdir, Command: command, Running: true}
    m.Launches = append(m.Launches, LaunchCall{name, workdir, command, args})
    return nil
}

func (m *MockManager) IsRunning(name string) bool {
    p, ok := m.Processes[name]
    return ok && p.Running
}

// ... other methods return sensible defaults
```

---

## How Consumers Change

### Before (cmd/new.go):
```go
import "github.com/axelgar/opentree/pkg/tmux"

tmuxCtrl := tmux.New(cfg.Tmux.SessionPrefix)           // concrete
tmuxCtrl.CreateWindow(branchName, worktreePath, cmd...) // tmux-specific
```

### After (cmd/new.go):
```go
import "github.com/axelgar/opentree/pkg/process"
import "github.com/axelgar/opentree/pkg/tmux"

var pm process.Manager = tmux.NewManager(cfg.Tmux.SessionPrefix, repoName)
pm.Launch(branchName, worktreePath, agentCmd, cfg.Agent.Args...)  // generic
```

### Before (pkg/tui/tui.go):
```go
type Model struct {
    tmuxCtrl *tmux.Controller   // concrete dependency
    ...
}

// 14 direct calls to tmuxCtrl scattered across 1706 lines:
m.tmuxCtrl.CreateWindow(...)
m.tmuxCtrl.KillWindow(...)
m.tmuxCtrl.AttachCmd(...)
m.tmuxCtrl.CapturePane(...)
m.tmuxCtrl.ListWindows(...)
m.tmuxCtrl.GetWindowActivity(...)
m.tmuxCtrl.KillSession(...)
```

### After (pkg/tui/tui.go):
```go
type Model struct {
    processes process.Manager   // interface — testable
    ...
}

// Same 14 calls, but through the interface:
m.processes.Launch(...)
m.processes.Stop(...)
m.processes.AttachCmd(...)
m.processes.CaptureOutput(...)
m.processes.List(...)
m.processes.LastActivity(...)
m.processes.StopAll(...)
```

---

## Testing Unlocked

```go
func TestCreateWorkspace(t *testing.T) {
    mock := process.NewMockManager()
    svc := workspace.NewService(
        worktreeMgr,
        mock,           // ← no tmux needed
        stateStore,
        config,
    )

    ws, err := svc.Create("feat/auth", "main")
    require.NoError(t, err)

    // Assert the agent was launched correctly
    require.Len(t, mock.Launches, 1)
    assert.Equal(t, "feat/auth", mock.Launches[0].Name)
    assert.Equal(t, "opencode", mock.Launches[0].Command)
    assert.True(t, mock.IsRunning("feat/auth"))
}

func TestDeleteWorkspace_KillsProcess(t *testing.T) {
    mock := process.NewMockManager()
    mock.Processes["feat/auth"] = &process.MockProcess{Running: true}

    svc := workspace.NewService(worktreeMgr, mock, stateStore, config)
    err := svc.Delete("feat/auth", true)
    require.NoError(t, err)

    assert.False(t, mock.IsRunning("feat/auth"))
}
```

---

## Migration Path

This is a **pure refactor** — no behavior changes, no new dependencies:

```
Step 1: Create pkg/process/process.go         (interface only, ~40 lines)
Step 2: Create pkg/process/mock.go            (test helper, ~60 lines)
Step 3: Add NewManager() to pkg/tmux/         (wraps existing Controller)
        Make TmuxManager implement process.Manager
Step 4: Update cmd/*.go                       (swap tmux.New → tmux.NewManager)
        Change CreateWindow → Launch, KillWindow → Stop, etc.
Step 5: Update pkg/tui/tui.go                 (swap tmuxCtrl type to process.Manager)
Step 6: Write tests for consumers using MockManager
Step 7: Delete old tmux.Controller API (or keep as internal)
```

Each step compiles and passes tests independently. No big bang.
