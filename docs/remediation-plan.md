# Remediation plan

Where this came from: a full-tree review run in August 2026 against `025f719`,
alongside the CI hardening on this branch. 13 finders, each finding put to two
adversarial verifiers — one told to refute it, one to judge its impact. 73 raw
findings, 68 confirmed, 5 refuted. 49 distinct issues after dedup. A separate
completeness pass then asked what the review itself had not looked at, and
found ten more, listed here as WS9.

Severity below is the higher of the two verifiers' ratings.

The workstreams are ordered by what they cost the user if left alone, not by
size. WS1 is first because it is the only place where the loss is both silent
and permanent. WS2 is second because it is three lines of guard standing
between `opentree delete state.json` and `os.RemoveAll` on the registry.

Read the **cross-cutting conflicts** at the bottom before starting WS2c, WS3
or WS4a. Several of the obvious fixes are the wrong ones, and the reason is
recorded there.

## Status

- [x] WS1 — Field-scoped state writes
- [x] WS2 — Destructive operations: guardrails
- [x] WS3 — Chat control socket: identity on the wire
- [x] WS4 — Chat/agent process lifecycle
- [ ] WS5 — TUI state machine correctness
- [ ] WS6 — Config resolution inside linked worktrees
- [ ] WS7 — Close the test holes that hide the above
- [ ] WS8 — Polish batch
- [ ] WS9 — What the review itself missed

---

## WS1 — Field-scoped state writes (HIGH x3 + medium x1)

**Problem.** `Store.UpdateWorkspace` replaces the whole record with the
caller's snapshot *after* `mutate` reloaded the authoritative one from disk.
`GetWorkspace` serves from the in-memory map and never reloads. Two processes
(the dashboard and each `opentree chat`) share one `state.json` via
`git rev-parse --git-common-dir`, so each reverts the other's fields.
Self-healing for the PR and branch fields, which are re-derived every 30s;
**permanent** for `ACPSessionID`, `ACPSessions`, `SetupAt`, `SetupHash` and
`Port`, because nothing re-derives those.

**Change.** Add `func (s *Store) Update(name string, fn func(*Workspace) error) error`
running `fn` on the freshly-loaded record inside `mutate`, keeping the
not-found check. Convert all nine non-test call sites, then delete
`UpdateWorkspace` so the shape cannot come back.

**What could break.** `mutate` holds `s.mu` (a non-reentrant `sync.RWMutex`)
and the flock while `fn` runs, so any re-entry into the Store from inside the
closure self-deadlocks — `ensurePort` calling `ListWorkspaces` is the one that
bites. `CreateFromIssue` returns its `ws` to the caller, so the callback must
mutate the struct that gets returned. `AddWorkspace` stays a whole-record copy;
it is a create, not an update.

**Verify.** Two `Store` instances over one file: A writes `ACPSessionID`, B
holding a pre-A snapshot writes `PRURL`, both survive. `TestConcurrentWriters`
and `TestMutate_DoesNotResurrectDeletedWorkspace` stay green.

---

## WS2 — Destructive operations: guardrails (medium x4)

Everything here ends in `os.RemoveAll`, `git branch -D` or `tmux kill-session`.

**2a. `worktree.Delete` skips `reservedDirName`.** `Create` and
`CreateFromRemote` reject `state.json`; `Delete` does not, and the unregistered
path runs `os.RemoveAll` on it. Reproduced: `opentree delete state.json` wipes
the registry, then exits 1 blaming a missing branch. Add the check, and refuse
an unregistered path that is not a directory — the latter kills the whole
class. Do *not* require a `.git` entry before `RemoveAll`; that breaks
`TestDelete_OrphanedWorktreeDirectory`.

**2b. `base_dir` reaches `os.RemoveAll` with no containment check.** A
repo-supplied `[worktree] base_dir = "../.."` flows unvalidated. Reject an
escaping `base_dir` *from the repo-config layer only*, mirroring how
`LoadWithSources` already strips `Notify`, and assert containment on the final
path before `RemoveAll`. Rejecting `..` globally would break the working
`../worktrees` layout.

