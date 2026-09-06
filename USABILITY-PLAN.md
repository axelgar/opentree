# opentree — Usability gaps: design & plan

> Status: **all thirteen commits implemented.** `make check` green; the
> race suite green on Linux. Departures from the plan as written are under
> *Found during implementation*.
> Scope: the basic things that stop opentree being usable day to day on a real
> project — not features. Two came from use (text cannot be selected in the
> chat; the worktrees under `.opentree` collide with the project's own tools).
> The rest came from walking every surface with the question those two share:
> *at the moment the agent hands a person something, what can they do with it?*

## The thing worth fixing

opentree's model is sound: one worktree per branch, one chat per worktree, the
dashboard over all of them. What is missing is the plumbing between that model
and the person at the keyboard. The agent says "run the tests and paste me the
failure", and there is no way to copy what it wrote, no way into the worktree
without knowing the sanitised path, and when the tests are run from the main
checkout they pick up five extra copies of the project. None of that is a
feature. All of it decides whether the tool gets used.

## Verified facts

Read from the code on 2026-09-06 at `26f1cbb`. File:line references are to
that commit.

### 1. Both views take the mouse, and tmux hands it to them

- The chat and the dashboard start bubbletea with `tea.WithMouseCellMotion()`
  (`pkg/chat/model.go:348`, `pkg/tui/model.go:398`), and opentree turns
  `mouse on` for its own tmux session (`pkg/tmux/tmux.go:568`). The comments
  say why: without it the wheel scrolls the terminal's scrollback out from under
  the alt screen. It is also exactly what disables the terminal's own
  drag-to-select — with reporting on, a drag goes to the program, and the
  program only reads the wheel (`pkg/chat/update.go:196`,
  `pkg/tui/update.go:1225`).
- Nothing in the chat copies anything. The one clipboard write in the codebase
  is the dashboard error log's `c` (`pkg/tui/clipboard.go`,
  `pkg/tui/commands.go:22`); the chat has only the read side, for images
  (`pkg/chat/clipboard.go`).
- Terminals do have an escape hatch — shift-drag bypasses mouse reporting in
  xterm, kitty, Alacritty, WezTerm and GNOME Terminal, and iTerm2 uses
  option-drag — and nobody is told. Neither `?` nor the README mentions it.
- Clicks arrive and are dropped: a click on a workspace row, on
  `[a] Allow once`, or on `… 42 more lines · ctrl+x` does nothing.

### 2. Worktrees live inside the working tree, and only git is told

- The default is `base_dir = ".opentree"` (`pkg/config/config.go:155`), joined
  to the repository root (`pkg/workspace/workspace.go:118`). Every workspace is
  a complete checkout of the project under `<repo>/.opentree/<branch>/`.
- Git is handled: `excludeBaseDir` writes `/.opentree/` into
  `.git/info/exclude` (`pkg/worktree/worktree.go:114`).
- Nothing else is, and this repository already shows the cost twice.
  `.golangci.yml:122` excludes `\.opentree/` — "linting them lints the same
  code twice, and at whatever revision the worktree happens to sit on" — and
  the Makefile's `fmt` target runs over `git ls-files` because a plain `find`
  would reformat the worktrees. Go's `./...` skips dot-directories, so a Go
  project sees the mild version. A JavaScript, TypeScript or Python project
  sees the full one: jest and vitest collect the nested tests, and
  jest-haste-map refuses two `package.json` files with one name; `tsc` with
  `include: ["**/*"]` compiles the copies; eslint and prettier walk them;
  pytest collects them and fails with "import file mismatch"; `docker build .`
  ships them in the context; vite, next and nodemon watch them and rebuild
  every time an agent saves. And node resolves `require` upward: a worktree
  that skipped its `setup` finds `<repo>/node_modules` two levels up and works
  by accident until it does not.
- The way out exists, but only for people who read the source: `base_dir`
  accepts a path outside the repository from the **global** config — a
  repository config's non-local path is dropped (`config.go:446`), for the
  good reason the comment gives. The README shows only `.opentree` (line 622);
  the `../worktrees` layout a code comment calls "documented"
  (`worktree.go:174`) is documented nowhere.
- `state.json` is always at `<repo>/.opentree/state.json`
  (`pkg/state/state.go:355`), whatever `base_dir` says.

### 3. A new workspace branches from whatever the local base happens to be

