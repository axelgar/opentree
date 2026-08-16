# opentree × ACP — Integration Plan (opencode pilot)

> Status: **implemented.** All six commits landed on `feat/acp-integration`.
> The branch then kept going past this plan — four ACP agents, a session
> ledger, the plan panel back, the preview retired, mouse capture reversed.
> Each departure is recorded under
> [Deviations](#deviations-from-the-plan-as-written); the sections above it
> read as written when the six commits landed.
> Companion to [PLAN.md](PLAN.md), which describes the shipped v0.1 architecture.

## Overview

opentree stops shelling out to an agent's own TUI and becomes an [Agent Client
Protocol](https://agentclientprotocol.com) client. Each worktree's tmux window runs
`opentree chat <ws>` — an altscreen Bubble Tea view that owns an `opencode acp` child
over stdio and serves a unix socket so the main list can watch and control it.

```
opentree (main list, altscreen)
     │  unix socket client  ──▸ .opentree/sock/<ws>
     ▼
opentree chat <ws>            ← tmux window, altscreen
     ├─ ACP client  ── JSON-RPC/stdio ──▸ opencode acp --cwd <worktree>
     └─ socket server (status out, control in)
```

Every current flow survives: one window per worktree, `enter` attaches, killing the
window kills the agent, and `capture-pane` still drives the list preview — it just
renders opentree's own frame now instead of raw agent output. *(The preview was
later retired in favour of the socket's own status — see deviations.)*

## Why not the alternatives

**Why not `opencode serve`?** It works — one server can host sessions in many
directories, and `opencode attach` can join a live session, which ACP cannot. But it is
opencode-specific, and the goal is a client that generalises to other agents.

**Why not a broker daemon?** Only needed if two UIs render the same live session
concurrently. The chat process is already long-lived in tmux, so it can hold the ACP
pipe *and* serve the socket itself. No supervision, no pidfiles, no orphan reaping.

**Why not a Go ACP library?** Four community libraries exist (`coder/acp-go-sdk`,
`ironpark/acp-go`, `eino-contrib/acp`, `spachava753/acp-sdk`) — none official, none with
a stated maintenance story, and a **v2 draft** is in flight. Our surface is JSON-RPC 2.0
over newline-delimited stdio plus ~10 message types. Hand-roll it, zero new deps; fall
back to `coder/acp-go-sdk` only if that drags.

## Verified facts (Spike 0)

Recorded from live `opencode acp` v1.18.12 traffic on 2026-08-07. Sanitized captures are
committed as `pkg/acp/testdata/` and are the source of truth for the Go types — the
protocol docs were not trusted over the wire.

```
initialize -> protocolVersion: 1
              agentCapabilities.loadSession: true
              sessionCapabilities: { close, fork, list, resume }
              promptCapabilities:  { embeddedContext, image }
              authMethods: [ opencode-login ]
              agentInfo: { name: "OpenCode", version: "1.18.12" }
```

**Methods**

| Direction | Method | Shape |
|-----------|--------|-------|
| →  | `session/new` | `{cwd, mcpServers}` → `{sessionId, configOptions[]}` |
| →  | `session/load` | `{sessionId, cwd, mcpServers}` → replays history as `session/update` notifications, then `{configOptions}` |
| →  | `session/prompt` | `{sessionId, prompt: ContentBlock[]}` → `{stopReason, usage{inputTokens,outputTokens,totalTokens,cachedReadTokens,cachedWriteTokens}}` |
| →  | `session/cancel` | notification (no id); the in-flight prompt then returns `stopReason: "cancelled"` |
| ←  | `session/request_permission` | `{sessionId, toolCall, options[{optionId,kind,name}]}` → `{outcome:{outcome:"selected", optionId}}` |

**`session/update` variants** (discriminator: `sessionUpdate`)

`agent_message_chunk` · `agent_thought_chunk` · `user_message_chunk` (replay only) ·
`tool_call` · `tool_call_update` · `available_commands_update` · `usage_update`

- Message chunks carry `{messageId, content:{type:"text", text}}`.
- `tool_call`: `{toolCallId, title, kind, status, locations[], rawInput}`.
  Kinds seen: `read`, `edit`, `execute`. Statuses: `pending`, `in_progress`,
  `completed`, `failed`.
- `tool_call_update` is a sparse patch — every field except `toolCallId` is optional, and
  `kind`/`title` are frequently absent on the terminal update. **The client must merge
  updates into retained state, not replace.**
- Tool content blocks are wrapped, not bare: `{"type":"content","content":{ContentBlock}}`
  and `{"type":"diff","path","oldText","newText"}`.

**Behaviours that shape the implementation**

- **Agent→client requests use a separate ID space starting at 0**, colliding with the
  client's own outbound IDs. The client must key pending calls by direction, not by id alone.
- Rejecting a permission produces `tool_call_update` with `status: "failed"` and an
  explanatory content block — the turn continues rather than aborting.
- opencode offers exactly three permission options: `once`/`allow_once`,
  `always`/`allow_always`, `reject`/`reject_once`. There is **no `reject_always`**, so the
  UI must render options from the wire rather than hardcoding four.
- `--cwd` sets the session directory.
- `opencode acp --port` does **not** serve HTTP — ACP mode is stdio only, so an ACP
  session and an attached `opencode` TUI cannot share one process.
- An ACP session has exactly one live client. `loadSession`/`resume` mean a *later*
  client can re-open a session with full history, but not concurrently.

### Two findings that change scope

1. **opencode never emits `plan` updates.** Prompting it to use its todo tool produced
   `tool_call` updates for `todowrite`, not a `plan` notification. The plan panel in
   commit 4 would render nothing. Either drop it, or derive it from the `todowrite`
   tool call's `rawInput` — which is opencode-specific and violates decision 9.
2. **`usage_update` and `configOptions` are free wins not in the original plan.**
   `usage_update` carries `{used, size, cost:{amount,currency}}` — live cost and context
   usage with no extra work. `session/new` returns `configOptions` describing model,
   effort, and mode as select controls with current values, which is a model/mode picker
   handed over on a plate. Neither is in base ACP; both degrade to absent for other agents.

## Decisions

| # | Decision |
|---|---|
| 1 | tmux attach stays the primary way you work; the thing you attach to is now opentree's chat |
| 2 | No `opencode serve`, no broker daemon — the chat process *is* the server |
| 3 | Integrated path only for opencode; no plain-launch fallback *(the registry later grew to four agents, all ACP — see deviations)* |
| 4 | Chat view is **altscreen**, full control |
| 5 | Sessions persist: `session_id` in `state.json`, `session/load` replays history on reopen *(the one id later grew into a ledger — see deviations)* |
| 6 | **Bidirectional unix socket** — answer permissions and interrupt from the list |
| 7 | List shows status + control; preview stays `capture-pane` (no second renderer) *(the preview was later retired outright — see deviations)* |
| 8 | No opentree permission policy — opencode's ruleset decides, opentree surfaces escalations |
| 9 | Client written protocol-generic; `PredefinedAgent.ACP *ACPSpec`, only opencode filled in *(later: a value, four agents filled in — see deviations)* |
| 10 | **Fat v1**: slash commands, `@` mentions, plan panel, thought blocks — and no escape hatch |
| 11 | Fail loudly + `[r]` restart; auth-required offers `[l]` → `opencode auth login` in-window |
| 12 | Ship `opentree chat` standalone, flip the create flow last |

### Agent registry

```go
type PredefinedAgent struct {
    Name, Command string
    Args          []string
    ACP           *ACPSpec // nil = plain launch, unchanged
}

// opencode: &ACPSpec{Args: []string{"acp"}, CwdFlag: "--cwd"}
// claude, codex, copilot, gemini, pi: nil
```

Adding Gemini later is a registry entry, not a rewrite. *(It was: the shipped
registry holds opencode, Claude Code, Copilot and Gemini, each an entry — with
`ACP` a value rather than a pointer, since every agent now has one. See
deviations.)*

### Permissions

opencode applies its own ruleset first and only escalates what it cannot decide.
opentree never auto-answers: an escalation becomes a badge in the list and a prompt in
the chat, answered with `allow_once` / `allow_always` / `reject_once` / `reject_always`.
Users wanting autonomy configure opencode's ruleset — opentree adds no second policy
engine.

## Sequence

**Spike 0** — record real JSON from `opencode acp`: `session/new` with `--cwd` on a
worktree, a prompt round-trip, a full `tool_call` lifecycle, and a live
`session/request_permission`. Type everything downstream off these captures, not off the
spec.

| Commit | Scope | Status |
|--------|-------|--------|
| 0 | Plan, plus recorded `opencode acp` traffic committed as `pkg/acp/testdata`. | done |
| 1 | `pkg/acp` plus a line-based `opentree chat` to drive it. | done |
| 2 | Altscreen chat view replacing the line UI. | done |
| 3 | Diffs, failure detail, restart, and guided auth. | done |
| 4 | Slash commands, `@` mentions, reasoning toggle. | done |
| 5 | Control socket: badges, answer, interrupt, message from the list. | done |
| 6 | Flip `workspace.Service` to launch the chat; retire opencode's hooks. | done |

Commits 1–5 changed nothing for existing users. The other five agents keep
`OPENTREE_STATUS_FILE` and their hooks untouched throughout. *(True while the
six commits landed; the branch then committed to ACP outright and the hooks and
status files left with the plain-launch path — see deviations.)*

## Not built

- **Sending a prompt from the list is one-way.** `m` delivers the text and the
  reply lands in the chat window; the list shows only the resulting status. A
  conversation still means attaching, which is decision 1 working as intended.
- **No `reject_always`.** opencode does not offer it, and the UI renders the
  options it receives rather than inventing one.
- ~~**The plan panel** was dropped for the reason in finding 1.~~ Restored
  later: the Claude Code adapter does emit `plan` updates, so the chat renders
  the checklist for the agents that send one. opencode still never does, and
  for it the panel simply never appears — which is what finding 1 predicted.

## Implementation calls already made

- `fs: false` / `terminal: false` in `initialize` — those capabilities exist for editors
  with unsaved buffers; opentree has none, so opencode does its own IO. Diffs still
  arrive as `toolCall` content.
- Session created lazily on first chat launch, not at workspace creation.
- tmux invokes opentree via `os.Executable()`, not `PATH`.
- Stale sockets unlinked on bind.
- The list holds sockets for all opencode workspaces, not just the selected one.

## Risks

1. **Long runway.** Fat v1 with no fallback means the final commit is the first day
   anyone benefits. The sequencing contains this — but if commit 4 slips, resist
   flipping early.
2. ~~**Altscreen copy/paste.**~~ **Resolved in commit 2 — then deliberately
   reversed.** The chat first ran altscreen *without* mouse mode, so terminal and
   tmux text selection kept working natively — the opposite call from the main
   list (`tea.WithMouseCellMotion`), which is what caused #34. Living with it
   showed the sharper problem runs the other way: without mouse capture the
   wheel scrolls the terminal's own buffer, walking out of the alt screen into
   whatever the shell printed before opentree started (commit 3036adf). Both
   screens now capture the mouse, the wheel scrolls the conversation, and
   selection is the terminal's shift-drag — the trade #34 warned about, taken
   knowingly this time. The rationale lives on `p()` in `pkg/chat/model.go`.
3. **ACP v2 draft.** The hand-rolled client is the hedge; budget a version-negotiation
   pass.
4. ~~**`capture-pane` on an altscreen app.**~~ **Resolved in commit 2, then made
   moot.** Captured live from tmux, the frame rendered cleanly — decision 7 held
   for as long as there was a preview. The preview itself was later retired: the
   socket's live status answers the question it existed for (see deviations),
   and with it went the last `capture-pane` call.

## Deviations from the plan as written

- **Commits 1 and 2 of the original sequence were merged.** `make check` runs
  `go tool deadcode ./cmd/opentree`, and tests do not count as reachability, so a
  `pkg/acp` commit with no caller in the binary cannot pass the gate. Commit 1 is
  therefore the client plus a line-based `opentree chat`, and commit 2 replaces that line
  UI with the altscreen view. Six commits, same content, every one of them green.
- **The plan panel is dropped** (see finding 1). opencode emits no `plan` update, so
  commit 4 covers slash commands, `@` mentions, and thought blocks only.

The six commits above landed as described. The branch then kept working, and
the code moved past this plan in six places — recorded here rather than
silently, because a plan that contradicts the code teaches the wrong thing:

- **The registry did not stay opencode-only, and the plain launch is gone.**
  Decision 3's fallback was deleted outright; the registry ships four agents —
  opencode, Claude Code (through the `claude-agent-acp` adapter, installable
  from the agent list), GitHub Copilot and Gemini CLI — every one of them ACP.
  `ACP` became a value rather than a pointer since every agent has one, Codex
  and Pi left the registry, and the hooks/`OPENTREE_STATUS_FILE` machinery
  went with the plain path. SKILLS-PLAN.md records the same shift from the
  skills side.
- **The list preview is gone** (decision 7). The socket already reports what
  the window is doing — state, current tool, cost, a queued prompt, a pending
  permission — and those badges answered the question the `capture-pane`
  preview existed for. The preview was retired, and with it the constraint
  that kept it: there is no second renderer because there is no preview at all.
- **One session id became a ledger** (decision 5). `state.json` keeps the last
  twenty conversations per workspace, each stamped with the agent that made
  it, which is what `/resume`'s picker runs on and what stops one agent being
  handed another's session id.
- **The plan panel came back** (decision 10, finding 1). The Claude Code
  adapter emits `plan` updates, so the chat draws the checklist for agents
  that send one; opencode still never does and never sees it.
- **Mouse capture was reversed** — risk 2 tells the story.
- **Images joined the prompt surface.** ctrl+v lifts an image off the
  clipboard, a path dragged onto the terminal becomes an attachment, and both
  travel as ACP image blocks — only where the agent's `promptCapabilities`
  declare image support, as the protocol requires.