**2c. Delete always force-deletes an adopted branch.** `createdBranch` is used
for rollback only; `Delete`/`DeleteMultiple` hardcode `true`. Persist
`AdoptedBranch` on `state.Workspace`, set true *only* in the adopt fallback,
and pass `!ws.AdoptedBranch`. Also name the branch in the TUI dialog and echo
the already-loaded diffstat into the card body.

**2d. A missing branch aborts the whole delete.** `git branch -D` failing skips
`KillWindow` and `DeleteWorkspace`, leaving an undeletable row and a live dev
server holding its port. Gate the block on `git rev-parse --verify`. Do not
string-match git's message; it is translated.

**2e. Same-basename repos share a tmux session.** `repoName()` uses
`filepath.Base(root)`, so two clones named `api` share `opentree-api`. Gate
`KillSession` on the session holding only this repo's windows, and filter
`pruneServerWindows` by `#{pane_current_path}`. Do not hash the session name —
it breaks the documented `opentree-<repo>` and strands live sessions.

---

## WS3 — Chat control socket: identity on the wire (medium x2)

`SocketPath` truncates the sanitized workspace name to 32 bytes, so two
workspaces sharing a 32-char prefix collide: `serve()` unlinks the live socket
and binds, and later the first chat's `Close` deletes the second's. Prompts
then execute in the wrong worktree. Separately, `chat.Permission` and
`chat.Command` carry no request identity, so a dashboard answer applies to
whatever is head of the queue at that instant.

Three additive edits: append a hash suffix **only when truncation occurs**, so
short names keep their current path and running chats stay reachable; add
`ToolCallID` to both messages and refuse on mismatch **only when it is
non-empty**; validate `Status.Workspace`, **accepting empty**, since `serve()`
seeds a nameless starting status.

---

## WS4 — Chat/agent process lifecycle (medium x2 + low x1)

**4a. No SIGHUP handler.** The chat notifies on `os.Interrupt` only. bubbletea
covers SIGINT and SIGTERM but not SIGHUP, which is exactly what
`tmux kill-window` sends on delete, prune and manual close. Teardown is
skipped, so the ACP group kill never runs and the agent's children are
orphaned against a just-deleted worktree. Use `signal.Notify`, **not**
`NotifyContext` — `ctx` is what `exec.CommandContext` watches, and its default
cancel kills the direct child only.

**4b. Unbounded ACP handshake before the TUI exists.** `Initialize` has no
deadline and the first `launch()` runs before the program does, so a slow agent
shows a bare pane; worse, ctrl+c during launch poisons `m.ctx` and `[r] restart`
then fails forever. Make the first launch async with an explicit starting
state, and derive a per-launch context.

**4c. `runCommand` can block forever on `<-drained`.** If a setup command
leaves any descendant holding the inherited stdout fd, `Wait` returns but the
drain never does. Select on the context and a grace timer, closing the pipe so
the scanner unblocks. Do not keep the killer goroutine alive across the drain —
SIGKILLing the group after the shell exited would kill shared daemons.

---

## WS5 — TUI state machine correctness (medium x3 + low x1)

- Servers tab indexes unsorted map order. Use a dedicated name sort shared by
  the update and the view — not `sortedWorkspaces()` (honours `m.sortMode`) and
  not `visibleWorkspaces()` (applies the Workspaces filter) — and re-anchor the
  cursor by name on the 10s refresh, which never reaches `updateServers`.
- Generic `errMsg` tears down unrelated dialogs. **Delete the four resets**;
  every one is provably redundant. Splitting the message into typed failures
  risks sticking `workspaceCreating` true — spinner forever, `q` blocked.
