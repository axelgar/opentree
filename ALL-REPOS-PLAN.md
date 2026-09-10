# opentree — One dashboard across every repository: design & plan

> Status: **all four commits implemented.** `make check` green locally except
> shellcheck (not installed here; no script changed). Departures from the plan
> as written are under *Found during implementation*. Turned from `HANDOFF-ALL-REPOS.md` on
> 2026-09-10 at `aef3985`. Scope: `opentree` outside a repository, or
> `opentree --all` inside one, lists every repository's workspaces in one
> dashboard, each row live; `opentree list --all` does the same on the command
> line. Per-repo behaviour is unchanged.

## The thing worth fixing

The dashboard is scoped to the repository it is launched from. The agents
blocked in `~/src/web` are invisible from `~/src/api` until a second dashboard
is opened there. The data is already machine-wide — one state file per
repository under `~/.opentree/state/`, one socket directory keyed the same way
— only the dashboard is per-repo. The question a person running agents across
several services actually has is *which of my fourteen agents wants me?*, and
`b` already answers it, one repository at a time.

## Verified facts

Read from the code on 2026-09-10 at `aef3985`.

### 1. The handoff's facts hold, with three it missed

- `NewModel` (`pkg/tui/model.go:376`) resolves one root, falls back to the cwd,
  and builds one `worktree.Manager`, `state.Store`, `workspace.Service`. The
  Model holds them as `svc`, `worktreeMgr`, `stateStore`, `prMgr`, `cfg`,
  `repoRoot`. `m.stateStore` ×10, `m.worktreeMgr` ×3, `m.prMgr` ×2 are all
  row-bound; `m.svc` ×27 mostly so; `m.repoRoot` ×25 splits between row-bound
  (chat sockets, skills.Missing, portless host) and repository-bound (the
  Skills tab, creation).
- **Missed 1 — tmux is cwd-bound.** `Controller.repoName()`
  (`pkg/tmux/tmux.go:520`) calls `gitutil.RepoRoot()` from the process cwd to
  name the session. A controller built for another repository would list and
  attach to *this* repository's session. `tmux.New(prefix)` has 22 test
  callers and 3 real ones.
- **Missed 2 — gh is cwd-bound.** `ghRun(dir, …)` (`pkg/github/github.go:20`)
  is called with `""` by `FetchPRReviews`, `CreatePR`, `UpdatePR`,
  `GetPRStatus`, `GetFullPRStatus`, `GetPRCIStatus`, `GetIssue`; only
  `GetBranchAndPRStatus` and `FindPR` take a `repoDir`. `PRManager` has no
  directory. `github.New()` has 5 test callers and 6 real ones.
- **Missed 3 — the litter is the lock.** `Store.Load()` takes the file lock
  first, and `withFileLock` (`state.go:438`) `MkdirAll`s the directory and
  creates `state.lock`. The dashboard's 10 s refresh calls `Load()`, so every
  cwd the dashboard was ever opened in outside a repository has a directory.
  On this machine: 34 state directories, **17 hold only `state.lock`**.
- `workspace.New(repoRoot, cfg)` (`pkg/workspace/workspace.go:52`) already
  builds all five dependencies from a root and a config. The Service holds
  exactly the six things the Model holds.
- `config.Load(path)` with a non-empty path reads that file and skips the
  cwd walk (`config.go:408-411`). Per-repository config is therefore
  `config.Load(filepath.Join(root, "opentree.toml"))`.
- `State` has no root; `stateKeys = jsonKeys(State{})` (`state.go:234`) means
  a new field is known to the marshaller the moment it is declared, and the
  `unknown` passthrough means an older binary carries it through.
  `Store.Save` sets `Version` at `state.go:523`; the root goes beside it.
- `gitutil.RepoRoot()` (`gitutil.go:107`) is the git-common-dir dance from
  the cwd; `gitutil.Output(dir, args…)` runs git in a directory. Neither
  `gitutil` nor `state` imports the other; `state` importing `gitutil` is
  cycle-free.
