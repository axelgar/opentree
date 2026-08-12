# opentree × Skills — Design & Plan

> Status: **commits 1–5 implemented**, uncommitted on `feat/acp-integration`.
> `make check` green. Commits 6 (CLI) and 7 (plugin skills) not started.
> Companion to [ACP-PLAN.md](ACP-PLAN.md).
> Scope: skills only. MCP is sketched at the end, not planned.

## The thing worth building

Not a skill browser. A **propagator**.

Verified on this machine, in this repo:

```
opentree/.claude/skills/release/SKILL.md      exists, untracked
opentree/.opentree/test-img/.claude           does not exist
```

`test-img` is a live opentree worktree. The agent running in it cannot use
`/release`, because project skills live in an untracked directory and
`git worktree add` only carries tracked files. Every worktree opentree creates
starts blind to the repo's own skills, and nothing in the current UI says so.

opentree is the process that creates those worktrees. It is the only tool in
the loop that can fix this, and the fix is one symlink.

The browsing UI is the second-best part. It ships in the same change because
once you can see the four skill trees side by side, the gaps are obvious and
`c` (copy to another agent) writes itself.

## Verified facts

Probed on this machine, 2026-08-12. Not taken from docs.

**1. Skills are a filesystem convention, not a protocol feature.**
`pkg/acp/types.go` has zero skills surface — grep returns nothing. ACP does not
model skills and neither does any agent's CLI API. Every agent discovers them by
reading directories. opentree therefore manages them with `os.ReadDir` and no
agent cooperation whatsoever.

**2. The same format everywhere.** `<dir>/<skill-name>/SKILL.md`, YAML
frontmatter carrying `name:` and `description:`. Identical across all four
agents that have the feature. One reader covers all of them.

**3. The directories.** Transcribed from opencode 1.18.16's own embedded
documentation, which is the authority for its half:

| Agent | User | Repo | Also auto-loads |
|---|---|---|---|
| Claude Code | `~/.claude/skills/` | `.claude/skills/` | — |
| OpenCode | `~/.config/opencode/skill(s)/` | `.opencode/skill(s)/` | `~/.claude/skills/`, `~/.agents/skills/` |

Two things here were wrong in the first draft and are the reason the tab
initially reported "OpenCode 0" while opencode was visibly offering the same
skills as slash commands:

- **`skill(s)`** — opencode reads both the singular and plural spelling of its
  own trees.
- **Agents read each other's directories.** A skills tree has readers, not an
  owner, and the same `SKILL.md` is commonly usable from every agent at once.
  Note the asymmetry: opencode auto-loads Claude Code's *user* tree but not its
  *repo* tree, so a skill in `.claude/skills` is invisible to opencode — which
  the badges now say.

Claude Code also exposes 38 plugin skills, and they are **not listed** — dropped
during implementation once the layout turned out to have no contract. Five
marketplaces on this machine use four different shapes:

```
<mp>/skills/<skill>/SKILL.md            cloudflare, ponytail      11 + 6
<mp>/<plugin>/skills/<skill>/SKILL.md   ponytail/.openclaw         6   ← a decoy mirror
<mp>/<x>/<plugin>/skills/<skill>/       claude-plugins-official   31
                                        gmickel-marketplace       23
```

A glob wide enough to catch all of them also catches the `.openclaw` copies
inside the ponytail repo, which would render six duplicate rows. Reading them
correctly means parsing `installed_plugins.json` and each plugin's manifest —
a feature in its own right, for skills opentree could only ever list, never
manage. Its own commit if it is ever wanted.

`~/.codex/skills/` and `~/.gemini/skills/` also exist on this machine, but
opentree cannot launch either agent, so listing their skills would be inventory
for a tool the user cannot start from here. Out of scope until the registry
grows — at which point it is one entry, same as ACP.

**4. No new dependency.** `go.mod` has no YAML library. Three keys out of
frontmatter is ~20 lines of line-scanning. Same call the ACP client made.

**5. Installed is not available.** Two mechanisms decide what an agent will
actually do with a skill that is sitting on disk:

- **`skillOverrides`** in Claude Code's settings, keyed by skill name, one of
  `on` / `name-only` / `user-invocable-only` / `off` (the enum its binary
  carries). On this machine 13 of 21 user skills are `off` — and the set of
  "off" skills and the set this session was offered have zero overlap, which is
  what confirms the mechanism.
