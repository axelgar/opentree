# opentree × chat polish — the plan

> Goal: a developer who lives in Claude Code's or opencode's own chat attaches to
> an opentree chat and finds nothing missing that they reach for daily —
> rendered prose, live feedback, queued typing, expandable evidence, working
> mode cycling — on every agent, in light and dark terminals.

## Verified facts

Read from the code, not from memory:

1. **Rendering has one seam.** `relayout()` (`pkg/chat/update.go`) re-renders
   every entry through `renderEntry` (`pkg/chat/view.go`) into one string and
   `SetContent`s the viewport. `renderEntry` is a pure function of
   `(entry, width, spinnerFrame, hideThoughts, cwd, brand)`. That one seam is
   what makes both a markdown pass and a per-entry cache tractable.
2. **The spinner is frozen because ticks skip relayout.** `spinnerTickMsg`
   advances `m.spinnerFrame` and returns without `relayout()`; the viewport
   keeps its cached content, so `⠋ thinking…` and the running-tool glyphs stand
   still between agent chunks. The comment in `renderEntry`'s bullet helper
   ("re-rendered ten times a second while a turn is live") describes the
   behaviour before the regression; fixing the tick makes it true again.
3. **Enter during a turn is silently dropped** — the text stays in the box and
   nothing says why. The only queue is the single remote slot `m.queued`,
   filled solely by the socket's `CommandPrompt`. `turnSource` gates
   attachments: only `typedHere` carries `m.pending` images.
4. **Agent prose is one flat style.** No markdown anywhere except `unfence()`,
   which strips a fence off tool output. Deps are bubbles/bubbletea/lipgloss
   only; lipgloss wrapping is ANSI-aware, so pre-styled inline spans survive it.
5. **Truncation caps with no escape hatch**: `outputMaxLines = 8`,
   `diffMaxLines = 12`; the ponytail in `renderOutput` names the constraint —
   no cursor in the viewport, no per-entry state. `entry` has room for that
   state.
6. **Notices neither wrap nor truncate** — the only entry kind that does
   neither; a long adapter stderr line pushes the log sideways.
7. **Modes are opencode-shaped only.** Session responses drop the classic ACP
   `modes` object; there is no `session/set_mode`. But `current_mode_update`
   *is* decoded and folded into whatever config option has
   `Category == "mode"` — so the whole mode UI (flags line, ctrl+g picker,
   shift+tab, `/mode`) hangs off `[]ConfigOption`. That is the unification
   seam: synthesize a config option from classic modes and everything
   downstream works untouched.
8. **`Model` is a value; a cache must be a pointer field.** Every handler
   returns a copied Model.
9. **Free keys**: ctrl+x and ctrl+r are unclaimed by the textarea and the chat.

## Decisions

| # | Decision |
|---|----------|
| 1 | Markdown is hand-rolled in `pkg/chat/markdown.go` (~300 lines): headings, bold/italic, inline code, fenced blocks on a `ui.Band` background, lists, blockquotes. Not glamour — it drags goldmark, picks its own light/dark theme against the adaptive palette, and re-renders whole documents at streaming rate. |
| 2 | chroma v2 is used as a **lexer only** for fence contents; a small painter maps token categories onto adaptive palette colours. Its formatters and themes stay unused. The dependency lands in its own commit so it is revertible without touching the renderer. |
| 3 | Renderer contract: pure, re-runnable, stable on prefixes. An unterminated fence stays an open code block; an unmatched inline marker renders literally until its closer arrives. Emphasis never crosses a blank line, so the snap window is one paragraph. |
| 4 | Markdown applies to agent prose only. Thoughts stay plain italic, the user echo stays verbatim, tool output keeps `unfence`. Code-block lines are truncated plain, padded, then painted — `ui.Truncate` never sees styled text. |
| 5 | Spinner: reinstate relayout-on-tick and keep live lines in the log. The running-tool glyph lives inside entries and cannot move to a footer. The 10Hz cost is made cheap by decision 6. |
| 6 | Perf: revision-keyed per-entry memo. `entry.rev` bumps at every mutation; `renderCache` (a pointer field) maps entry index → `{rev, width, rendered}`. Running tools are uncacheable — their glyph animates. Width change or log reset drops the cache whole. |
| 7 | Tool expansion is inline per-entry state, not a pager: ctrl+x toggles the most recent entry holding something back, and the held-back line is the affordance (`… 42 more lines · ctrl+x`). Expanded output hard-caps at 500 lines. No cursor needed — "the last collapsed thing" is what you were just looking at. |
| 8 | The local send queue replaces the single remote slot: enter during a turn queues (visibly, above the input); backspace on an empty composer pops the newest queued prompt back into the box, images and all — edit and cancel in one gesture. The socket path rides the same queue; `Status.Queued` still reports the head, so the dashboard protocol is unchanged. |
| 9 | Classic ACP modes are synthesized into the config-option world, not given a parallel UI. `session/set_mode` is added; when the agent sent `modes` and no mode-category option, one is synthesized and routed through it. shift+tab, `/mode`, ctrl+g and the flags line then work with zero further change. |
| 10 | Small fixes ride where they fit: notices wrap; esc with no turn clears an unsent message (recorded to history, so ↑ undoes it); ctrl+r retries a failed turn with the stored blocks, so images survive the retry. |

## Commit sequence

Each commit is green under `make check` on its own; every new function is
reached from the live path in the commit that introduces it, or `deadcode`
fires.

| # | Commit | Status |
|---|--------|--------|
| 1 | docs: chat polish plan | done |
| 2 | fix: keep the spinner and running tools animating through a quiet turn | — |
| 3 | fix: wrap long notices; feat: esc clears a message not yet sent | — |
| 4 | feat: render the agent's prose as markdown | — |
| 5 | feat: colour the code the agent writes | — |
| 6 | perf: re-render only the entries that changed | — |
| 7 | feat: expand what a tool call held back | — |
| 8 | feat: queue messages typed while the agent is working | — |
| 9 | feat: retry a failed turn with one key | — |
| 10 | feat: session modes for agents that speak classic acp | — |
| 11 | docs: chat plan retired | — |

If the sequence stalls: 2, 4 and 8 are the three that move the "would I
switch" needle; 6 must land before long-session dogfooding of 2.

## Not built

- Tables, footnotes and setext headings in markdown; per-language completeness
  beyond what chroma's lexers give for free.
- An entry cursor or vim-style log navigation — ctrl+x's most-recent rule
  dodges the cursor problem the `renderOutput` ponytail warns about.
- A pager overlay; editing a queued prompt in place (pop-back is the edit).
- The untouched ponytails: per-run history, single-page `session/list`, image
  downscaling, inline image rendering, trailing-word completion. The `fs` and
  `terminal` client capabilities stay declared false — that is deliberate.

## Risks

1. **Streaming flicker** — bounded by the sticky-open-fence and
   literal-unmatched-marker rules, pinned by a streaming test. If agents chunk
   mid-marker often enough to annoy, the fallback is style-to-end-of-entry for
   `**` only.
2. **Wire vs spec on modes** — the ACP types here were derived from recorded
   traffic and the wire wins. Commit 10 decodes a captured fixture, not the
   published schema.
3. **deadcode** — the renderer, the painter and `SetSessionMode` each land
   wired in their own commit; tests do not count as reachability.
4. **Coverage ratchet** — the renderer decomposes into small block/inline
   functions, which are the most unit-testable code in the plan.
5. **Perf tail** — after the cache, the per-frame join and `SetContent` split
   stay O(total log bytes). Acceptable now; the follow-up (a joined-prefix
   cache or a rendered-history cap) is noted here, not built.
