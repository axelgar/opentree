# Architectural Review: opentree

## Context

opentree is a Go CLI tool that orchestrates parallel AI coding sessions in isolated git worktrees via tmux. The goal is to be the **ultimate developer productivity tool** for working on multiple tasks in parallel. This review questions core architectural decisions and proposes concrete improvements — no new features, just making the existing foundation rock-solid.

---

## 1. Questioning tmux as the Core Abstraction

**Current state**: tmux is a hard, unabstracted dependency. Every workspace = a tmux window. Process launch uses `send-keys`, monitoring uses `capture-pane`, and the entire UX assumes tmux semantics.

**Problems**:

- **tmux is a multiplexer, not an orchestrator.** opentree uses tmux primarily as a process manager with a side benefit of terminal access. But tmux was designed for human terminal multiplexing, not programmatic process orchestration. This creates friction:
  - `send-keys` is fire-and-forget — no confirmation the command actually started, no exit code capture
  - `capture-pane` is lossy — you get visible terminal output, not structured process state
  - Window naming restrictions force branch name sanitization
  - The `ulimit` hack (`ulimit -n 2147483646`) must be injected into every command string because there's no way to set the environment before `send-keys`

- **Tight coupling prevents testability.** The tmux package has minimal test coverage because testing requires a running tmux server. Every component that touches workspace lifecycle (TUI, CLI commands) is implicitly untestable without tmux.

- **The abstraction layer is missing.** There should be a `ProcessManager` interface that tmux *implements*. This would allow:
  - Unit testing with a mock process manager
  - Future alternative backends (e.g., direct `os/exec` + PTY for environments without tmux, Zellij support, etc.)
  - Cleaner separation between "run a process" and "provide terminal access to that process"

**Proposed change**: Introduce a `ProcessManager` interface in a new `pkg/process/` package:

```go
type ProcessManager interface {
    Launch(name, workdir, command string, args ...string) error
    Stop(name string) error
    IsRunning(name string) bool
    CaptureOutput(name string, lines int) (string, error)
    Attach(name string) error  // interactive terminal access
    AttachCmd(name string) (*exec.Cmd, error)
    List() ([]ProcessInfo, error)
}
```

The existing tmux code becomes `TmuxProcessManager` implementing this interface. This is purely a refactor — no behavior change, no new backends needed yet.

**Files to modify**:
- Create `pkg/process/process.go` (interface definition)
- Refactor `pkg/tmux/tmux.go` → implement `ProcessManager`
- Update `pkg/tui/tui.go` to depend on interface, not concrete tmux type
- Update `cmd/opentree/cmd/*.go` similarly

---

## 2. The Dead Agent Abstraction

**Current state**: `pkg/agent/agent.go` defines an `Agent` interface with `Start(workdir) (*exec.Cmd, error)`, but the returned `*exec.Cmd` is **never executed**. Instead, the tmux controller receives the command name as a string and runs it via `send-keys`. The entire agent package is dead code in terms of its actual contract.

**Problems**:
- `OpenCodeAgent.Start()` creates an `exec.Cmd` that nobody runs — it's a misleading API
- `OpenCodeAgent.Name()` returns hardcoded `"opencode"` even when configured with a different command
- The agent package doesn't participate in the actual launch flow at all — `cmd/new.go` directly passes `cfg.Agent.Command` to the tmux controller
- There's no validation that the agent binary exists before creating a workspace

**Proposed change**: Either make the Agent interface meaningful or remove it:

**Option A (Recommended)**: Simplify agent to a value type — it's just configuration, not behavior:
```go
// In pkg/config or pkg/agent
type AgentConfig struct {
    Command string
    Args    []string
}

func (a AgentConfig) Validate() error { /* check binary exists on PATH */ }
func (a AgentConfig) CommandLine() string { /* return full command string */ }
```

Remove the `Start()` method entirely. The process manager handles launching.

**Option B**: If you want agents to control their own launch semantics (e.g., some agents need env vars, pre-launch setup, TASK.md writing), make the interface actually participate:
```go
type Agent interface {
    Name() string
    PreLaunch(workdir string) error  // write TASK.md, set up env
    Command() string
    Args() []string
}
```

