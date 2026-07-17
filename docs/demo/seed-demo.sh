#!/usr/bin/env bash
# Seed a throwaway opentree repo with a rich, deterministic dashboard for the
# demo recording — no live AI agents, no API keys, no origin remote.
#
# It builds a local clone of this repo under a temp dir, creates several git
# worktrees (real diffs), drops .opentree-status.json files whose mtimes select
# each agent-liveness badge, hand-writes .opentree/state.json, and starts a
# detached tmux session so the working/waiting rows get activity dots + a live
# "Agent Output" preview pane.
#
# Usage: docs/demo/seed-demo.sh [demo-dir]
# Then:  (cd <demo-dir> && opentree)   — or let demo.tape do it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$(cd "$SCRIPT_DIR/../.." && pwd)"                 # the real opentree repo
DEMO_DIR="${1:-${TMPDIR:-/tmp}/opentree-demo}"
DEMO_DIR="${DEMO_DIR%/}"
SESSION="opentree-$(basename "$DEMO_DIR")"             # matches getSessionName()

echo "seeding demo repo at $DEMO_DIR (tmux session: $SESSION)"

# --- clean slate -----------------------------------------------------------
tmux kill-session -t "$SESSION" 2>/dev/null || true
rm -rf "$DEMO_DIR"

# --- clone this repo, drop the remote so the PR-status poll can't mutate badges
git clone --quiet --local "$SRC" "$DEMO_DIR"
cd "$DEMO_DIR"
git remote remove origin 2>/dev/null || true
git checkout --quiet main 2>/dev/null || git checkout --quiet -b main
printf '.opentree-status.json\n.preview.txt\n' >> .git/info/exclude   # keep demo scaffolding out of the uncommitted count

san() { printf '%s' "${1//\//-}"; }                   # SanitizeBranchName: / -> -
ago_touch() { date -v-"$1" +%Y%m%d%H%M; }             # touch -t stamp, N ago (macOS)
ago_iso()   { date -u -v-"$1" +%Y-%m-%dT%H:%M:%SZ; }  # RFC3339 UTC, N ago

# make_worktree <branch> <n-lines> — real worktree with committed + uncommitted
# changes; n varies the diffstat per row so they don't all look identical.
make_worktree() {
  local br="$1" n="${2:-3}" dir=".opentree/$(san "$1")" i
  git worktree add --quiet -b "$br" "$dir" main
  # committed change (shows under "Committed Changes" + gives PR-gen commits)
  for i in $(seq 1 "$n"); do printf '// %s: refinement %d\n' "$br" "$i" >> "$dir/pkg/tui/helpers.go"; done
  git -C "$dir" add -A
  git -C "$dir" commit --quiet -m "wip: $(basename "$br") — adjust liveness badge rendering"
  printf '\n<!-- draft: %s -->\n' "$br" >> "$dir/README.md"
  git -C "$dir" add -A
  git -C "$dir" commit --quiet -m "docs: note $(basename "$br") behaviour"
  # uncommitted change (shows under "Uncommitted Changes" + "~N uncommitted")
  printf '\n\tfmt.Println("scratch: %s")\n' "$br" >> "$dir/pkg/tui/view.go"
}

# status_file <branch> <status> <mtime-touch|now> [message]
status_file() {
  local dir=".opentree/$(san "$1")" f
  f="$dir/.opentree-status.json"
  if [ -n "${4:-}" ]; then
    printf '{"status":"%s","message":"%s"}' "$2" "$4" > "$f"
  else
    printf '{"status":"%s"}' "$2" > "$f"
  fi
  [ "$3" = "now" ] || touch -t "$3" "$f"
}

echo "  creating worktrees…"
make_worktree "chore/update-docs"     2
make_worktree "feat/agent-liveness"   7
make_worktree "feat/native-terminal"  4
make_worktree "fix/diff-scroll"       3
make_worktree "refactor/state-store"  9