- Rows: title is built at `view.go:368`; filter matches `ws.Name` at
  `view.go:633`; `sortedWorkspaces` groups by `FanoutGroup` or `Name`
  (`view.go:657`). `nextBlocked` runs over `visible`.
- Tests build `Model` without any service (`newTestModel`, `tui_test.go:34`),
  so no test wires the singletons; row-routed helpers are simply not called.
- `go tool deadcode ./cmd/opentree` is in `make check`: every new function
  needs its caller in the same commit.

### 2. The state directories on this machine

17 with a `state.json` (16 with one workspace, 1 with none), 17 with only a
lock. `~/.opentree/worktrees/` has one marker, for `frontend`. Route 3 of the
handoff (the marker) would recover nothing here that routes 1–2 do not.

## Decisions

| # | Decision |
|---|---|
| 1 | **Entry.** `opentree` outside any repository opens the all-repos view. `opentree --all` / `-A` inside one opens it too, with the cwd repository included. Plain `opentree` inside a repository is unchanged. No toggle key: the scope is a property of the launch, and a key would need the discovery to be re-runnable mid-session. |
| 2 | **Discovery is `state.Roots()`.** Lists `~/.opentree/state/*/state.json`, recovers the root from `repo_root` (route 1), else from any workspace's `worktree_dir` via `git -C … rev-parse --git-common-dir` (route 2, `gitutil.RepoRootIn`). Lock-only directories are skipped silently — they are not repositories. A file with workspaces and no recoverable root is dropped with a line in the error log. Route 3 is not built: with no live worktree there is nothing to show. Discovery runs once, at startup. |
| 3 | **`repo_root` is written on every save.** `State.RepoRoot string \`json:"repo_root,omitempty"\``; the Store remembers the root it was opened for and stamps it beside `Version`. The next save of every repository makes route 2 unnecessary for it. |
| 4 | **tmux and gh learn their root by a second constructor.** `tmux.NewIn(prefix, repoRoot)` seeds the cached repo name; `github.NewIn(dir)` sets the directory every `ghRun("")` now takes from the manager. `New()` keeps its meaning for the 27 callers that stand in the repository. `workspace.New` uses both. |
| 5 | **The Model holds `repos map[string]*workspace.Service`** keyed by root, plus `WorkspaceItem.RepoRoot`. `worktreeMgr`, `stateStore`, `prMgr` are deleted from the Model; the Service grows four one-line accessors (`Worktrees`, `State`, `GitHub`, `Config`) and `GitHubManager` gains `GetBranchAndPRStatus`. `svc`, `cfg`, `repoRoot` stay as *the cwd repository* — nil/empty outside one — for creation and the Skills tab. `svcFor(wsName)` routes a row to its Service, falling back to `m.svc`. |
| 6 | **Rows are prefixed `repo/` when more than one repository is showing.** The repo is the root's base name, what the tmux session is named after. The filter matches `repo/name`. The sort's group key gets the root as a prefix, so fan-out siblings and same-named workspaces in two repositories never interleave; within a repository every mode sorts as it did. |
| 7 | **Creation needs a repository.** `n`, `i`, `r` with no cwd repository answer with a transient error naming `opentree new`. Inside a repository with `--all` they create in the cwd repository, as today. |
| 8 | **`opentree list --all [--json]`** walks `state.Roots()`; the table gains a `REPO` column, and each JSON object gains `"repo"`. Without `--all` the output is byte-identical to today. |
| 9 | **Polling is unchanged.** One `gh` process per workspace per 30 s already skips merged-and-gone rows and non-GitHub remotes. Sixteen rows on this machine is sixteen processes every thirty seconds, which is what a single repository with sixteen workspaces already costs. Measure before spacing. |
| 10 | **The litter stops with decision 1.** Outside a repository no cwd store is built, so nothing locks a directory nobody asked for. The 17 existing lock-only directories are left alone; `rmdir ~/.opentree/state/*/` clears the empty ones after `rm` of the locks, and a prune is not built for it. |

### On decision 5, why not one Model per repository

Bubbletea has one model, one cursor, one keymap. Every per-row action already
takes the row; the only change is which Service it is handed. A map of
Services and a root on the row is the smallest change that makes the
single-repo case the same code path with one entry.