- **`disable-model-invocation: true`** in a SKILL.md's own frontmatter: loaded
  and slash-invocable, but the model will not reach for it unaided. Five skills
  here use it — exactly the five that are neither off nor offered to the model.

Together those two account for all 21 with nothing left over: 13 off, 5
manual-only, 3 fully live. A list that shows 22 undifferentiated rows is
claiming capabilities that are not there.

The three settings sources are the ones `claude --setting-sources` names —
user, project, local — layered in that order.

**5. `EnsureExcluded` already exists** (`pkg/worktree/worktree.go:473`) and
resolves `--git-common-dir`, so it is shared by every worktree. Useful, but see
decision 6 — the plan mostly avoids needing it.

## Where it lives

**A second top-level tab, next to Workspaces.** `tab` / `shift+tab` switches.

```
  Workspaces  │  Skills
```

The instinct in the request is right. Every other secondary surface here is an
overlay (`d` diff, `A` agent picker, `E` error log) because each is *transient* —
you open it, act, and it closes. Skills are a **place**: a persistent inventory
you visit, scan, and edit. Tabs are for places.

It also costs almost nothing. `View()` gets one branch at the top, the key
handler gets one, and the skills view owns its own height math — it never
touches the workspace list's budget, which the comments in `listWindow` show has
already been a source of pain.

One thing stays in the Workspaces tab: a `⚠ skills` badge on any workspace
missing project skills the repo has. That is triage information, and triage
belongs in the list.

## The view

Grouped **by skill name**, with agent badges — because the question asked is
"what do I have, who can use it, what's shared", and that grouping answers all
three in one pass. Grouping by directory answers none of them.

```
opentree

  Workspaces  │  Skills

  ▶ release            ✻ repo                    ⚠ missing in 1 worktree
      Cut and publish a new versioned release of opentree to all three…
    research           ✻ user  ◆ user
      Investigate a question against high-trust primary sources…
    ponytail           ✻ plugin:ponytail   ro
      Forces the laziest solution that actually works, simplest, shortest…
    grilling           ✻ user
      Grill the user relentlessly about a plan or design…

  47 skills  •  ✻ Claude Code 47  ·  ◆ OpenCode 1  ·  ◈ Codex 0  ·  ✦ Gemini 0

  ↑/k up      enter edit          c  copy to agent…      / filter
  ↓/j down    x     delete        l  link to worktrees   tab workspaces
```

The badge marks and colours come from `config.Brand()`, which already exists and
already knows `✻` orange for Claude Code and `◆` for OpenCode. Free.

`research ✻ user ◆ user` is the "shared across" case rendered without a special
mode: two badges on one row. `release ✻ repo` next to three empty agent columns
in the footer is the gap that makes `c` obvious.

## Data model

New package, `pkg/skills`:

```go
type Scope int // ScopeUser, ScopeRepo, ScopeWorktree, ScopePlugin

type Skill struct {
    Name        string // from frontmatter, falling back to the directory name
    Description string
    Dir         string // the directory holding SKILL.md
    Agent       string // registry Name: "Claude Code"
    Scope       Scope
    Source      string // plugin name, for ScopePlugin
}

// Scan walks every known skills root for every registered agent.
func Scan(repoRoot string) ([]Skill, error)
```

Registry addition to `PredefinedAgent`, mirroring `ACPSpec` exactly — a value,
not a pointer, because `ACP ACPSpec` is a value and the zero value already means
"this agent has no skills":

```go
Skills SkillsSpec

type SkillsSpec struct {
    UserDir    string // "~/.claude/skills", tilde expanded at scan time
    RepoDir    string // ".claude/skills", relative to a repo or worktree root
    PluginGlob string // optional, read-only trees
}
```

Filled in for both registry entries. Adding an agent later is a registry entry,
not a rewrite — decision 9 of the ACP plan, applied again.

## Decisions