**Files to modify**:
- `pkg/agent/agent.go` — simplify or redesign
- `cmd/opentree/cmd/new.go` — use agent properly
- `cmd/opentree/cmd/issue.go` — TASK.md writing currently in github package, should be agent's responsibility

---

## 3. State Management: Race Conditions and State Drift

**Current state**: `pkg/state/state.go` uses a JSON file with no file locking. Every mutation reads, modifies, and writes the entire file.

**Problems**:

- **No file locking.** If the TUI is running and a user runs `opentree new` from another terminal, both can write to `state.json` simultaneously, causing data loss. This is a real scenario since the tool is designed for power users running multiple terminals.

- **State drifts from reality.** The `Status` field (active/idle/stopped) is set manually but never reconciled with tmux state. If a user kills a tmux window with `Ctrl-C` or `tmux kill-window`, the state file still says "active." The TUI does some reconciliation during refresh, but the CLI commands don't.

- **Every write serializes the full state.** With many workspaces, this becomes increasingly wasteful, though admittedly the file is small.

**Proposed changes**:

1. **Add file locking** using `syscall.Flock` or a `.lock` file pattern. This prevents concurrent corruption.

2. **Derive status from tmux state** instead of storing it. The `Status` field should be computed by checking if the tmux window exists and has recent activity, not manually set. This eliminates state drift entirely.

3. **Add a `Reconcile()` method** that validates state against git worktrees and tmux windows on startup, removing orphaned entries.

**Files to modify**:
- `pkg/state/state.go` — add file locking, reconcile method
- `pkg/tui/tui.go` — simplify status computation (already partially does this)

---

## 4. The 1706-Line TUI Monolith

**Current state**: `pkg/tui/tui.go` contains ALL TUI logic: model definition, 40+ message types, update handler (giant switch statement), view rendering, styles, async commands, and business logic.

**Problems**:

- **Untestable business logic.** Workspace creation, deletion, PR creation — all embedded in TUI command functions that return `tea.Msg`. You can't test "create a workspace" without the entire Bubble Tea runtime.

- **State machine complexity.** The model has 15+ boolean/enum flags (`creating`, `prCreating`, `deleting`, `diffViewing`, `confirmingDelete`, `confirmingBatchDelete`, `batchDeleteStep`, etc.) that interact in undocumented ways. This is a classic symptom of a state machine that needs explicit states.

- **Mixed responsibilities.** The TUI file directly constructs worktree managers, tmux controllers, state stores, and github clients. It's both the presentation layer and the application layer.

**Proposed changes**:

1. **Extract business logic into a `pkg/workspace/` package** — a `WorkspaceService` that encapsulates the create/delete/diff/PR workflow. The TUI and CLI commands both call this service instead of directly orchestrating multiple packages.

```go
type WorkspaceService struct {
    worktrees  *worktree.Manager
    processes  process.ProcessManager
    state      *state.Store
    github     *github.PRManager
    config     *config.Config
}

func (s *WorkspaceService) Create(name, baseBranch string) (*state.Workspace, error)
func (s *WorkspaceService) Delete(name string, deleteBranch bool) error
func (s *WorkspaceService) CreatePR(name, title, body string) (string, error)
func (s *WorkspaceService) List() ([]*WorkspaceInfo, error)  // enriched with tmux/git state
```

2. **Split the TUI file** into at least:
   - `pkg/tui/model.go` — model struct, messages, init
   - `pkg/tui/update.go` — update logic
   - `pkg/tui/view.go` — rendering
   - `pkg/tui/styles.go` — style definitions
   - `pkg/tui/commands.go` — async tea.Cmd functions

3. **Replace boolean flags with explicit view states**:
```go
type ViewState int
const (
    ViewList ViewState = iota
    ViewCreating
    ViewCreatingFromIssue
    ViewDeleting
    ViewBatchDeleting
    ViewDiff
    ViewPRCreating
    ViewHelp
    ViewError
)
```

**Files to modify**:
- Create `pkg/workspace/workspace.go` — service layer
- Split `pkg/tui/tui.go` into multiple files
- Update `cmd/opentree/cmd/*.go` to use workspace service

---

## 5. Duplicated Concepts and Leaky Abstractions

**Current state**: Several concepts are duplicated across packages with subtle inconsistencies.

**Problems**:

- **Branch name → directory name mapping** (`/` → `-`) is done in both `worktree.go:31` and `tmux.go:314-318`, plus in multiple CLI commands. If these ever diverge, workspaces break silently.

