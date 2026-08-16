# opentree × Notifications — Design & Plan

> An agent that stops to ask is an agent that has stopped. opentree is the only
> thing that knows, and it only says so while someone is looking at it.

## The thing worth building

opentree's premise is that you run four agents at once. The cost of that premise
is that idleness becomes invisible: the one workspace blocked on a permission
prompt looks exactly like the three that are working, unless you are staring at
the list.

And the list is the only place that knowledge exists. `readChatStatus` dials each
workspace's control socket every ten seconds and throws the answer away on the
next refresh. Close the dashboard, or switch to any other tmux window, and a
workspace can sit blocked for an hour on a question you would have answered in
two seconds.

Three parts, one problem:

- **notice** — the chat process detects the moment it starts needing a human.
- **carry** — a tmux bell and an OS notification take that moment out of the window.
- **return** — one key on the dashboard takes you to whichever workspace has been
  waiting longest.

The third is what makes the first two worth having: a notification you cannot act
on is an interruption.

## Verified facts

Established by reading the tree, not assumed.

1. Every state change funnels through one place. `Model.Update`
   (`pkg/chat/update.go:22`) computes the next model, then calls
   `nm.publish(nm.status())` — and its comment says so: *"Publishing here rather
   than at each call site is the whole point: every state change funnels through
   this method, so the socket cannot drift out of date."* That is exactly the
   edge-detection point a notifier needs.
2. `status()` (`pkg/chat/update.go:33`) collapses the model into one of six
   states by precedence: a pending permission → `awaiting_permission`; an active
   setup phase → `setting_up`; `dead || authNeed` → `stopped`; a live turn →
   `working`; no session id → `starting`; otherwise `idle`
   (`pkg/chat/socket.go:20-25`).
3. The dashboard polls every **10 seconds** (`pkg/tui/model.go:367`), reading
   statuses inside `loadWorkspacesCmd` (`pkg/tui/commands.go:106`). Edge
   detection from that poll would miss every turn shorter than the tick, and
   would produce nothing at all with the dashboard closed — which is the case
   the whole feature exists for.
4. `recordChatErrors` (`pkg/tui/update.go:1152`) is the precedent for turning a
   published status field into a side effect: it scans every refresh and skips
   messages already in the log. It is deliberately *level*-triggered, because a
   setup failure persists on the socket until the window is dealt with. A
   notification is the opposite: the moment is the whole content.
5. **Window names cannot carry a marker.** Lookup is by exact sanitized name
   everywhere — `findWindowID` matches `w.Name == sanitizeWindowName(name)` in Go
   (`pkg/tmux/tmux.go:165`), and the dashboard builds `windowMap[w.Name]`
   (`pkg/tui/commands.go:56-83`). Renaming a window to prefix a `!` would break
   `SelectWindow`, `AttachWindow`, `KillWindow`, `GetWindowActivity`,
   `PaneCurrentCommand`, `EnsureWindow` and the dashboard's own row-to-window
   mapping, all silently.
6. **tmux already has the marker.** Verified against tmux 3.7b: `monitor-bell` is
   `on` by default and `window-status-bell-style` is `reverse`, so a bell
   delivered into a pane makes its window render inverted in the status bar with
   no configuration and no rename. `#{window_bell_flag}` is readable through
   `list-windows -F`, and the flag clears when the window is selected.
7. **A chat can tell whether anyone is looking at it.** tmux exports `TMUX_PANE`
   into every pane it starts, and
   `tmux display-message -p -t <pane> '#{window_active}|#{session_attached}'`
   answers both halves — verified returning `1|0` for a live window in a detached
   session. Both halves are needed: an unattached session still has a
   "current" window.
8. Every chat runs inside a tmux window created by `CreateAppWindow`
   (`pkg/tmux/tmux.go:59`), which execs the program directly so the pane's process
   *is* the chat. tmux ≥3.0 is already a hard requirement (`checkVersion`,
   `pkg/tmux/tmux.go:119`).
9. `osascript` is the established platform shell-out (`pkg/chat/clipboard.go:62`),
   bounded by a 3s context, with Linux handled by trying `wl-paste` then `xclip`
   and Windows absent *"because opentree needs tmux"*.
10. The chat is configured through `Options` (`pkg/chat/model.go:30`) and is not
    handed a `*config.Config`. Anything new it must know travels as a field.