| # | Decision |
|---|---|
| 1 | Skills tab is a **top-level tab**, not an overlay. Overlays are for transient things. |
| 2 | One row per skill **directory**, badged with every agent that reads it. Answers "shared across" with no second view, and keeps `x` unambiguous. |
| 2a | Skills reachable by two paths are **collapsed on the resolved path**, and the surviving row is the real directory. |
| 3 | Plugin skills are **not listed at all** — four directory shapes, no contract, and nothing opentree could do with them but print them. |
| 4 | Frontmatter parsed by hand. `name` and `description`, first line only. **No YAML dependency.** |
| 5 | Worktrees get repo skills by **symlink**, not copy. Always current, no drift, one syscall. |
| 6 | opentree links **only when the repo does not track the skills dir.** If `.claude/skills` is committed, git already propagates it and opentree stays out of the way. |
| 7 | Linking happens **automatically at worktree creation**. The tab is where you see it and repair drift, not where you have to remember to do it. |
| 8 | `enter` opens `SKILL.md` in `$EDITOR` via `tea.ExecProcess`. That is what "update a skill" means. |
| 9 | **No marketplace, no browse, no install-from-registry.** Adding a skill is `c` (copy an existing one across agents) or `a` (clone a git URL). |
| 10 | Everything is a plain file operation. No state file, no index, no cache — `Scan` on tab entry is a handful of `ReadDir` calls. |
| 11 | A disabled agent's mark is **greyed, not dropped**. The row still has to say the skill is installed for it. |
| 12 | `t` writes `off`, and toggling back **clears the entry** rather than writing `on` — `on` is not always the default, and would silently promote a `disable-model-invocation` skill to fully automatic. |
| 13 | Only the overrides object is rewritten, spliced in by byte range. A whole-document rewrite would reorder 10 of the 12 top-level keys in a file the user hand-maintains and probably has in git. |
| 14 | The override is written to **the file already holding one** for that skill, falling back to the first source. That is the file observation proves is honoured; nothing proves a higher-precedence `on` beats a lower-precedence `off`. |

### On decision 5, symlink vs copy

A copy drifts the moment you edit either side, and then the tab has to render a
three-way comparison nobody asked for. A symlink cannot drift. Editing a skill
from inside a worktree edits the repo's copy, which is correct — a project skill
is one thing, not one per branch.

The cost is Windows, where symlinks need privileges. opentree requires tmux, so
Windows is not a target. If `os.Symlink` fails the operation reports it rather
than silently falling back to a copy — one mechanism, or a clear error.

### On decision 6

Implemented without asking git anything. `Link` skips any destination that
already exists, and a tracked `.claude/skills` is one git has already checked
out — so the tracked case falls out of a single `Lstat` with no subprocess and
no error path to get wrong. One condition, no config knob, no way to get a
worktree whose skills disagree with the branch it is on.

## Commit sequence

Each commit is green on its own and ships something.

| # | Scope | Files | Status |
|---|---|---|---|
| 1 | `pkg/skills`: `Skill`, `Scan`, frontmatter reader, `Delete`, `CopyTo`. Registry gains `SkillsSpec`. | `pkg/skills/skills.go`, `skills_test.go`, `pkg/config/agents.go` | done |
| 2 | **Auto-link on create.** `workspace.Service` symlinks the repo skills dir into each new worktree when git does not carry it. This is the payload — it works with no UI at all. | `pkg/workspace/workspace.go`, `pkg/skills/link.go` | done |
| 3 | Tab bar and read-only Skills tab: rows, badges, footer tally, scroll window, filter. | `pkg/tui/skills.go` (new), `model.go`, `view.go`, `update.go`, `keys.go`, `styles.go` | done |
| 4 | Mutations: `enter` edit, `x` delete with confirm, `c` copy to another agent, `l` relink worktrees. `⚠ no repo skills` badge in the Workspaces list. | `pkg/tui/skills.go`, `view.go` | done |
| 5 | **Availability, not just presence.** Read `skillOverrides` and `disable-model-invocation`; grey the mark of an agent that has a skill off, tag the row, count only what will load. `t` toggles. | `pkg/skills/settings.go` (new), `skills.go`, `pkg/config/agents.go`, `pkg/tui/skills.go` | done |
| 6 | `opentree skills` / `skills list` / `skills sync` for the CLI, matching the existing subcommand pattern. | `cmd/opentree/cmd/skills.go` | not started |
| 7 | Plugin skills via `plugins/installed_plugins.json` — 37 more, and each entry carries an explicit `installPath`. | `pkg/skills/plugins.go` | not started |

Commit 2 is the one that matters. If the sequence stalls after it, the real bug
is already fixed.

**The deadcode gate collapsed 1–4 into one change.** `make check` runs
`deadcode ./cmd/opentree` and tests do not count as reachability, so `Scan`,
`Delete`, `CopyTo` and `Missing` are unreachable until the tab calls them —
commit 1 cannot be green on its own. Exactly what merged commits 1 and 2 of the
ACP plan; here it merges four.

### Found during implementation

