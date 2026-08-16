# opentree × Workspace Bootstrap — Design & Plan

> A worktree carries only what git tracks. opentree creates the worktrees, so
> opentree is the only thing in the loop that can fix what git left behind.

## The thing worth building

`skills.Link` already solves one instance of this problem: a repository's skills
are usually untracked, so a fresh worktree cannot see them, and the agent working
there is quietly less capable than the same agent one directory up.

The general case is larger. Every worktree opentree creates starts with no
`node_modules`, no `.venv`, no `target/`, no `.env`. The agent's first turn goes
on `pnpm install` — or worse, it runs the test suite, watches it fail for reasons
that have nothing to do with the task, and starts "fixing" the lockfile.

Three parts, one problem:

- **seed** — untracked config the worktree needs and git will not carry.
- **setup** — commands that build what seeding cannot copy.
- **run** — the dev server, on demand, reachable and told apart from its four siblings.

The third is the one with a port problem: five worktrees of one repo all want 3000.

## Verified facts

Established by reading the tree, not assumed.

1. `Service.Create` (`pkg/workspace/workspace.go:234`) runs worktree → `linkSkills`
   → `launchAgentWindow` → state entry. Nothing between. `CreateFromIssue`
   delegates to it; `CreateFromRemoteBranch` repeats the same sequence.
2. The TUI calls `Create` inside a **blocking** `tea.Cmd`
   (`pkg/tui/commands.go:117`). A 90-second install there freezes the dashboard
   with no output and nothing to watch.
3. Every window runs `opentree chat <name> --agent X` (`workspace.go:169`). One
   launch path, defended by name in `agentLaunch`'s comment.
4. `EnsureWindow` (`workspace.go:183`) relaunches that same chat process whenever
   a window is lost — a killed pane, a restarted tmux server, a chat quit by hand.
   **Chat startup happens many times per workspace lifetime, not once.**
5. `opentree.toml` **is tracked in git** (`git ls-files` confirms it in this repo).
   Anything executable in it is code that arrives with a clone.
6. `SanitizeBranchName` (`gitutil.go:95`) replaces `/` and `:` with `-`. No
   workspace-derived window name can contain a colon.
7. `findWindowID` (`tmux.go:161`) matches exactly in Go and then targets by window
   **ID** (`@3`), never by name — so a colon in a window name costs nothing.
8. Dashboard keys taken: `up/k down/j n i r enter d p o R x space / s A a c m E
   tab q ?`. Free lowercase: `b e f g h l t u v w y z`. The Skills tab rebinds
   `enter a c x t l v` **within its own tab** — a third tab gets a fresh keyspace.
9. `opentree prune` already means "reap workspaces whose worktree vanished"
   (`cmd/opentree/cmd/prune.go`).

### On portless

