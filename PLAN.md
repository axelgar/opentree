# opentree — Architecture Plan v0.1

> Conductor for the terminal. Orchestrate parallel AI coding sessions in isolated git worktrees via tmux.

## Overview

A standalone Go CLI that manages parallel AI coding sessions. Each session runs in an isolated git worktree with its own opencode instance, managed via tmux.

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    opentree (Go binary)                   │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │  Workspace   │  │    tmux      │  │   Git Worktree │  │
│  │  Manager     │  │  Controller  │  │   Manager      │  │
│  └──────┬──────┘  └──────┬───────┘  └───────┬────────┘  │
│         │                │                   │           │
│  ┌──────┴──────────────────┴───────────────────┴──────┐   │
│  │              State Store (JSON)                     │   │
│  └────────────────────────────────────────────────────┘   │
│                                                          │
│  ┌────────────────────────────────────────────────────┐   │
│  │          TUI Dashboard (Bubble Tea)                │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐          │   │
│  │  │ workspace│ │ workspace│ │ workspace│  ...      │   │
│  │  │ feat/auth│ │ fix/login│ │ feat/api │          │   │
│  │  │ ● active │ │ ○ idle   │ │ ● active │          │   │
│  │  │ +12 -3   │ │ +45 -8   │ │ +7  -1   │          │   │
│  │  └──────────┘ └──────────┘ └──────────┘          │   │
│  └────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────┘
                           │
            ┌──────────────┼──────────────┐
            ▼              ▼              ▼
     ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
     │ tmux window │ │ tmux window │ │ tmux window │
     │  worktree/  │ │  worktree/  │ │  worktree/  │
     │  feat-auth  │ │  fix-login  │ │  feat-api   │
     │             │ │             │ │             │
     │  opencode   │ │  opencode   │ │  opencode   │
     │  (running)  │ │  (running)  │ │  (running)  │
     └─────────────┘ └─────────────┘ └─────────────┘
```

## Modules

### 1. Git Worktree Manager (`pkg/worktree/`)
- `Create(branchName, baseBranch)` → creates `.opentree/<branchName>/` worktree
- `List()` → lists all opentree-managed worktrees
- `Delete(branchName, deleteBranch bool)` → prunes worktree, optionally deletes branch
- `Diff(branchName)` → returns diffstat vs base branch
- Worktrees stored in `.opentree/` inside the repo root (added to `.gitignore`)

### 2. tmux Controller (`pkg/tmux/`)
- Single tmux session: `opentree-<repo-name>`, each workspace = a window
- `CreateWindow(name, workdir, cmd)` → new tmux window running agent in worktree dir
- `ListWindows()` → list active windows with metadata
- `AttachWindow(name)` → switch to that tmux window
- `KillWindow(name)` → stop the session
- `CapturePane(name)` → grab recent output for status display

### 3. Agent Interface (`pkg/agent/`)
- `Agent` interface: `Start(workdir string) Command`, `Name() string`
- Default: `OpenCodeAgent` — runs `opencode` in given directory
- Future: `ClaudeCodeAgent`, `AiderAgent`, etc.
- Config-driven via `opentree.toml`

### 4. State Store (`pkg/state/`)
- Tracks: name, branch, base branch, created_at, status, agent
- JSON file at `.opentree/state.json`
- Reconstructs from tmux + git state on startup

### 5. TUI Dashboard (`pkg/tui/`)
- Bubble Tea app with dashboard view
- Workspace cards: branch name, status, diff summary, last activity
- Keybindings:
  - `n` — new workspace
  - `Enter` — attach to workspace (switch to tmux window)
  - `d` — view diff
  - `p` — create PR
  - `x` — archive/delete workspace
  - `q` — quit dashboard (sessions keep running)
  - `?` — help

### 6. PR Integration (`pkg/github/`)
- Uses `gh` CLI under the hood
- `CreatePR(branch, baseBranch, title, body)`
- Dashboard shows PR status if exists

## CLI Interface

```
opentree                   # Launch TUI dashboard
opentree new <branch>      # Quick create workspace
opentree list              # List workspaces (non-TUI)
opentree attach <branch>   # Attach to workspace tmux window
opentree delete <branch>   # Archive workspace
opentree diff <branch>     # Show diff for workspace
opentree pr <branch>       # Create PR for workspace
```

## UX Flow

1. `cd` into repo → run `opentree`
2. Dashboard shows existing workspaces (or empty state)
3. `n` → enter branch name → opentree creates worktree + tmux window + launches opencode
4. `Enter` → attached to tmux window, interacting with opencode directly
5. Detach (custom keybind or Ctrl-B d) → back to dashboard
6. `p` to create PR, `x` to archive when done

## Config (`opentree.toml`)

```toml
[agent]
command = "opencode"
args = []

[worktree]
base_dir = ".opentree"
default_base = "main"

[tmux]
session_prefix = "opentree"

[github]
auto_push = false
```

## Dependencies

- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss` — TUI styling
- `github.com/charmbracelet/bubbles` — TUI components
- `github.com/spf13/cobra` — CLI framework
- `github.com/pelletier/go-toml/v2` — config parsing

External tools on PATH: `git`, `tmux`, `gh` (optional), `opencode`

## NOT in v0.1

- Built-in terminal emulator
- Multi-repo support
- Workspace from GitHub issue/PR
- Agent activity monitoring / token usage
- Workspace checkpoints / revert