11. ACP-PLAN.md decision 8 stands: *"No opentree permission policy — the agent's
    ruleset decides, opentree surfaces escalations."* A notification is a surface.
    An auto-answer would be a policy, and is out of scope by a decision that was
    already made.
12. There is no aggregate and no attention ordering today: sort modes are
    name / age / activity / PR (`pkg/tui/model.go:48-54`), and `StateAwaiting` is
    read in exactly two places, both per-row (`pkg/tui/agentctl.go:40,99`).
13. **`chat.Status` carries no time.** Nothing anywhere records *when* a workspace
    became blocked — which is precisely what "the one that has been waiting
    longest" needs.
14. Free lowercase keys on the Workspaces tab: `b e f g h l t u v y z`. Taken:
    `up/k down/j n i r enter d p o R x space / s A a c m w E tab q ?`
    (`pkg/tui/keys.go`).

## Decisions

| # | Decision |
|---|---|
| 1 | Scope is **notice, carry, return**. No auto-answer, no policy engine, no remote delivery. |
| 2 | **The chat process emits, never the dashboard.** The chat is the only thing that exists when the dashboard does not, and fact 1 gives it a single exact funnel. The dashboard emits nothing, so two of them running cannot double-notify. |
| 3 | **Edge-triggered on the state machine of fact 2**, not level-triggered on a field. This is the opposite of `recordChatErrors` (fact 4) and deliberately so: an error persists and wants to be seen once; a transition happens once and wants to be carried immediately. |
| 4 | Three events. **`blocked`** (`* → awaiting_permission`), **`done`** (`working → idle`), **`stopped`** (`* → stopped`, including a failed setup phase). |
| 5 | `done` fires only from **`working`**. `starting → idle` is a chat finishing its handshake, not a turn finishing its work, and notifying on it would ring every window at launch. |
| 6 | **Suppress when the human is already looking**: the pane's window is active *and* its session has an attached client (fact 7). One `display-message` exec per transition — rare, not per frame. |
| 7 | **Outside tmux, notify nothing.** No `TMUX_PANE` means someone ran `opentree chat` by hand in a terminal they are sitting in front of. Guessing loudly is worse than staying quiet. |
| 8 | The tmux surface is a **bell**, not a rename. Fact 5 makes renaming unavailable and fact 6 makes it unnecessary — tmux's own bell styling is the marker, for free. |
| 9 | The bell is one byte written to the **pane's own tty**, opened separately, rather than to the chat's stdout. Bubble Tea owns stdout and renders whole frames from its own loop; a notifier goroutine writing into that stream is a race for no gain. |
| 10 | The desktop surface is **`osascript` on darwin, `notify-send` on linux**, best-effort and bounded by a context timeout — the shape fact 9 already established. No Windows, for the reason already written down there. |
| 11 | Notifications are **read-only signposts**. macOS cannot attach an action to an `osascript` notification without bundling an app or requiring `terminal-notifier`, so no surface pretends to be clickable. The action is decision 18's key. |
| 12 | Events are configurable, **surfaces are not** (beyond desktop on/off). Which events matter is a real preference; "bell but no banner for permissions, banner but no bell for turns" is a matrix nobody wants to hold. |
| 13 | Defaults: **`blocked` and `stopped` on, `done` off.** Something that needs a human is worth a banner; a finished turn is worth seeing when you get back. Four agents finishing turns is a banner every ninety seconds, and a notifier you mute is a notifier you deleted. |
| 14 | `[notify]` is **global config only** — a repo's `opentree.toml` cannot set it. This inverts the bootstrap plan's decision 5 on purpose: a bootstrap sequence is how the *project* builds, so it belongs to the project; how you like to be interrupted belongs to you. A cloned repository should not be able to start sending you desktop banners. |
| 15 | **Flap guard: a 10s cooldown per `(workspace, event, fingerprint)`.** The fingerprint is the permission title for `blocked` and empty otherwise, so a genuine second escalation with a different question is never swallowed while a status that momentarily flickers is. |
| 16 | **`chat.Status` gains `Since time.Time`** (fact 13), stamped when the published state differs from the last published one — the same observation decision 3 already makes. |
| 17 | `Since` earns a second keep immediately: the row renders **`blocked 12m`** in `chatMeta`, and the header shows **`2 waiting`**. The notification tells you once; the elapsed time is what makes you go and deal with it. |
| 18 | **One new key: `b` — next blocked** (fact 14). It moves the cursor to the workspace blocked longest and cycles on repeat. |
| 19 | **A key, not a sort mode.** Sorting is a preference you set once; "who needs me *now*" is a question you ask at a moment. A list that reordered itself every ten seconds as agents changed state would be unusable for everything else it is for. |
| 20 | `pkg/notify` **does not import `pkg/chat`** — it observes a `notify.Signal{State, Detail}` and the chat maps its `Status` onto it. Otherwise the import cycle is immediate, and a notifier that knows about ACP tool calls is a notifier that cannot be tested without one. |
| 21 | Senders sit behind an **interface**, so the watcher's tests never shell out and the platform code is the only untested part. |
| 22 | **`opentree notify test`** sends one of each event. macOS silently drops `osascript` notifications until the user allows them for Script Editor, which is otherwise a bug with no symptom and no error. |
| 23 | **Nothing is notified when a chat disappears.** The socket going quiet usually means the user pressed `q`, and notifying someone about a thing they just did is how a notifier loses their trust. The dashboard already says the chat is not running. |
| 24 | Notifications are **not written to the error log**. The log is for failures that need a record; a notification is a moment, and `stopped` already reaches the log through the path fact 4 describes. |