- **Git repo root discovery** happens independently in `worktree.Manager.ensureGitRepo()` and `tmux.Controller.repoName()`. Both shell out to `git rev-parse --show-toplevel`.

- **Config's `BaseDir` field is ignored.** `config.WorktreeConfig.BaseDir` can be set to any value, but `worktree.New()` hardcodes `.opentree`. The config value is never passed through.

- **Diff inconsistency.** `Diff()` compares merge-base to working tree (includes uncommitted), while `DiffFull()` compares merge-base to HEAD (excludes uncommitted). There's also a separate `DiffUncommitted()`. The naming doesn't communicate this difference.

**Proposed changes**:

1. **Centralize name sanitization** into a single utility function that both worktree and tmux packages use.

2. **Pass repo root as a constructor parameter** to all packages instead of each one discovering it. The CLI/TUI should resolve it once and inject it.

3. **Wire up the config properly.** `worktree.New()` should accept `baseDir` as a parameter from config.

4. **Clarify diff API naming**: `DiffStat()`, `DiffFull()` → `DiffFullCommitted()`, `DiffIncludingWorktree()`, or similar names that communicate scope.

**Files to modify**:
- `pkg/worktree/worktree.go` — accept baseDir and repoRoot params
- `pkg/tmux/tmux.go` — accept repoRoot, share sanitization
- `cmd/opentree/cmd/*.go` — resolve repo root once, pass to constructors
- `pkg/config/config.go` — no changes needed, just wire it through

---

## 6. Missing Structured Communication with Agents

**Current state**: The only "communication" with agents is writing TASK.md before launch and scraping tmux pane output via `capture-pane`.

**Problem**: This is the biggest gap preventing opentree from being the "ultimate" parallel productivity tool. Without structured communication:
- You can't know when an agent is done
- You can't know if an agent succeeded or failed
- You can't aggregate results across workspaces
- The 5-second polling for preview is wasteful and lossy

**Proposed change** (minimal, not a new feature — this is fixing the communication architecture):

1. **Define a completion signal convention**: Agents write a `.opentree-status.json` file in the worktree when they complete:
```json
{"status": "done", "summary": "Implemented auth flow", "files_changed": 5}
```

2. **Watch for this file** using `fsnotify` instead of polling tmux panes for status. This is more reliable, testable, and agent-agnostic.

3. **Keep `capture-pane` for the live preview** — that's a legitimate use of tmux. But don't derive workspace status from it.

**Files to modify**:
- `pkg/agent/agent.go` or new `pkg/workspace/` — define status file convention
- `pkg/tui/tui.go` — watch for status file changes
- Documentation — describe the convention for agent authors

---

## 7. ~~gh CLI Inefficiency~~ (DONE)

~~**Current state**: `PRManager.IsInstalled()` runs `gh --version` on every call. Multiple methods check installation redundantly.~~

~~**Proposed change**: Cache the result of `IsInstalled()` using `sync.Once`, similar to how tmux caches `repoName`.~~

**Implemented**: Added `sync.Once` + `ghInstalled bool` fields to `PRManager`. `IsInstalled()` now shells out once and caches the result for all subsequent calls.

**File modified**: `pkg/github/github.go`

---

## Summary: Priority Order

| Priority | Issue | Impact | Effort |
|----------|-------|--------|--------|
| **P0** | Extract `WorkspaceService` from TUI | Enables testability, removes duplication between TUI and CLI | Medium |
| **P1** | Add file locking to state store | Prevents data corruption in real usage | Low |
| **P1** | Fix agent abstraction (dead code) | Removes confusion, enables proper validation | Low |
| **P2** | Introduce `ProcessManager` interface | Enables testing, future flexibility beyond tmux | Medium |
| **P2** | Centralize repo root + name sanitization | Eliminates subtle divergence bugs | Low |
| **P2** | Wire config.BaseDir through to worktree | Fixes broken config option | Low |
| **P3** | Split TUI into multiple files | Improves maintainability | Medium |
| **P3** | Define agent completion signal | Enables reliable status tracking | Medium |
| ~~**P3**~~ | ~~Cache gh CLI availability~~ | ~~Minor performance fix~~ | ~~Trivial~~ **DONE** |
