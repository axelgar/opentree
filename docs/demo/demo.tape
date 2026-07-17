# opentree — 30s product demo (hero cut)
#
# Run from inside the seeded demo repo (see seed-demo.sh):
#   bash docs/demo/seed-demo.sh "$TMPDIR/opentree-demo"
#   (cd "$TMPDIR/opentree-demo" && vhs /path/to/docs/demo/demo.tape)
#   cp "$TMPDIR/opentree-demo/demo.gif" docs/demo.gif
# Or just:  make demo
#
# Scenes: populated dashboard + agent-liveness badges -> live agent output ->
# colorized diff -> auto-generated PR -> full keymap -> quit.

Output docs/demo.gif
Require opentree

Set Shell "bash"
Set FontSize 15
Set Width 1320
Set Height 820
Set Padding 22
Set Margin 28
Set MarginFill "#0d0f14"
Set BorderRadius 9
Set WindowBar Colorful
Set Theme "Catppuccin Mocha"
Set TypingSpeed 90ms
Set PlaybackSpeed 1.0

# enter the seeded demo repo (hidden setup — see seed-demo.sh)
Hide
Type 'cd "$TMPDIR/opentree-demo"'
Enter
Type "clear"
Enter
Show

# 1. Launch — hold on the full populated dashboard (logo + all four badges + status bar)
Type "opentree"
Enter
Sleep 4s

# 2. Move onto the actively-working row — the Agent Output pane fills with live output
Down
Sleep 3s

# 3. Open the colorized diff, scroll through Committed / Uncommitted sections
Type "d"
Sleep 1.5s
Down 4
Sleep 1.5s
Down 4
Sleep 1s
Escape
Sleep 800ms

# 4. Auto-generate a PR from the commits — the pre-filled title/body appears
Type "p"
Sleep 3.5s
Escape
Sleep 800ms

# 5. Drop onto the "waiting · your turn" row (loud amber badge + its own live output)
Down 2
Sleep 2.5s

# 6. Reveal the full keymap, then collapse it
Type "?"
Sleep 2.5s
Type "?"
Sleep 600ms

# 7. Quit
Type "q"
Sleep 1s