### On decision 2, and why not the dashboard

The dashboard is the tempting place. It already dials every socket, it already
has the config, it already renders the badge — `recordChatErrors` proves the
shape works, and it would be perhaps forty lines.

It also cannot work. Fact 3 is the objection: the dashboard polls at 10s, so a
turn that starts and finishes between two ticks is a `done` that never happened,
and — fatally — a dashboard that is not running notifies nobody about anything.
The entire premise is "tell me when I am not looking at the list", and the list
is the one component guaranteed to be absent at that moment.

The chat process is the opposite in every respect. There is one per workspace, it
lives as long as the window, it holds the ACP connection, and fact 1 hands it a
single funnel through which every state change already passes. The dashboard's
job here is not to detect anything. It is to be the place you land.

### On decision 8, and the marker that was already there

The original pitch said "tmux bell **plus** a window-name marker" — mark the
window `!feat-dark-mode` so the status bar shows which one wants you.

Fact 5 kills it. Every tmux operation opentree performs resolves a window by
exact name: attach, select, kill, activity, pane command, and the dashboard's own
`windowMap[w.Name]` join. A renamed window is a window opentree can no longer
find, and the failure is silent — `EnsureWindow` would decide the chat had died
and launch a second one beside it.

Fact 6 makes the loss free. tmux's `monitor-bell` is on by default and
`window-status-bell-style` is `reverse`, so the bell decision 9 delivers *is* the
marker: the window renders inverted in the status bar until you select it, which
is exactly the semantic wanted — including the clearing, which a rename would
have had to implement and get right.

One byte, no rename, no new state, and it works in a status bar the user
configured themselves.

### On decision 16, and one mechanism serving two features

`Since` looks like scope creep on a notifications feature. It is the opposite: it
is the same observation, kept.