- `worktree.Create` runs `git worktree add -b <branch> -- <path> <base>` with
  the local ref (`pkg/worktree/worktree.go:238`). No fetch precedes it; the
  only `git fetch` on any creation path is `--remote`'s (`worktree.go:268`).
  A `main` last pulled yesterday makes a workspace that starts behind origin,
  and its PR later carries commits already merged, or conflicts with them.

### 4. Nothing gets a person into a worktree

- The workspace's tmux window *is* `opentree chat`
  (`pkg/workspace/workspace.go:150-176`). There is no shell window — only the
  dev server's `<name>:run` (`pkg/workspace/server.go:24`). The CLI has no
  `path`, `shell`, `open` or `exec`. The dashboard shows the worktree path
  nowhere; only an empty chat's banner does (`pkg/chat/view.go:846`).
  `opentree list` prints name, branch, base and status
  (`cmd/opentree/cmd/list.go:64`), not the path — and the path is not the
  branch name: `feat/x` lives at `feat-x`.

### 5. Smaller chat gaps

- `↑` recalls messages from this process only (`pkg/chat/history.go:8`); a
  reopened window has forgotten them.
- No find in the transcript. Scrolling is pgup/pgdn and shift+↑/↓
  (`pkg/chat/keys.go:100-120`); nothing reaches the top or the bottom in one
  press.
- No way to get the conversation out. The transcript exists only as the
  viewport's rendered content (`pkg/chat/view.go:726`); `/resume` reopens the
  agent's session but never writes a file.

### 6. Smaller dashboard and CLI gaps

- The README promises "Archive workspaces after merge". No `archive` exists;
  `x` deletes one workspace at a time, merged or not.
- The diff viewer scrolls by ↑/↓ and the wheel only (`pkg/tui/view.go:227`).
- `MergeConflicts` is tracked and drawn (`pkg/state/state.go:73`,
  `pkg/tui/view.go:462`), and there is no action that brings the base in —
  you ask the agent in words.

## Decisions