# agent-liveness badges via status file + mtime (staleAfter = 15m)
status_file "feat/agent-liveness"   in_progress now                "Editing pkg/tui/view.go"   # working…
status_file "fix/diff-scroll"       needs_input now                "Ready for review"          # waiting
status_file "chore/update-docs"     needs_input "$(ago_touch 2H)"                              # idle · 2h
status_file "feat/native-terminal"  in_progress "$(ago_touch 22M)"                             # stalled · 22m
# refactor/state-store: no status file — it's the merged row

# --- hand-write state.json -------------------------------------------------
echo "  writing state.json…"
wt() { printf '%s' "$DEMO_DIR/.opentree/$(san "$1")"; }
cat > .opentree/state.json <<JSON
{
  "workspaces": {
    "chore/update-docs": {
      "name": "chore/update-docs", "branch": "chore/update-docs", "base_branch": "main",
      "created_at": "$(ago_iso 5d)", "status": "idle", "agent": "claude",
      "worktree_dir": "$(wt chore/update-docs)", "branch_pushed": true
    },
    "feat/agent-liveness": {
      "name": "feat/agent-liveness", "branch": "feat/agent-liveness", "base_branch": "main",
      "created_at": "$(ago_iso 2H)", "status": "active", "agent": "claude",
      "worktree_dir": "$(wt feat/agent-liveness)", "issue_number": 47,
      "issue_title": "Show when an agent stalls"
    },
    "feat/native-terminal": {
      "name": "feat/native-terminal", "branch": "feat/native-terminal", "base_branch": "main",
      "created_at": "$(ago_iso 8d)", "status": "idle", "agent": "claude",
      "worktree_dir": "$(wt feat/native-terminal)"
    },
    "fix/diff-scroll": {
      "name": "fix/diff-scroll", "branch": "fix/diff-scroll", "base_branch": "main",
      "created_at": "$(ago_iso 1d)", "status": "active", "agent": "claude",
      "worktree_dir": "$(wt fix/diff-scroll)",
      "pr_url": "https://github.com/axelgar/opentree/pull/48",
      "pr_status": "open", "branch_pushed": true
    },
    "refactor/state-store": {
      "name": "refactor/state-store", "branch": "refactor/state-store", "base_branch": "main",
      "created_at": "$(ago_iso 12d)", "status": "stopped", "agent": "claude",
      "worktree_dir": "$(wt refactor/state-store)",
      "pr_url": "https://github.com/axelgar/opentree/pull/44",
      "pr_status": "merged", "branch_pushed": true, "remote_deleted": true
    }
  }
}
JSON

# --- tmux polish: dots + live preview pane for the working/waiting rows -----
# Only the working…/waiting rows get a window (a live pane would rescue an
# intended stalled/idle row back to "working…" via paneFresh).
echo "  starting tmux session…"
preview() {                                            # write a fake transcript
  cat > ".opentree/$(san "$1")/.preview.txt"
}
preview feat/agent-liveness <<'TXT'
● Editing pkg/tui/view.go
  Rewrote the badge switch to use badgeWithAge()
  Running go test ./pkg/tui/ …
  ok  github.com/axelgar/opentree/pkg/tui  0.79s
> working on it…
TXT
preview fix/diff-scroll <<'TXT'
● Ran go test ./pkg/tui/  →  253 ok
  Clamped the diff scroll offset in Update()
  Pushed fix/diff-scroll and opened PR #48
  Ready for your review — approve the PR?
> waiting for input
TXT

win_run='cat .preview.txt; exec sleep 100000'
tmux new-session  -d -s "$SESSION" -n tmp -c "$DEMO_DIR" "exec sleep 100000"        # throwaway seed window
tmux new-window   -t "$SESSION" -n "$(san fix/diff-scroll)"     -c "$(wt fix/diff-scroll)"     "$win_run"
tmux new-window   -t "$SESSION" -n "$(san feat/agent-liveness)" -c "$(wt feat/agent-liveness)" "$win_run"
tmux kill-window  -t "$SESSION:tmp"                             # safe now — real windows remain
tmux select-window -t "$SESSION:$(san feat/agent-liveness)"     # this one becomes the active ● dot

echo "done. run:  (cd \"$DEMO_DIR\" && opentree)"