- **A skills tree has readers, not an owner.** Caught by the user: the tab said
  "OpenCode 0" while opencode's own slash-command list offered `/ask-matt`.
  opencode auto-loads `~/.claude/skills`, so `Skill.Agent string` became
  `Skill.Agents []string` and scanning moved from per-agent to per-directory.
- **The same skill is reachable by several paths.** With the external trees
  added, every skill listed twice: `~/.claude/skills/ask-matt` is a *symlink*
  to `~/.agents/skills/ask-matt` — same inode — because the installer that put
  them there keeps its store in `~/.agents` and links it into `~/.claude`.
  `Scan` collapses on `EvalSymlinks` and keeps the real directory, so `x`
  deletes the skill rather than a link to it. This also restores the meaning of
  the "duplicate" tag: two *different* files under one name, one shadowing the
  other.

- **The registry is two agents, not six.** Codex, Copilot, Gemini and Pi were
  removed when opentree became ACP-only, and `ACP` is a value rather than a
  pointer. `SkillsSpec` follows it.
- **Every key on the Skills tab is consumed, including the ones it ignores.**
  The first cut fell through to the workspace handler for unknown keys, so `n`
  set `creating = true` while `View` still drew the skills list — a text input
  collecting keystrokes behind a screen with no sign of it. `q` and `E` are
  handled explicitly instead, and `TestSkillsTab_SwallowsWorkspaceKeys` pins it.
- **Descriptions are truncated, not wrapped.** They run to a paragraph by
  convention, and a row that silently became three lines would break the
  two-lines-per-row arithmetic the scroll window depends on.
- **`$EDITOR` is split on whitespace.** `code --wait` and `nvim -u NONE` are
  ordinary values; passing the whole string as the binary name looks for a file
  named after the flags.

## Not built

- **No skill marketplace or discovery UI.** Skills spread by git URL and by copy;
  both are covered. A curated registry is a product, not a feature.
- **No per-worktree enable/disable.** The symlink is all-or-nothing. A worktree
  that wants different skills is a different repo.
- **No versioning or update-check.** `git -C <dir> pull` is available to anyone
  who cloned a skill; opentree does not track provenance to offer it.
- **No writing to plugin trees.** Read-only, listed for completeness.
- **No conflict resolution** when two agents hold different `SKILL.md` files
  under the same name. The row shows both badges; opening either one is `enter`
  on the row after picking a badge. Merging them is the user's problem.

## MCP — the phase 2 sketch

Deliberately not planned yet, because it is a materially harder problem and the
request said skills first. What makes it harder:

1. **No shared format.** Skills are one convention across four agents. MCP is
   `.mcp.json` (Claude, project), `~/.claude.json` (Claude, user), `opencode.json`
   (OpenCode), `config.toml` (Codex), `settings.json` (Gemini) — five schemas,
   three file formats, all with different key names for the same concept.
2. **Secrets.** MCP server entries carry API keys, in env blocks or inline.
   Anything that reads, copies, or displays them is a security surface that
   skills simply do not have. A "copy this server to another agent" button moves
   credentials between config files.
3. **Rewriting user config.** Skills are self-contained directories, so deleting
   one is `RemoveAll`. Removing an MCP server means editing a JSON file the user
   hand-maintains, preserving everything else in it — the same problem
   `config.SetKeys` solves for TOML, five times over.
4. **Liveness.** A listed skill either exists or does not. A listed MCP server
   may be unreachable, unauthenticated, or wedged, and a list that does not say
   which is worse than no list.

If it gets built, the shape is the same: a `pkg/mcp` with per-agent
read/write adapters behind one `Server` type, a third tab, and **read-only for
v1** — show every agent's servers in one place, with secrets masked, and no
mutation until the read side has been correct for a while.

## Risks

1. **Symlink surprise.** A user who does not expect `.claude` in their worktree
   finds an untracked symlink there. Mitigated by decision 6 (it only appears
   when git was not going to provide the directory anyway) and by the Skills tab
   naming exactly what was linked and where.
2. **Agents that stop reading these paths.** Four directories confirmed by
   observation, not by contract. A path that moves silently produces an empty
   tab rather than a wrong action, and is a one-line registry fix.
3. **`~/.claude/skills` is not small.** 21 user skills plus 38 plugin skills here
   is 59 rows, and other machines will have more. The scroll window and `/` filter
   are in commit 3 for that reason, not as polish.
4. **Tab key collision.** `tab` is bound inside the remote-branch dialog for
   suggestion selection. Tab-switching applies only at top level, where no dialog
   is open, so they do not overlap — but the key handler must check dialogs first,
   which it already does for every other key.