### On the name as the row's key

`ciStatus`, `selected`, `workspaceDeletingNames`, `workspaceIndex` and every
message are keyed by workspace name. Two repositories can each have a
`fix/typo`. `svcFor` and `workspaceIndex` take the first match, so a PR badge
or a state update for the second lands on the first. `ponytail:` comments mark
the two lookups; the fix, if it is ever needed, is a `(root, name)` key on the
messages that carry one.

## Commit sequence

Each commit is green on its own.

| # | Scope | Files | Size | Status |
|---|---|---|---|---|
| 0 | **This document.** | `ALL-REPOS-PLAN.md` | S | done |
| 1 | **tmux and gh take a root** (decision 4). `tmux.NewIn`, `github.NewIn`, `PRManager.dir`, `workspace.New` uses both. No visible change. | `pkg/tmux/tmux.go`, `pkg/github/github.go`, `pkg/workspace/workspace.go` | S | done |
| 2 | **Discovery and `list --all`** (decisions 2, 3, 8). `gitutil.RepoRootIn`, `State.RepoRoot` stamped on save, `state.Roots()`, the list command. Tests for `Roots` (route 1, route 2, lock-only skipped) and `RepoRootIn`. | `pkg/gitutil/gitutil.go`, `pkg/state/state.go`, `roots.go`, `roots_test.go` (new), `cmd/opentree/cmd/list.go` | M | done |
| 3 | **The dashboard** (decisions 1, 5, 6, 7). `--all` flag, `tui.Run(all)`, `NewModel(all)`, `repos`, `WorkspaceItem.RepoRoot`, `svcFor`, routing at every row-bound site, row prefix, filter, sort, creation guard. Tests for prefix, filter and grouping. | `cmd/opentree/main.go`, `pkg/tui/model.go`, `commands.go`, `update.go`, `view.go`, `agentctl.go`, `servers.go`, `helpers.go`, `tui_test.go` | L | done |
| 4 | **Docs.** README's TUI section says how to open it; `list --all`; this file's statuses. | `README.md`, `ALL-REPOS-PLAN.md` | S | done |

### Found during implementation

- **`--git-common-dir` is relative from the top level.** `RepoRootIn(dir)`
  first resolved `.git` against the process cwd, which is where the old
  `RepoRoot()` happened to stand. It now joins against `dir` first.
- **The state directories on this machine are test residue.** All sixteen
  files with a workspace hold a `feat-x` with a zero `created_at` and no
  `worktree_dir` — written by a test somewhere that does not set `HOME`.
  `Roots` reports each as untraceable, which is right, and loud: sixteen lines
  on every `list --all` until they are removed. The leaking test is a separate
  fix.
- **Service lookups live inside the command closures**, as the fields they
  replaced did. Tests build a Model with no Service and press every key;
  hoisting `m.svcOf(ws).Worktrees()` out of the closure made building the
  command need one.
- **`baseOr` takes the row**, not the base string: the default base is per
  repository now, and the row is what knows its repository.

## Not built

- Creating workspaces from the all-repos view; a repo picker.
- A registry of repositories, or re-discovery mid-session: restart to see a
  repository whose first workspace was made after launch.
- Route 3 (the worktree marker).
- Cross-repo Skills/Agents/Plugins/Servers tabs: the Skills tab stays the cwd
  repository's, and is empty outside one.
- Spacing the 30 s poll by repository (decision 9).

## Risks

- **Name collisions** across repositories misroute a badge or a state update
  to the first row of that name (see above). Rare — the names are branch
  names — and the damage is a wrong PR badge, corrected by the next poll of
  the right row.
- **The Agents tab outside a repository**: `enter` persists the selection via
  `config.SetKeys("")`, which resolves to `opentree.toml` in the cwd — in
  `$HOME`, that is a stray file. `g` (global) is the right key there. Not
  guarded in this change.
- **A stale `repo_root`** after a repository is moved: route 1 answers with a
  directory that no longer exists. `Roots` stats the root and falls through to
  route 2, then drops it.