- `diffLoadedMsg` opens an invisible overlay. Guard on `busyWithDialog()`
  and the tab. Do not fold tabs into an `overlay()` enum: `busyWithDialog`
  deliberately excludes `diffViewing` so the wheel scrolls the diff first.
- The delete confirmation hides what it destroys. Covered in WS2c.

---

## WS6 — Config resolution inside linked worktrees (medium x2 + low x1)

`FindConfigFile` stats `dir/opentree.toml` *before* the `dir == root` break,
and `gitutil.RepoRoot` returns the **main** repo root inside a linked worktree,
so the two disagree from `.opentree/<ws>`. Not a trust bypass — `Trusted`
hashes exact command text and both trust paths print what they approve — but it
produces a dead-end loop (`setup` says run `trust`; `trust` approves the branch
hash under the main key; `setup` refuses again) and a wrong write target
(`config set`, `agents use` and the TUI picker edit the branch's tracked
`opentree.toml`, dirtying the agent's branch and losing the setting).

In order: point `workspaceConfig()` at the repo root; make `FindConfigFile`
worktree-aware; print the absolute path `config set` writes to. Separately,
`SetKeys` round-trips through `map[string]any`, deleting comments and
alphabetising — git-recoverable for the repo file, **not** for the untracked
global one.

---

## WS7 — Close the test holes that hide the above

Do this *with* WS1-WS3, not after; each item guards a fix being shipped.

- `scripts/check-skips.sh`'s FATAL regex misses `pkg/github`'s "gh installed but
  not authenticated". One regex edit turns a silently-27%-covered package into a
  loud CI failure. Highest value for the effort in this workstream.
- `pkg/github`'s decoders are at 0.0%. Extract the real filter clusters into
  pure `func([]byte) (...)` helpers and table-test them, matching
  `deriveCIStatus`. Do not add a `ghRun` package var; a mutable global fights
  `-race -shuffle=on`.
- `worktree.Delete`'s wrong-branch guard has no coverage at all.
- `DeleteMultiple`'s partial failure, and the caller that collapses partial
  success into a flat error and skips the refresh.
- `configKeys` agreement with the `getConfigValue` switch.
- `notify.Watched`, coverable as-is with a fake `tmux` on PATH.

---

## WS8 — Polish batch (all low)

Bundle only after WS1-WS4 land.

- `openURLCmd` never `Wait()`s: one zombie per `o` press.
- The setup spinner never ticks.
- `errorText()` drops everything after the first line, discarding the agent's
  stderr tail. Split where `m.err` is *stored*: a failed first launch assigns
  directly and never emits `errMsg`.
- `sessions.go` writes the session twice and swallows a write error, stranding
  a live session with an empty id.
- Pasted images are discarded by a socket or queued prompt.
- `acp.Close`'s kill and Wait need a `sync.Once`.
- A non-GitHub remote still pays a 30s `gh pr view`; decide GitHub-ness once
  from the remote URL.
- `send.Send()` runs on the bubbletea loop. Move `watched()` and `throttled()`
  together onto one drain goroutine — their order is deliberate.
- The clipboard falls over instead of falling through to xclip.
- `launchLine` quotes args but not the command.
- Skills settings decode drops sibling keys.
- `bulleted` and `renderTool` rebuild strings quadratically.
- `pkg/gitutil` grows `Output(dir, args...)` folding `(*exec.ExitError).Stderr`
  into the error, for the data-parsing call sites only.

---

## WS9 — What the review itself missed

The review covered concurrency and correctness exhaustively and never asked
about the product around the code: first run, upgrade, uninstall, supply chain,
observability, terminal compatibility, licensing.

1. **Worktrees land inside the repo and nothing teaches git to ignore them.**
   `BaseDir` defaults to `.opentree`, `state.json` goes beside it, and nothing
   writes `.gitignore` or `.git/info/exclude`. `git add -A` stages a gitlink to
   a local-only commit. This repo's own `.gitignore` has the entry; the default
   ships without it.
2. **The two IPC channels between versions are unversioned.** A live chat
   window is never relaunched, so after an upgrade the dashboard is the new
   binary and every open chat is the old one. Neither the socket messages nor
   `state.json` carry a schema field, and nothing on either side can detect
   skew to report it.
3. **A downgrade silently strips every state field the older binary does not
   know.** `loadFromDisk` unmarshals into a fresh `State`, Go drops unknown
   fields, and `atomicWrite` writes back in full. Needs no race — one
   serialized process destroys them.
4. **There is no way to get diagnostics off a user's machine.** No log package,
   no `--verbose`, no log file. The whole surface is a 20-entry in-memory error
   log and the agent's stderr ring, shown only after the agent dies.
5. **Agent adapters install from npm unpinned, with lifecycle scripts, one
   keypress away.** No version pin, no `--ignore-scripts`, no integrity check —
   and the `i` key installs with no confirmation at all.
6. **The skills index is the root of trust and can be fetched over plaintext
   HTTP.** The extraction itself is well hardened; the hole is upstream. The
   per-artifact digest comes from the index, and the index has no digest of its
   own. Skills are instructions the agent acts on.
7. **The chat's primary text is unreadable on a light terminal.** `#EEE` on an
   unset background is 1.16:1 on white. 97 hardcoded colors, zero uses of
   `AdaptiveColor`.
8. **Nothing uninstalls, and the code claims the opposite.** The stated intent
   is that uninstalling opentree leaves no stray package; ~300 MB per adapter,
   the trust file and the completion script all stay.
9. **Third-party wordmarks are vendored with no attribution file.** Four
   vendors' marks in their own brand colors, MIT licence, no NOTICE.
10. **`pkg/state` reimplements `fsutil.WriteAtomic` at 0644**, bypassing the
    project's own documented 0600 rule for the one file holding session ids.

### Categories asked about and genuinely fine

Recorded so they are not re-audited: the archive extraction hardening in
`pkg/skills/wellknown.go` (traversal, symlinks, setuid, zip-bomb budgets) is
above average; `NO_COLOR` is handled through termenv; the npm launcher's
platform detection is correct; `scripts/npm-publish.sh` pre-verifies
architecture, version wiring and registry auth before the first non-reversible
publish.

---

## Cross-cutting conflicts

1. **WS1 vs `ensurePort`** — `Store.Update`'s closure runs under `s.mu` and the
   flock; `ensurePort` calls `ListWorkspaces`. Hoist it out or deadlock. Same
   audit for every converted site.
2. **WS1 supersedes** the "only write when a field changed" suggestion.
3. **WS2c must use `AdoptedBranch` (zero = delete), not `BranchCreated`
   (zero = keep)** — the latter breaks every existing workspace and blocks
   same-name recreation.
4. **WS2b must not reject `..` globally** — `../worktrees` is a documented
   layout. Reject from the repo-config layer and assert containment at the
   `RemoveAll`.
5. **WS2e must not rename the tmux session** — it breaks the documented name
   and strands live sessions on upgrade.
6. **WS3's path change must be conditional on truncation**, or every running
   chat is orphaned on upgrade.
7. **WS3's `ToolCallID` guard must be soft** (`!= ""`), or a new chat talking
   to an older dashboard silently loses every remote answer.
8. **WS3's `Status.Workspace` check must accept empty**, or the "starting..."
   state disappears.
9. **WS4a must not use `NotifyContext`** for SIGHUP.
10. **WS4c must not select on `done` before the SIGKILL** — that removes the
    tested grandchild escalation.
11. **WS5's `errMsg` fix is deletion, not decomposition.**
12. **WS5's servers ordering must not reuse `sortedWorkspaces()` or
    `visibleWorkspaces()`.**
13. **WS8's notify fix must move `throttled()` with `watched()`.**
14. **WS9.2 and WS9.3 land in the same channel WS1 and WS3 are changing** — add
    the schema version before, not after, the fields that need it.