[vercel-labs/portless](https://github.com/vercel-labs/portless) solves the naming
problem well, and its README is explicit about the cost:

- The proxy binds 443 (or 80 with `--no-tls`) and **auto-elevates with sudo** on
  macOS/Linux.
- It **mutates `/etc/hosts`**, because `*.localhost` auto-resolution is a
  Chrome/Firefox/Edge behaviour — Safari and non-browser clients do not get it.
- `portless service install` writes launchd/systemd units that **run as root**.
- It assigns 4000–4999 via `PORT` and **injects `--port`/`--host` flags for
  frameworks it recognises**, explicitly giving up on compound commands, env
  prefixes, and scripts that delegate.
- Non-interactive environments get a hard error, not a fallback.
- Its name is inferred from `package.json`/git root/directory — so **every
  worktree of one repo infers the same name and collides.**

Reimplementing that in Go would give opentree a certificate authority, a sudo
prompt, an `/etc/hosts` writer, a root-owned service, a framework-flag table to
keep current, and a process supervisor. That is a larger and more privileged
program than opentree is. Today the worst thing this tool does to your machine is
create a symlink.

So: use it when it is there, and work without it when it is not.

## Decisions

| # | Decision |
|---|---|
| 1 | Scope is **seed + setup + on-demand run**. No CA, no proxy of our own, no framework flag injection. |
| 2 | **Setup commands run as the first phase of `opentree chat`**, before it connects to the agent. That is where the screen and the status socket already are, so streaming output and a live row cost nothing, and it keeps the single launch path fact 3 defends. |
| 3 | **Seeding runs in `Create`, beside `linkSkills`.** It *is* `linkSkills` with a configurable source list — same rationale, same best-effort posture. It also puts `.env` and `.npmrc` in place before the first setup command needs them. |
| 4 | Setup failing **cannot roll back the worktree**, because `Create` has already returned. Correct anyway: a worktree with a failed install should be kept and repairable, not destroyed because a lockfile was stale. |
| 5 | Config lives in the **tracked `opentree.toml`**, behind a **trust gate**. A bootstrap sequence is a property of the project, not the person — make it per-machine and it drifts and stops being maintained. |
| 6 | The trust hash covers **`setup` and `run` together**. `run` is executable code from the same tracked file; gating only `setup` means a hostile repo moves its payload one key down. |
| 7 | The trust prompt appears **in the chat, at setup time**, reusing the dialog that already asks permission questions. Non-interactive **refuses** — `opentree trust` is the escape hatch for scripts and CI. |
| 8 | Bootstrap applies to **all three creation paths**. `CreateFromRemoteBranch` needs it most: a remote branch is the one you did not prepare for. |
| 9 | Seed sources are an **explicit list**. Deriving from what git ignores returns `.opentree/` and a 16MB binary in this very repo — auto-seeding would recursively copy your worktrees into your worktrees. |
| 10 | **Symlink files, refuse directories.** Config gets symlinked; state gets built. `node_modules` is not a file you seed, it is the output of a setup command — and a worktree that `rm -rf`s a symlinked `node_modules` has just deleted your main checkout's. |
| 11 | Seed paths that **escape the repo root** after symlink resolution are a validation error, not a trust prompt. There is no legitimate `seed = ["../../.ssh/id_key"]`, and "ask the user" is the wrong answer to a question with one correct outcome. |
| 12 | Per-workspace override is an **explicit detach** — `opentree seed detach <branch> .env` swaps the link for a copy. Divergence should be decided, not discovered. |
| 13 | Idempotence via **`SetupAt` + a hash of the resolved commands in `state.json`**. The hash earns its keep by making *edited* setup re-run on the next chat start rather than staying stale forever. |
| 14 | Setup failing shows the **existing stopped panel**: `[r]` retry, `[s]` start anyway. An agent that starts on a broken worktree burns a turn rediscovering that, and may "fix" it by editing your lockfile. |
| 15 | The failure goes to **opentree's error log**. It is **never auto-injected** into the conversation — whether the agent should see it is the user's call, and pasting it is one action they can take themselves. |
| 16 | Hung setup: **no timeout, `esc` cancels.** Any timeout is wrong for somebody — a warm `pnpm install` is 2 seconds, a cold `cargo build` is twenty minutes, and a build killed at minute 19 of 20 is worse than one you chose to abandon. Kill the process group, or `pnpm` leaves children behind. |
| 17 | The **run command is explicit config**, never rewritten. Flag injection is where portless spends most of its caveats, and it is a table that goes stale every time a framework ships a new CLI. Export `PORT`; let the user write `--port $PORT` if their stack ignores it. |
| 18 | Detection exists only as a **one-time suggestion** (`opentree setup --suggest`), reading `package.json` scripts and `Procfile`, proposing **`setup` and `run` together** — the file that says `"dev": "next dev"` also says `"packageManager": "pnpm@9"`. Not `Makefile`: `make dev` means something different everywhere. |
| 19 | Suggestion is **never offered during `opentree new`**. Creation already carries a trust prompt; a second interactive question turns "make me a worktree" into a wizard. |
| 20 | Servers start **on demand only**. Five worktrees auto-running `next dev` is five Node processes and several gigabytes nobody asked for, and most workspaces are agent-only. |
| 21 | The server runs in **its own tmux window, `<sanitized>:run`**. Reuses `CreateAppWindow`/`KillWindow`/`ListWindows`/`PaneCurrentCommand` — start, stop, status and orphan detection all already exist. And "where did my output go" is answered by "attach to it". |
| 22 | `:` as separator is **provably** collision-free (facts 6 and 7), not probably. Suffix parsing is one `strings.Cut` on a character that cannot appear in the left half. |
| 23 | **Servers is a third top-level tab**, matching `model.go:47` — "Tabs rather than overlays". |
| 24 | The list is **derived from tmux**, plus a **port dial** for liveness. A server is a process, and the process list is the only thing that cannot be stale. Alive-but-not-listening = starting, listening = up, gone = stopped — that three-state distinction is most of the view's value and costs a 50ms `net.DialTimeout`. |
| 25 | The tab lists **every workspace**, not only running ones. A view that shows only running servers is empty exactly when you opened it to start one. When `run` is unset the tab is a single empty state — a tab that appears and disappears based on config is one people do not learn. |
| 26 | The port is **assigned once and persisted**, from **20000–32000**. OAuth redirect URIs are registered against an exact `localhost:PORT`. The range matters: Linux's ephemeral range starts at 32768 and macOS's at 49152, so allocating above that collides with the kernel intermittently and unreproducibly. |
| 27 | **portless if installed, plain port otherwise.** Named `<branch>.<repo>.localhost` via its subdomain form, which reads as "this branch of this project" and sidesteps the collision its own inference would cause. |
| 28 | If portless is on PATH but **not yet initialised**, do not launch it — say so on the row and fall back to the port. opentree must never surface a sudo prompt in a detached tmux window nobody is looking at. |
| 29 | **Delete kills the server first**, and says so in the confirmation. **Quit leaves servers running**, matching the existing "quit the dashboard, sessions keep running" contract. Genuine orphans extend **`opentree prune`** — same category of mess, and importing portless's `prune` vocabulary would overload a verb that already means something here. |
| 30 | **One new key on the Workspaces tab: `w`** (start/stop this workspace's server). Everything else lives in the Servers tab's own keyspace, including `o` for open-URL. Re-running setup and detaching a seed are rare, deliberate, recovery-shaped actions — CLI only until someone asks. |
| 31 | `opentree setup <branch>` runs **inline in your terminal**, re-seeding first and then running commands. You typed it to watch it; restarting the chat would tear down a live conversation to re-run an install. Both paths call one function and write the same marker. |

### On decision 2, and what it costs

The alternative was running setup inside `Service.Create`, which makes creation
atomic and rollback trivial. It also freezes the TUI for the duration behind a
blocking `tea.Cmd` (fact 2) with nothing on screen. The chat phase trades
atomicity for a window that exists immediately, output you can read, and a
dashboard row that says `setting up` through the status file it already polls.

Decision 4 is the price, and it is the right way round.

### On decision 10, symlink vs copy

`skills.Link` chose symlink because "a copy starts drifting the moment either
side is edited". That holds for `.env` — one credential set, shared — and
inverts for anything a branch may legitimately change.

One caveat worth writing down: a symlink is not as durable as it sounds. Tools
that write atomically (rename-over-original) silently replace the link with a
regular file. So `opentree setup --check` should report seeded files that have
quietly become regular files — accidental detachment needs to surface somewhere,
even though decision 12 makes deliberate detachment explicit.

### On decision 11, and where the error lands

Seeding is best-effort beside `linkSkills` (decision 3), which swallows what it
cannot do — so a validation error raised there would never be seen. The two
failures are different kinds, and they surface in different places:

- A path that escapes the repository is the config being wrong for **every**
  workspace this repository will ever make. `bootstrap.ValidateSeed` runs beside
  `Agent.Validate`, before the worktree exists, and creation fails with one
  message.
- A symlink the filesystem refuses is one worktree's bad luck. That stays
  best-effort and the workspace still comes up.

`opentree config set` refuses a list-valued key rather than inventing a
comma-splitting syntax that would quote wrongly the first time a path contained
a comma. `config list` and `config get` read it; the file is where it is edited.

### On decision 15, and how a failure leaves the window

opentree's error log lives in the dashboard's memory, and the chat is a
different process — so "goes to the error log" needed a channel. It rides the
control socket the list already polls: `chat.Status` gained an `Error`, the chat
fills it while the setup panel is up, and the list copies it in once. A failure
in a window nobody attaches to still surfaces, and the conversation still never
sees it.

### On decision 27, and what portless actually accepts

Checked against its README rather than assumed, because the launch line had to
be right:

- `portless <name> <command> [args...]`, with dotted names giving subdomains —
  so the name is passed, not inferred.
- The child's port comes from portless as `PORT` in 4000–4999, **except** that
  `PORTLESS_APP_PORT` pins it. opentree sets that to the workspace's own port,
  which keeps the recorded port true and keeps the liveness dial pointed at the
  process opentree started.
- `-p/--port` is the *proxy's* listen port (443, or 80 with `--no-tls`), not
  the app's.
- `portless doctor` inspects health but its exit code is undocumented, so
  "initialised" is decided by dialling 443/80 instead. A proxy that answers has
  already been through the CA, the hosts file and the root service; one that
  does not would need a sudo prompt to get there.


The value portless adds is a stable name instead of a port. Everything else it
does — the CA, the sudo, the hosts file, the root service — exists to serve
`https://` on 443, which buys the scheme and the absent port number and nothing
else. That is not worth becoming a privileged program for. It is very much worth
using when the user has already decided to install it.

Suggest it in the README and in the Servers tab, stating what it buys and that
opentree works without it.

## Config

```toml
[workspace]
setup = ["pnpm install --frozen-lockfile"]   # run in the fresh worktree
seed  = [".env", ".npmrc"]                   # symlinked from the repo root
run   = "pnpm dev"                           # on demand, PORT exported
```

`setup` and `run` hash together into one trusted block (decision 6). `seed`
needs no gate, only path validation (decision 11).

## Data model

`state.Workspace` gains:

| Field | Why |
|---|---|
| `SetupAt time.Time` | when bootstrap last completed |
| `SetupHash string` | of the resolved `setup` + `run` block; a mismatch re-runs |
| `Port int` | assigned once from 20000–32000, stable for the workspace's life |

Everything else is derived: the server list from tmux, liveness from a dial,
seeded-file drift from `lstat`.

## Commit sequence

| # | What | Files | Status |
|---|---|---|---|
| 1 | `[workspace]` config block; `pkg/bootstrap` seeding — explicit list, symlink files, refuse directories, reject escaping paths. Called from all three creation paths beside `linkSkills`. **Payload with no UI.** | `pkg/config/config.go`, `pkg/bootstrap/seed.go` (new), `pkg/workspace/workspace.go`, `cmd/opentree/cmd/config.go`, `README.md` | done |
| 2 | Trust: hash `setup`+`run`, record approvals in `~/.opentree/trust.json`, `opentree trust` / `show` / `revoke`. | `pkg/bootstrap/trust.go` (new), `cmd/opentree/cmd/trust.go` (new), `pkg/fsutil` (new, extracted from `config` and `skills`) | done |
| 3 | Setup phase in the chat: run before the ACP connect, stream into the view, `SetupAt`+`SetupHash` marker, `esc` cancels the process group, stopped panel with `[r]`/`[s]` on failure, error to the error log. | `pkg/bootstrap/setup.go` (new), `pkg/chat/setup.go` (new), `pkg/chat/{model,update,view,keys,socket}.go`, `pkg/state/state.go`, `cmd/opentree/cmd/chat.go`, `pkg/tui/{agentctl,update}.go` | done |
| 4 | `opentree setup <branch>` inline (re-seed, then commands), `--check`, `opentree seed detach`. | `cmd/opentree/cmd/setup.go` (new), `cmd/opentree/cmd/seed.go` (new), `pkg/bootstrap/seed.go` | done |
| 5 | Ports assigned and persisted; `<name>:run` window; `w` starts/stops from the Workspaces tab. | `pkg/bootstrap/run.go` (new), `pkg/workspace/server.go` (new), `pkg/state/state.go`, `pkg/tmux/tmux.go`, `pkg/tui/{keys,update,commands,model,view}.go` | done |
| 6 | Servers tab: derived from tmux, dial for three-state, own keyspace (`s`/`x`/`r`/`enter`/`o`), empty state when `run` is unset. | `pkg/tui/servers.go` (new), `model.go`, `view.go`, `update.go`, `commands.go`, `keys.go`, `skills.go` (tab bar), `pkg/bootstrap/run.go` | done |
| 7 | portless: detect on PATH, detect initialised, `<branch>.<repo>.localhost`, URL in the view, fall back to the port with a reason. | `pkg/bootstrap/run.go`, `pkg/workspace/server.go`, `pkg/tui/{servers,model,commands,update}.go`, `README.md` | done |
| 8 | `opentree setup --suggest` from `package.json` + `Procfile`; `prune` extended to reap orphaned run windows. | `cmd/opentree/cmd/setup.go`, `pkg/workspace/workspace.go` | todo |

Commit 1 is the whole feature for anyone whose bootstrap is "copy `.env`".
Commits 5–7 are the run story and can slip without blocking the rest.

## Not built

- **No CA, no trust store, no `/etc/hosts`, no root service.** Decision 27.
- **No framework detection or flag injection.** Decision 17.
- **No `run_on_create`.** Decision 20 settled on-demand; the flag can arrive when
  somebody asks.
- **No filesystem watching** for copy-on-write seed overrides. Real machinery for
  a rare event, and it cannot tell "the user edited it" from "a tool replaced it".
- **No LAN mode, no tunnels.** portless has them; opentree has no opinion.
