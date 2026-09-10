# opentree — Diff viewer: design & plan

> Status: **all eight commits implemented.** Every `make check` target green
> locally except shellcheck (not installed here; no script changed). Departures
> from the plan as written are under *Found during implementation*.
> Scope: the dashboard's `d`/`D` diff view grows the parts of
> [revdiff](https://github.com/umputun/revdiff) that make a diff *reviewable*
> — a file tree, a cursor, hunk and file jumps, search, syntax colour,
> word-level diff — and the one part opentree is uniquely placed to do well:
> notes on lines that go to the workspace's agent as a prompt. Native, no new
> dependency, no shelling out to the revdiff binary.

## The thing worth fixing

The diff view is a scrolling string. `DiffCombined` returns one blob, the view
splits it on every frame, and a fifteen-line prefix match colours it. Reading
a two-thousand-line diff is `j` two thousand times or `pgdn` and hope; there is
no way to know which file you are in, no way to jump to the next one, nothing
to find with, and — the part that matters — no way to react to what you read
except to leave, open the chat, and describe the line from memory.

revdiff exists because that last gap is what agent review needs: read, mark
the lines, and have the marks come out as text the agent acts on. revdiff has
to bolt that onto an agent through a plugin and an exit code. opentree already
owns the channel: `R` sends PR review comments to the workspace's agent over
its chat socket. Notes made in the diff take the same road.

## Verified facts

Read from the code on 2026-09-10 at `8d4b751`. File:line references are to
that commit.

### 1. The view is four fields and a string

- `pkg/tui/model.go:154-158`: `diffViewing bool`, `diffContent string`,
  `diffScrollOffset int`, `diffWsName string`. `diffLoadedMsg{content, wsName}`
  (`:339`) is constructed directly by `fanout_test.go` and `agentctl_test.go`.
- `pkg/tui/view.go:112-149` splits `diffContent` on `\n` every frame, slices a
  window of `m.height - headerFooterHeight` (8) rows, paints each with
  `renderDiffLine` (`helpers.go:198`: `══`, `diff --git`/`---`/`+++`, `@@`,
  `+`, `-`, else plain). Nothing is truncated to the width; a long line
  overflows the frame.
- `pkg/tui/update.go:105-132` is a raw `msg.String()` switch: `esc`/`q`, `j`/`k`,
  `pgup`/`pgdn`/`ctrl+u`/`ctrl+d`, `g`/`G`, `home`/`end`. **`space` is page-down
  today.** Everything else is swallowed, so the open diff has its own keyspace.
  The wheel scrolls it (`update.go:1304`); clicks are refused while it is open
  (`update.go:1238`).

### 2. The string is a contract with three parties

- `pkg/worktree/worktree.go:745 DiffCombined` joins `git diff <merge-base> HEAD`
  and `git diff HEAD` under `══════ Committed Changes ══════` and
  `══════ Uncommitted Changes ══════`, the committed half unlabelled when it is
  alone, `No changes.` when both are empty.
- `pkg/tui/commands.go:604 buildGroupDiff` joins a fan-out's siblings under
  `══════════ name (agent) ══════════`, on purpose the same shape;
  `fanout_test.go:119` asserts the `══` prefix.
- `cmd/opentree/cmd/diff.go:56` prints the same string to stdout.

### 3. The pieces already exist, in the other program

`pkg/chat` has, package-private: `syntax.go` (chroma as a lexer only, painted
with the palette's five `Syn*` colours — chroma's own themes ignore the
terminal background); `find.go` (`findMatches` over ANSI-stripped rows,
`paintMatches` via `ansi.Cut`/`ansi.Strip`); `view.go:1185 diffLines` (an LCS
bounded by `maxDiffCells`). `pkg/ui`'s charter (`ui.go:1-9`) is exactly this
case: behaviour shared, styles per program.

### 4. The agent channel exists

`pkg/tui/agentctl.go:276 sendAgentCommand(wsName, action, chat.Command)` sends
a `chat.CommandPrompt` to the workspace's socket and reports back;
`ws.chatUnavailable()` (`:205`) is the guard; `R` at `update.go:531-543` is the
precedent. `github.FormatReviewsPrompt` (`github.go:368`) is the wrong prose
for a local review ("PR review comment(s) to address") and quotes no code.

### 5. Constraints

- `x/ansi` v0.11.8 has `Truncate`, `Hardwrap`, `Cut`, `Strip`;
  `chroma/lexers.Match(filename)` picks a lexer by file name.
- `go tool deadcode` runs in `make check`: a helper lifted into `pkg/ui` needs
  its second caller in the same commit, and `renderDiffLine` must go when it is
  replaced.
- Tests: `newTestModel`, `applyUpdate`, `keyMsg`, `clickAt`, substring
  assertions on `View()`. Fifty-one references to the four fields across four
  test files.

## Decisions

| # | Decision |
|---|---|
| 1 | **Parse the string; keep `DiffCombined`'s contract.** The `══` headers become section titles in the parser. worktree, the CLI and `buildGroupDiff` are untouched. |
| 2 | **One `diffView` struct in `pkg/tui/diff.go`** replaces the four fields. Parsing happens in `Update` on `diffLoadedMsg`, whose shape does not change. |
| 3 | **Flat rows are the parse output.** `parseDiff(content) ([]diffRow, []diffFile)`; a row knows its kind, text, old and new line number, file, word-diff partner and syntax spans; a file knows its section, paths, counts and row range. No hunk type — a hunk is a row of kind `h`, and `@@` seeds the counters. |
| 4 | **Slice window stays; no bubbles viewport.** The content is static and every highlight (cursor, match, word-diff) is a per-row decision at paint time; a viewport would want the whole thing re-rendered on every toggle. |
| 5 | **Cursor row; scrolling follows it.** `j`/`k` move the cursor and pull the window; page keys move both; `g`/`G` go to the ends; the wheel moves the window and pulls the cursor into it. `▎` in Accent marks it. A click in the body sets it; a click in the tree jumps to the file. |
| 6 | **A tree pane**, 28 columns, when `t` is on, there is more than one file, and the terminal is at least 100 wide. A heading row whenever the section changes — so `D` reads `sibling (agent)` › its files, and `d` gets Committed/Uncommitted headings for free. `✓ path  +3 -1 ●2`. |
| 7 | **Rows are `[▎][●][gutter][sign][text]`**, truncated with `…`. `w` hard-wraps instead and the window fills screen lines rather than rows. `L` shows two right-aligned line-number columns as wide as the widest number. |
| 8 | **Syntax colour is two streams per file.** Context+removed text is lexed as the old file, context+added as the new, once, by `lexers.Match` on the file name, capped at 256 KiB per file. Per-line lexing loses multi-line strings; one interleaved stream lexes `-foo(` `+foo(bar)` as noise. Added and removed rows get background bands (two new palette colours) so the sign survives the colour. |
| 9 | **Word-diff (`W`)** pairs a run of k removed rows with the k added rows that follow, i↔i, at parse. At paint the pair is tokenised (identifier runs, space runs, single runes), diffed with the lifted LCS, and the changed tokens are repainted bold+underlined. Paint order: syntax → words → search → prefix columns → truncate/wrap. |
| 10 | **Search lifts to `pkg/ui/find.go`** — `Match`, `FindMatches`, `Paint`, `PaintMatches` — and the chat wraps them. `/` opens the input in the footer slot; enter keeps the query, esc clears it. **`n`/`N` step matches while a query lives, otherwise `n`/`p` step files** — revdiff's rule. |
| 11 | **Notes.** `a`/`enter` on a code row (refused on headers with a footer notice; pre-filled when the row already has one), `A` on the file, `@` lists them (jump, delete), `x` deletes the note under the cursor, `s` sends. **The input is the footer bar, not a card** — a card would cover the line being noted. `●` marks noted rows; the footer shows the note under the cursor. No inline note rows: they would shift every row index. |
| 12 | **A dedicated prompt.** Numbered notes, `path:line (+)`, the line quoted, the note; a note ending in `?` or containing `??` is a `Question:` and the preamble says to answer the questions and make the other changes. Sent with `sendAgentCommand` under the `chatUnavailable` guard. A group compare has no single agent to send to, and says so. Sending clears the notes and leaves the diff open. |
| 13 | **`esc` with unsent notes arms**: the footer says `3 notes unsent — esc again discards, s sends`; the second `esc` closes. Notes die with the view on reload. |
| 14 | **`?` inside the diff** is a card listing its keys. The diff swallows every key, so the list's help cannot describe it. |

### On decision 1, parsing our own header back

The alternative is `DiffCombined` returning `[]Section` and `buildGroupDiff`
appending to it. It is cleaner and it touches worktree, the CLI, fan-out and
their tests for no visible change; the parser needs one `HasPrefix("══")`
branch either way to survive an old string. If a fourth consumer ever wants
structure, the parser moves down a package and the header goes.

### On decision 8, what chroma sees

Chroma is a lexer with state: a string opened on one line is still a string on
the next. A diff interleaves two files, so there is no single text that is
valid input. Two texts are — the old file's lines and the new file's lines,
each in order — and every row belongs to at least one of them. Context rows
consume a line from both and paint from the new one.

## Commit sequence

Each commit is green on its own and ships something.

| # | Scope | Files | Size | Status |
|---|---|---|---|---|
| 0 | **This document.** | `DIFF-PLAN.md` | S | done |
| 1 | **Parsed model, `diffView`, cursor** (decisions 1–5). `parseDiff`, rows and files, cursor-follows-scroll, `paintRow` replaces and deletes `renderDiffLine`. The screen is today's plus a cursor mark. Field renames in four test files. | `pkg/tui/diff.go`, `diff_test.go` (new), `model.go`, `update.go`, `view.go`, `helpers.go`, `styles.go`, the test files | L | done |
| 2 | **Tree and navigation** (decisions 6, 14). `t`, `[`/`]`, `n`/`p`, `space` marks reviewed (page-down keeps `pgdn`/`ctrl+d`), tree and body clicks, section headings, `?` card. | `diff.go`, `update.go`, `view.go` | M | done |
| 3 | **Truncate, `L`, `w`** (decision 7). | `diff.go` | S | done |
| 4 | **Search** (decision 10). | `pkg/ui/find.go`, `find_test.go` (new), `pkg/chat/find.go`, `pkg/tui/diff.go`, `update.go` | M | done |
| 5 | **Syntax** (decision 8). `highlight`/`codeSpan`/`tokenStyle` lift to `pkg/ui/syntax.go` as `Highlight(code, lexer) [][]Span`; the chat maps span kinds to its styles; two streams per file; bands. | `pkg/ui/syntax.go` (new), `palette.go`, `pkg/chat/syntax.go`, `styles.go`, `pkg/tui/diff.go`, `styles.go` | M | done |
| 6 | **Word-diff** (decision 9). `diffLines` lifts to `ui.Diff(old, new []string) []Edit` (keeps included; the chat filters them), the tokeniser, pairing, `W`. | `pkg/ui/diff.go` (new), `pkg/chat/view.go`, `pkg/tui/diff.go`, `styles.go` | M | done |
| 7 | **Notes** (decisions 11–13). | `pkg/tui/annotate.go`, `annotate_test.go` (new), `diff.go`, `update.go`, `view.go` | L | done |
| 8 | **Docs.** README's `d` line becomes a *Diff viewer* section; this file's statuses. | `README.md`, `DIFF-PLAN.md` | S | done |

If the sequence stalls after 2 the viewer is already better than today; after
7 it is the feature.

### Found during implementation

- **The span kinds are `ui.Kind*`, not `ui.Syn*`.** The palette already owns
  `SynKeyword` and friends as colours; a kind and a colour with one name would
  have been the ransom note the comment warns about.
- **`ui.Diff` keeps what it kept.** The chat's matcher dropped unchanged lines
  on the way out; the shared one emits them as `=` edits and the chat filters,
  because the word-diff needs the kept tokens to know where the changed ones
  sit.
- **The footer's key hints were the first thing to overflow.** At 100 columns
  a hint naming every key pushed the position off the bar, which is the bar
  silently dropping its right end. The hints name six things and point at `?`.
- **`q` arms like `esc`.** Two keys that close should not differ on whether
  they lose your notes.
- **No `enter` in the tree.** The tree has no focus of its own — the cursor is
  in the code, and the tree shows which file it is in. A click on a file jumps;
  `n`/`p` step. A focus toggle was a second cursor for a pane that fits on one
  screen.
- **lipgloss underlines a rune at a time.** Not a problem, but the word-diff
  test had to gather the segments rather than look for one.
- **The wheel-scroll and `G` tests moved onto the cursor.** Four tests asserted
  that `j` moved the window by one line; with a cursor it moves the cursor,
  and the window only when the cursor leaves it. Rewritten, not renamed.

## Tests

- **Parser** (`pkg/tui/diff_test.go`): a two-file fixture with a rename and a
  `@@ -10,3 +12,4 @@` hunk asserts paths, counts, the old and new number on
  every row and each file's row range; the Committed/Uncommitted and fan-out
  headers become sections; a binary and a deleted file; `No changes.` is one
  plain row.
- **Cursor and window**: `j` past the bottom moves the window by one; `G` puts
  the cursor on the last row; the wheel pulls the cursor into view; the paging
  test rewritten on the new fields.
- **Tree and keys**: the tree lists files with counts and hides for one file;
  `]` lands on the next hunk row, `n` on the next file; `space` shows `✓`; a
  click in the tree jumps, a click in the body selects; a group diff groups
  files under their sibling.
- **Width**: every rendered line fits the width; `w` shows the whole line;
  `L` shows and hides the gutter.
- **Lifts**: `pkg/ui/find_test.go`, `syntax_test.go`, `diff_test.go` carry the
  chat's cases across; the chat's own tests stay untouched and green.
- **Syntax**: a `.go` file gets spans and an unknown extension none; a string
  opened on a context line is still a string on the added line after it; the
  two bands pass the contrast test.
- **Word-diff**: 2 removed then 2 added pair, 2 then 3 do not; the changed
  word is emphasised and the unchanged one is not.
- **Notes**: enter captures path, line and code; a file note has no line; a
  hunk header refuses; the list jumps and deletes; the prompt marks `?` and
  `??` as questions and quotes the line; send produces the command and clears;
  send is refused when the chat is unavailable and from a group compare;
  esc arms then discards.
- **Help**: the `?` card names every key.

## Not built

- **Staged, untracked and `--only` sources.** `d` shows what `DiffCombined`
  shows: committed and uncommitted, tracked files.
- **Themes, keybinding files, a vim-motion preset.** The palette is the theme.
- **Multi-line notes, inline note rows, a blame gutter, collapsed and compact
  modes, horizontal scroll.** Each is a key away when someone wants it.
- **Persisted toggles.** `L`, `w`, `W`, `t` reset when the view closes; a
  `[diff]` config section is the upgrade path.
- **Sending notes from a group compare.** Three agents, one prompt, no right
  answer about who gets it.
- **Delegating to the revdiff binary.** Considered; the viewer would then be
  two viewers depending on `PATH`.
- **A standalone `opentree diff` TUI.** The CLI keeps printing the text.

## Risks

1. **Our own header parsed back.** A change to `DiffCombined`'s or
   `buildGroupDiff`'s header shape silently loses the sections. The parser's
   section test pins both strings.
2. **Chroma on a monster diff.** Lexing is once per file at load and capped at
   256 KiB per file; past the cap a file is plain. If a load still stalls, the
   upgrade is lexing a file on first paint.
3. **`n` means two things.** Files without a query, matches with one. revdiff
   made the same call and its users learned it; the `?` card says it in one
   line.
4. **Fifty-one renames in tests.** Mechanical, in commit 1, and the compiler
   finds every one.
5. **`space` stops paging.** `pgdn` and `ctrl+d` remain; the README says so.
