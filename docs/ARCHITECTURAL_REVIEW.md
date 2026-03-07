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

## 2. ~~The Dead Agent Abstraction~~ (DONE)

~~**Current state**: `pkg/agent/agent.go` defines an `Agent` interface with `Start(workdir) (*exec.Cmd, error)`, but the returned `*exec.Cmd` is **never executed**. Instead, the tmux controller receives the command name as a string and runs it via `send-keys`. The entire agent package is dead code in terms of its actual contract.~~

**Implemented**: Deleted `pkg/agent/` entirely (was never imported by any consumer). Added `Validate()` and `CommandLine()` methods to the existing `config.AgentConfig` which was already the real source of truth. `Validate()` checks the agent binary exists on PATH via `exec.LookPath`. Tests added.

**Files modified**:
- `pkg/agent/` — deleted (dead code)
- `pkg/config/config.go` — added `Validate()` and `CommandLine()` methods
- `pkg/config/config_test.go` — added 5 new tests

---

## 3. ~~State Management: Race Conditions and State Drift~~ (FILE LOCKING DONE)

~~**No file locking.** If the TUI is running and a user runs `opentree new` from another terminal, both can write to `state.json` simultaneously, causing data loss.~~

**Implemented**: Added `syscall.Flock`-based file locking via `.opentree/state.lock`. All mutating operations (`AddWorkspace`, `UpdateWorkspace`, `DeleteWorkspace`) now perform atomic read-modify-write under an exclusive lock — reloading fresh state from disk before applying changes. `Load()` uses a shared lock. Writes use atomic temp-file-and-rename pattern to prevent partial reads. Concurrency tests prove no data loss under 10 parallel writers.

**Files modified**:
- `pkg/state/state.go` — added `withFileLock()`, `mutate()`, `loadFromDisk()`, `atomicWrite()`
- `pkg/state/state_test.go` — added `TestConcurrentWriters`, `TestAtomicWrite_NoPartialReads`

**Still open** (separate work items):
- State drifts from reality — `Status` field is manually set, not derived from tmux state
- No `Reconcile()` method to clean up orphaned entries on startup

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

## 5. ~~Duplicated Concepts and Leaky Abstractions~~ (CENTRALIZATION DONE)

~~**Current state**: Several concepts are duplicated across packages with subtle inconsistencies.~~

~~**Problems**:~~

~~- **Branch name → directory name mapping** (`/` → `-`) is done in both `worktree.go:31` and `tmux.go:314-318`, plus in multiple CLI commands. If these ever diverge, workspaces break silently.~~

~~- **Git repo root discovery** happens independently in `worktree.Manager.ensureGitRepo()` and `tmux.Controller.repoName()`. Both shell out to `git rev-parse --show-toplevel`.~~

~~- **Config's `BaseDir` field is ignored.** `config.WorktreeConfig.BaseDir` can be set to any value, but `worktree.New()` hardcodes `.opentree`. The config value is never passed through.~~

**Implemented**:
1. Created `pkg/gitutil/gitutil.go` with `RepoRoot()` and `SanitizeBranchName()` — single source of truth for repo root discovery and branch name sanitization (both `/` and `:` → `-`).
2. Changed `worktree.New()` signature to `New(repoRoot, baseDir string)` — no more self-discovery or hardcoded `.opentree`. Config's `BaseDir` is now properly wired through.
3. Removed `worktree.Manager.ensureGitRepo()` entirely.
4. Updated all CLI commands (`new`, `issue`, `delete`, `pr`, `list`, `diff`, `completions`) and TUI to resolve repo root once via `gitutil.RepoRoot()` and pass it to constructors.
5. `tmux.Controller.repoName()` and `tmux.sanitizeWindowName()` now delegate to `gitutil` instead of reimplementing.

**Files created**:
- `pkg/gitutil/gitutil.go` — `RepoRoot()`, `SanitizeBranchName()`
- `pkg/gitutil/gitutil_test.go` — tests

**Files modified**:
- `pkg/worktree/worktree.go` — new signature `New(repoRoot, baseDir)`
- `pkg/worktree/worktree_test.go` — updated for new signature
- `pkg/tmux/tmux.go` — delegates to `gitutil`
- `pkg/tui/tui.go` — uses `gitutil.RepoRoot()`, `gitutil.SanitizeBranchName()`
- `cmd/opentree/cmd/new.go`, `issue.go`, `delete.go`, `pr.go`, `list.go`, `diff.go`, `completions.go` — all updated

**Still open** (separate work item):
- Diff API naming inconsistency (`Diff()` vs `DiffFull()` vs `DiffUncommitted()`) — names don't communicate scope difference

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
| ~~**P1**~~ | ~~Add file locking to state store~~ | ~~Prevents data corruption in real usage~~ | ~~Low~~ **DONE** |
| ~~**P1**~~ | ~~Fix agent abstraction (dead code)~~ | ~~Removes confusion, enables proper validation~~ | ~~Low~~ **DONE** |
| **P2** | Introduce `ProcessManager` interface | Enables testing, future flexibility beyond tmux | Medium |
| ~~**P2**~~ | ~~Centralize repo root + name sanitization~~ | ~~Eliminates subtle divergence bugs~~ | ~~Low~~ **DONE** |
| ~~**P2**~~ | ~~Wire config.BaseDir through to worktree~~ | ~~Fixes broken config option~~ | ~~Low~~ **DONE** |
| **P3** | Split TUI into multiple files | Improves maintainability | Medium |
| **P3** | Define agent completion signal | Enables reliable status tracking | Medium |
| ~~**P3**~~ | ~~Cache gh CLI availability~~ | ~~Minor performance fix~~ | ~~Trivial~~ **DONE** |
