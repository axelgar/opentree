# opentree

**Orchestrate parallel AI coding sessions in isolated git worktrees.**

Think [Conductor](https://conductor.build), but for the terminal.

opentree is a cross-platform CLI tool that manages multiple AI coding agent sessions. Each session runs in an isolated git worktree with its own branch, orchestrated via tmux. Perfect for working on multiple features/fixes simultaneously without context-switching overhead.

![opentree TUI](docs/screenshot.png)

## Features

- **🌳 Isolated Workspaces**: Each workspace = git worktree + branch + tmux window
- **🤖 Agent Integration**: Launch OpenCode (or other agents) automatically in each workspace
- **📊 TUI Dashboard**: Interactive terminal UI for managing workspaces (press `?` for help)
- **🔀 Parallel Development**: Work on multiple branches simultaneously without checkout overhead
- **📝 Diff Viewer**: Review changes before committing
- **🚀 PR Creation**: Create GitHub PRs directly from workspace
- **🧹 Clean Lifecycle**: Archive workspaces after merge, keeping your repo tidy

## Requirements

- **Git** (2.5+) - for worktree support
- **tmux** (2.0+) - for session orchestration
- **OpenCode** (optional) - default coding agent ([install](https://github.com/anomalyco/opencode))
- **GitHub CLI** (`gh`) (optional) - for PR creation ([install](https://cli.github.com/))

## Installation

### From Source

```bash
git clone https://github.com/yourusername/opentree.git
cd opentree
go build -o opentree ./cmd/opentree
sudo mv opentree /usr/local/bin/
```

### Using Go Install

```bash
go install github.com/yourusername/opentree/cmd/opentree@latest
```

## Quick Start

```bash
# Navigate to any git repository
cd ~/my-project

# Launch TUI dashboard (interactive mode)
opentree

# Or use CLI commands directly
opentree new feat/add-auth       # Create workspace
opentree list                    # List all workspaces
opentree attach feat/add-auth    # Attach to tmux window
opentree diff feat/add-auth      # Review changes
opentree pr feat/add-auth        # Create GitHub PR
opentree delete feat/add-auth    # Clean up workspace
```

## Usage

### TUI Mode (Interactive)

Run `opentree` without arguments to launch the interactive dashboard:

```bash
opentree
```

**Keybindings:**
- `n` - Create new workspace
- `Enter` - Attach to selected workspace
- `d` - Show diff for selected workspace
- `p` - Create PR for selected workspace
- `x` - Delete selected workspace
- `?` - Show help
- `q` - Quit

### CLI Mode (Direct Commands)

#### Create a Workspace

```bash
opentree new <branch-name> [flags]

# Examples
opentree new feat/user-auth           # Create workspace with branch
opentree new fix/login-bug --base dev # Branch off 'dev' instead of 'main'
```

Creates:
1. Git worktree at `.opentree/<branch-name>/`
2. New branch (or checks out existing)
3. tmux window in `opentree-<repo>` session
4. Launches OpenCode agent in the workspace

#### List Workspaces

```bash
opentree list
```

Shows table with: branch name, status, last modified time.

#### Attach to Workspace

```bash
opentree attach <branch-name>
```

Attaches to the workspace's tmux window. Detach with `Ctrl+b d`.

#### Show Diff

```bash
opentree diff <branch-name>
```

Shows `git diff` between workspace and base branch.

#### Create Pull Request

```bash
opentree pr <branch-name> [flags]

# Examples
opentree pr feat/user-auth                                    # Interactive prompts
opentree pr feat/user-auth --title "Add user auth" --body "..." # Non-interactive
```

Requires GitHub CLI (`gh`) to be authenticated.

#### Delete Workspace

```bash
opentree delete <branch-name> [flags]

# Examples
opentree delete feat/user-auth              # Keep branch
opentree delete feat/user-auth --delete-branch # Delete branch too
```

Removes worktree, kills tmux window, removes state. Optionally deletes branch.

## Configuration

Create `opentree.toml` in your repo root or `~/.config/opentree/opentree.toml`:

```toml
[worktree]
base_dir = ".opentree"        # Where to store worktrees (relative to repo root)
base_branch = "main"          # Default base branch

[agent]
command = "opencode"          # Command to launch agent
args = []                     # Additional arguments

[github]
default_base = "main"         # Default PR base branch
```

### Using Different Agents

To use a different coding agent instead of OpenCode:

```toml
[agent]
command = "cursor"            # Or "aider", "continue", etc.
args = ["--some-flag"]
```

Or override per workspace:

```bash
OPENTREE_AGENT_COMMAND="cursor" opentree new feat/my-feature
```

## How It Works

1. **Worktrees**: Git worktrees allow multiple checkouts of the same repo in different directories. Each workspace lives in `.opentree/<branch-name>/`.

2. **tmux Orchestration**: A single tmux session (`opentree-<repo>`) manages all workspaces. Each workspace = one tmux window. Attach to work, detach to switch.

3. **State Persistence**: Workspace metadata (branch, created time, agent PID) stored in `.opentree/state.json`.

4. **Agent Integration**: When creating a workspace, opentree launches your configured agent (default: OpenCode) inside the tmux window, ready to code.

## Workflow Example

```bash
# Start working on a feature
opentree new feat/add-dark-mode

# (tmux attaches automatically, OpenCode launches)
# (work with AI agent, make changes...)
# (detach with Ctrl+b d when done)

# While that's building, start a bugfix in parallel
opentree new fix/header-overflow

# (work on bugfix...)
# (detach)

# Review changes for first feature
opentree diff feat/add-dark-mode

# Create PR when ready
opentree pr feat/add-dark-mode --title "Add dark mode toggle"

# Clean up after merge
opentree delete feat/add-dark-mode --delete-branch
```

## Troubleshooting

### "Error: not a git repository"

opentree must be run from inside a git repository. Navigate to your project root first.

### "Error: tmux not found"

Install tmux:
- **macOS**: `brew install tmux`
- **Ubuntu/Debian**: `sudo apt install tmux`
- **Arch**: `sudo pacman -S tmux`

### "Error: opencode not found"

Install OpenCode from [github.com/anomalyco/opencode](https://github.com/anomalyco/opencode), or configure a different agent in `opentree.toml`.

### "Error: gh not found"

Install GitHub CLI from [cli.github.com](https://cli.github.com/), then authenticate:

```bash
gh auth login
```

### Workspaces not appearing in TUI

State file might be corrupted. Check `.opentree/state.json` or delete and recreate workspaces.

## Contributing

Contributions welcome! Please open an issue or PR.

### Development Setup

```bash
git clone https://github.com/yourusername/opentree.git
cd opentree
go mod download
go build -o opentree ./cmd/opentree
./opentree --help
```

### Architecture

See [PLAN.md](PLAN.md) for detailed architecture documentation.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Acknowledgments

- Inspired by [Conductor.build](https://conductor.build) by Sahil Lavingia
- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI framework
- Integrates with [OpenCode](https://github.com/anomalyco/opencode) AI coding agent