| # | Decision |
|---|---|
| 1 | **Worktrees leave the working tree by default.** The new default `base_dir` is `~/.opentree/worktrees/<repo>`, where `<repo>` is the repository directory's name. `base_dir` may be absolute, and `~` expands. In-repo stays a supported setting for anyone who wants it. |
| 1a | Two clones with one directory name must not share a base. The base directory carries a `.repo` marker holding the root it belongs to; a mismatch falls back to `<repo>-<8 hex of fnv32(root)>`, the scheme the sockets already use (`pkg/chat/socket.go:214`). |
| 2 | **Existing workspaces stay where they are.** `Service.WorktreePath` prefers `state.Workspace.WorktreeDir` when that directory exists, and falls back to the configured base; the config says where a *new* worktree goes. Delete already reasons this way (`worktree.go:325`). No migration command — the old layout drains as workspaces are deleted. |
| 3 | **`state.json` stays in `<repo>/.opentree/`.** Two small git-excluded files that no test runner cares about; moving them touches every reader. Listed under *Not built*. |
| 4 | **`doctor` says where worktrees go**, and when the base is inside the working tree, says what that costs and which line moves it. |
| 5 | **Fetch before branching, best-effort.** `new` runs `git fetch origin <base>` under a 15s timeout, then branches from `origin/<base>` when it exists, else from the local ref. `--no-fetch` skips it. No `origin`, or no network, is a notice on the way past, never an error. |
| 6 | **The chat draws its own selection.** It keeps the mouse (the wheel is why it has it) and does what a terminal does: drag selects across the viewport, release copies, double-click takes a word, triple-click a line. Text comes from the rendered rows with ANSI stripped, so what is copied is what is on screen. |
| 7 | **One clipboard package.** `pkg/clipboard` takes the write side out of `pkg/tui/clipboard.go` — pbcopy, wl-copy, xclip — and always also emits OSC 52 (`termenv.Copy`, already a dependency), so copying works over SSH and inside tmux, whose default `set-clipboard external` passes it through. The image read stays in the chat. |
| 8 | **`ctrl+y` copies without a mouse.** A picker on the existing `picker.go` component: the last reply, each fenced block in it by language and length, the last tool's output, the whole conversation as markdown. `ctrl+y` is free — the textarea binds neither it nor `ctrl+l`. |
| 9 | **Shift-drag gets documented first.** One line in `?` and one in the README, in the first commit, because it works today. |
| 10 | **Clicks do what they look like.** Dashboard: click selects a row, double-click attaches. Chat: click a permission option to answer it; click a held-back tool row to expand it. Only things already drawn as choices. |
| 11 | **`t` opens a shell in the worktree** — a second tmux window `<name>:sh`, reused when it exists, the way `:run` is — and `opentree shell <ws>` does the same from the CLI. `/shell` does it from inside the chat. |
| 12 | **`opentree path <ws>`** prints the worktree path, so `cd "$(opentree path feat/x)"` works; `y` copies it from the dashboard; `e` opens the worktree in `$VISUAL`/`$EDITOR` through `tea.ExecProcess`, the Skills tab's precedent (`pkg/tui/skills.go:378`). `list` gains a PATH column. |
| 13 | **Sent messages persist per workspace**, in `~/.opentree/history/opentree-<hash>/<ws>` (the sockets' repo hash), the last 200, loaded when the chat starts. |
| 14 | **`ctrl+f` finds in the transcript.** The textarea's `ctrl+f` (forward one character) is given up; `→` does that. `ctrl+n`/`ctrl+p` step through matches while the find box is open; `esc` closes it where it stands. |
| 15 | **`/export` writes the conversation** as markdown to `~/.opentree/exports/<repo>/<ws>-<timestamp>.md` and says where. Never into the worktree, which would dirty the branch. |
| 16 | **`opentree sync <ws>` and `u` bring the base in**: `git fetch origin <base>`, then `git merge origin/<base>` in the worktree. Merge, not rebase — the branch may already be pushed and under review. A conflict stops and offers to hand it to the agent as a prompt. |
| 17 | **`delete --merged`** removes every workspace whose PR is merged, with the same dirty-tree confirmations `delete` has. The README's "archive" line is rewritten to describe what exists. |
| 18 | **The diff viewer gets pgup/pgdn and `g`/`G`.** |

### On decision 1, where the worktrees go

Three places were considered. Beside the repository
(`../<repo>.worktrees/`) is what most hand-rolled worktree setups do, and it
litters whichever directory holds the clone. Inside the repository is where
opentree is now, with the costs in fact 2. Under the user's own opentree
directory is what Conductor does (`~/conductor/workspaces/<repo>/`); it keeps
every project's worktrees in one place, and it is already where opentree keeps
everything else it owns per machine — `~/.opentree/tools`, `registry`,
`plugins`, `trust.json`. The path is longer in the chat header
(`~/.opentree/worktrees/myapp/fix-auth`), and decision 12 is what makes that
not matter.

### On decision 6, drawing selection versus releasing the mouse

The alternative is a key that calls `tea.DisableMouse` so the terminal's own
selection works, then takes the mouse back. Inside tmux that does not hand the
mouse to the terminal at all: tmux keeps `mouse on` and runs its own copy-mode
selection, which lands in tmux's paste buffer rather than the clipboard unless
the server's `set-clipboard` is `on`. Two modes, two behaviours depending on
where the chat runs, and a wheel that stops working in one of them. Drawing the
selection costs more code and behaves the same everywhere.

Bubbletea 1.3 reports press, motion-while-held and release with cell
coordinates (`tea.MouseEvent{X, Y, Action, Button}`), which is everything the
selection needs, and `x/ansi.Strip` is already imported by the chat.

## Commit sequence

Each commit is green on its own and ships something. Order is by what unblocks
day-to-day use, not by size.

| # | Scope | Files | Size | Status |
|---|---|---|---|---|
| 1 | **Say what already works.** `?` help and the README gain "shift-drag (option-drag in iTerm2) selects text" beside the scroll keys. | `pkg/chat/keys.go`, `pkg/chat/view.go`, `README.md` | S | done |
| 2 | **`pkg/clipboard`** (decision 7). `Write(text)` = platform tool + OSC 52; `pkg/tui` uses it for the error log. Tests: the tool table per platform, the OSC 52 bytes on a fake writer. | `pkg/clipboard/clipboard.go` (new), `pkg/tui/clipboard.go` (moved), `pkg/tui/commands.go` | S | done |
| 3 | **`ctrl+y` copy picker** (decision 8). Fenced blocks are cut from the entry's raw markdown, not the rendering, so indentation survives. A notice says what went: "copied 18 lines (go)". | `pkg/chat/copy.go` (new), `keys.go`, `update.go`, `view.go` | M | done |
| 4 | **Drag to select in the chat** (decision 6). Press anchors on a viewport cell; motion extends; release copies and the highlight clears a moment later. The highlight is an inverse style painted over the rendered rows. Double- and triple-click. The text = the rendered rows, ANSI stripped, trailing spaces trimmed, joined with newlines. | `pkg/chat/select.go` (new), `update.go`, `view.go`, `styles.go` | L | done |
| 5 | **Worktrees outside the working tree** (decisions 1–4). Default base under `~/.opentree/worktrees`; `~` and absolute paths accepted; `.repo` marker; `WorktreePath` honours `state.WorktreeDir`; `doctor` reports the layout; README gains "Where worktrees live". | `pkg/config/config.go`, `pkg/workspace/workspace.go`, `pkg/worktree/worktree.go`, `cmd/opentree/cmd/doctor.go`, `README.md` | M | done |
| 6 | **Fetch before branching** (decision 5). `--no-fetch` on `new`, `issue` and `dispatch`; the dashboard's `n` follows `new`. | `pkg/worktree/worktree.go`, `cmd/opentree/cmd/new.go`, `issue.go`, `dispatch.go` | S | done |
| 7 | **`path`, `shell`, `t`, `y`, `e`, PATH column** (decisions 11–12). `shell` reuses `CreateAppWindow` with the user's `$SHELL`, and reuses the window when it exists. | `cmd/opentree/cmd/path.go`, `shell.go` (new), `list.go`, `pkg/workspace/shell.go` (new), `pkg/tui/keys.go`, `update.go`, `commands.go`, `pkg/chat/settings.go` | M | done |
| 8 | **Clicks** (decision 10). | `pkg/tui/update.go`, `pkg/chat/update.go` | S | done |
| 9 | **Persistent history** (decision 13). | `pkg/chat/history.go`, `pkg/chat/model.go` | S | done |
| 10 | **`ctrl+f` find** (decision 14). | `pkg/chat/find.go` (new), `keys.go`, `update.go`, `view.go` | M | done |
| 11 | **`/export`** (decision 15). Markdown from the entries: user, agent, tool rows with their output, notices as blockquotes. | `pkg/chat/export.go` (new), `pkg/chat/settings.go` | S | done |
| 12 | **`sync` and `u`** (decision 16). | `pkg/worktree/worktree.go`, `cmd/opentree/cmd/sync.go` (new), `pkg/tui/keys.go`, `update.go` | M | done |
| 13 | **`delete --merged`**, diff viewer paging, the README's archive line (decisions 17–18). | `cmd/opentree/cmd/delete.go`, `pkg/tui/update.go`, `README.md` | S | done |

Commits 1–5 are the two reported problems, fixed in the order they can ship.
If the sequence stalls after 5, both are fixed.

### Found during implementation

- **OSC 52 is the fallback, not an addition** (decision 7 as written said
  "always also"). iTerm2 asks for permission the first time the sequence
  arrives, and a laptop with pbcopy sitting there has no reason to be asked;
  the terminal route runs only when no tool could reach the clipboard — over
  ssh, or on a Linux box with no display. Written with a raw sequence rather
  than termenv's `Copy`, so the writer can be swapped for a buffer in tests
  and refused when stdout is not a terminal.
- **The default base directory claims itself with a marker, as planned, and
  the tests had to be told about it.** `config.Default()` now resolves to a
  directory under `$HOME`, so every test that created a workspace with a
  default config was writing into the real home directory. The workspace
  test helpers move `HOME` to a temp dir unless a test already did — the
  trust-file tests had — and the worktree tests set it themselves.
- **Git is told about the state directory on its own.** The `/.opentree/`
  exclude rule used to arrive with the worktrees; with the worktrees gone
  from the working tree, `state.json` would have shown up in `git status`
  on the first workspace. `ensureBaseDir` writes the state rule always and
  the base rule only when the base is inside the repository, each labelled.
- **`WorktreePath` prefers the recorded path, then asks git.** Decision 2
  named the first half; the second covers workspaces written before the
  path was recorded, through the manager's `Path`, which stats the computed
  location and consults `git worktree list` only when nothing is there.
- **`new` fetches only when the base names a branch.** A sha, a tag or
  `HEAD` is what it is wherever it is read, and fetching it would have
  produced an "offline" note about a fetch that had no business happening.
  `--no-track` on the branch, so a feature branch made from `origin/main`
  does not report itself "up to date with origin/main".
- **The chat's `/shell` runs `opentree shell`** rather than talking to tmux:
  the command already knows the window's name, how to reuse one that
  exists, and how to move a client to it from inside the session.
- **Clicks resolve against the rendering, not a model of it.** The
  dashboard's list renderer records where each row landed; the chat's log
  renderer says which entry each row belongs to. A permission option is
  found by the `[key]` its row leads with, so the row and the click cannot
  disagree.
- **Find starts from where the reader stands.** Typing lands on the first
  match at or below the top of the screen, wrapping to the first when
  nothing is below — searching a conversation you have read is looking
  for something further on.
- **Sync leaves a stopped merge in progress.** A conflict is not an error
  and is not aborted: the markers are what the agent needs to resolve it,
  and the prompt names the files and asks for `git commit` at the end.

New CLI commands (7, 12) are covered by `scripts/smoke.sh` the moment they
register — it reads the command list out of `--help`. `make check` runs
`deadcode ./cmd/opentree`, so a package that lands before its caller is red:
that is why 2 and 3 are one PR if not one commit, and why
`pkg/workspace/shell.go` ships with the command that calls it.

## Tests

- **Chat.** `pkg/chat/chat_test.go` drives `Update` directly with `tea.KeyMsg`
  and `tea.MouseMsg` on `newTestModel()`. Selection and copy tests assert on
  the viewport's content and on a fake clipboard writer, injected the way
  `clipboardTools()` already is — a package-level function a test replaces.
- **Worktree location.** `pkg/worktree/worktree_test.go` and
  `pkg/workspace/workspace_test.go` already build real repositories in
  `t.TempDir()`. Add a case per layout — in-repo, absolute, `~`, marker
  mismatch — and the upgrade case: a state entry whose `WorktreeDir` is the old
  in-repo path with the config pointing at the new default, where
  `WorktreePath` must return the old one. Seeds and skill links from an
  out-of-repo base, both directions.
- **Fetch.** A bare `origin` in a temp dir holding a commit the clone lacks;
  after `new`, the worktree's HEAD is origin's. With no `origin`, `new` still
  succeeds and says so.
- CI runs on macOS and Linux with `-race -shuffle=on`; the clipboard's
  platform table is what makes the tool choice testable with no display.

## Not built

- **Moving `state.json` out of the repository.** Decision 3.
- **Renaming a workspace.** The worktree, branch, window, socket and any PR
  are all keyed on the name.
- **Writing ignore rules for other tools** (jest, tsconfig, `.dockerignore`).
  opentree does not edit the project's configuration; decision 1 removes the
  need.
- **A terminal inside the chat.** The shell is a tmux window, which is what
  tmux is for.
- **Rectangular selection, and selection past the edge of the screen**
  (auto-scroll while dragging). Shift-drag still covers the screen; commit 4
  can grow later.

## Risks

1. **Upgraders find new worktrees somewhere new.** Muscle memory says
   `.opentree/feat-x`. Mitigated by decision 2 (nothing moves), the doctor
   line, the README section, and `opentree path`.
2. **Links into a worktree that is no longer under the repository.** Seeds
   link by absolute path (`pkg/bootstrap/seed.go:263`); the skills bridge
   links relatively (`pkg/skills/link.go:111`), which stays valid at any
   distance but reads as `../../../../src/repo/.claude/skills`. Commit 5's
   tests cover both.
3. **OSC 52 is not universal.** Terminal.app ignores it, which is why the
   platform tool comes first and OSC 52 is the addition rather than the
   replacement. Inside tmux it needs `set-clipboard` not `off`, which is the
   default.
4. **`ctrl+f` changes meaning.** Anyone stepping the cursor with emacs keys
   loses one of them; `→` remains. Called out in the commit message and the
   README's key table.
5. **Fetch adds latency to `new`.** Bounded at 15s and skipped with
   `--no-fetch`; a repository with no `origin` is detected before any fetch is
   attempted.
6. **Selection and the render cache.** The chat re-renders only entries whose
   revision changed (`pkg/chat/view.go:760`). The highlight has to be painted
   over the joined output, not into the cached entries, or a drag would
   invalidate the cache on every motion event.