Decision 3 requires comparing the newly computed status against the last
published one. Once that comparison exists, "the state changed at T" is already
in hand, and throwing it away means the dashboard has to reconstruct it — badly.
The alternative (fact 13's absence) is for the dashboard to remember when it
first *saw* a workspace blocked, which is wrong exactly when it matters: a
workspace that has been waiting forty minutes, in a session you opened the
dashboard against ten seconds ago, would report ten seconds.

So one field, stamped where the edge is already detected, and it pays for:
ordering decision 18's cycle, the `blocked 12m` on the row, and a `done` event
that can say how long the turn took.

### On decision 14, and what a repository may ask of you

The bootstrap plan put `setup` and `run` in the tracked `opentree.toml` and then
spent a trust gate defending it, because a bootstrap sequence really is a
property of the project.

Notification preference is not. It is a property of the person and the room they
are sitting in, and there is no version of "this repository would like to send
you desktop banners" that is a reasonable thing for a clone to be able to say.
The merge in `config.Load` walks defaults → global → repo generically, so this is
an explicit strip of the `[notify]` section from the repo layer — five lines, and
the alternative is a second trust gate for a feature that does not need one.

## Config

Global `opentree.toml` only (decision 14):

```toml
[notify]
on      = ["blocked", "stopped"]   # also "done"; [] disables everything
desktop = true                     # false: tmux bell only
```

## Data model

`chat.Status` gains:

| Field | Why |
|---|---|
| `Since time.Time` | when the published state last changed; orders the `b` cycle and renders `blocked 12m` |

`notify.Watcher` holds the previous signal and a small cooldown map. Nothing is
persisted — a notification is a moment, and a restarted chat has a restarted
state to go with it.

## Commit sequence

| # | What | Files | Status |
|---|---|---|---|
| 1 | `Since` on `chat.Status`, stamped when the published state changes; `blocked 12m` in `chatMeta`; `N waiting` in the header. **No notifications yet — useful on its own.** | `pkg/chat/{socket,update,model}.go`, `pkg/tui/{agentctl,view}.go` | done — `14aa1f0` |
| 2+3 | `pkg/notify`: `Signal`/`Event`/`Watcher` — edge detection, the three events, the 10s fingerprinted cooldown, senders behind an interface. The tmux surface: BEL to the pane tty, visibility suppression via `TMUX_PANE`, wired into `chat.Update` beside `publish`. | `pkg/notify/{notify,watcher,tmux}.go` (new), `pkg/chat/{update,model}.go`, `cmd/opentree/cmd/chat.go` | done — `e233710` |
| 4 | Desktop senders (`osascript`, `notify-send`), the `[notify]` config block, repo-layer strip, `Options` plumbing. | `pkg/notify/desktop.go` (new), `pkg/config/config.go`, `cmd/opentree/cmd/{chat,config}.go`, `README.md` | done — `ef80bc2` |
| 5 | `b` — next blocked, ordered by `Since`, cycling on repeat; help entries. `opentree notify test`. | `pkg/tui/{keys,update,agentctl,view}.go`, `cmd/opentree/cmd/notify.go` (new), `README.md` | done — `7359b07` |

Commit 1 stands alone: it is the elapsed-time badge, and it is worth having
whether or not anything is ever notified. Commits 2–3 are the feature for anyone
who lives inside tmux. Commit 4 is the one that reaches you with the terminal
closed.

### What the plan did not know

- **Commits 2 and 3 landed as one.** `make check` runs `go tool deadcode
  ./cmd/opentree` and fails on anything unreachable from `main`, so a `pkg/notify`
  with no caller cannot be committed on its own. The split was a description of
  two ideas, not two commits; the tmux surface is what makes the watcher
  reachable.
- **Decision 17's header half was already built.** `statusBar` has counted
  `N waiting` from `pendingPermission` since before this plan. Commit 1 was the
  elapsed time on the row; commit 5 added the `(b)` beside the count.
- **Fact 4's `stopped` path is not `StateStopped`.** A failed setup phase
  publishes `setting_up` with `Error` set, not `stopped` — `setup.active()`
  includes `setupFailed`. `signalOf` maps that pair onto `notify.StateStopped`,
  which is what decision 4 asks for and what the states alone would have missed.
- Facts 6 and 7 were re-verified against tmux 3.7b on this machine, including
  end-to-end: a Go program using `notify.Bell` inside a tmux window sets
  `window_bell_flag` on the window it runs in, and `Watched` answers true only
  with a client attached to the session showing that window.

## Not built

- **No auto-answer, no allow-lists, no permission policy.** Fact 11 — that
  decision was made in ACP-PLAN.md and this feature stays inside it.
- **No actionable notifications.** Decision 11. Clicking through to a workspace
  needs a bundled app on macOS or a second binary to install, for an action that
  is already one keystroke away in the dashboard.
- **No OSC 9 / OSC 777 terminal-native notifications.** They work in iTerm2,
  WezTerm, kitty and Ghostty, and need tmux `allow-passthrough` plus wrapping in
  a passthrough sequence — a capability opentree cannot detect and a server
  option it should not be setting on the user's behalf.
- **No push, webhook, Slack or phone delivery.** A different feature, wanting
  authentication, retries and a place to store a secret.
- **No notification history.** Decision 24: the error log is the durable surface.
- **No per-workspace mute, no quiet hours, no sound selection.** Real requests, all
  of them, and none of them until someone asks.
- **No `chat vanished` event.** Decision 23.
- **No repo-level `[notify]`.** Decision 14.
