# opentree — 60s product demo (hero cut)
#
# Run from the repository root, which is where Output lands docs/demo.gif:
#   bash docs/demo/seed-demo.sh
#   vhs docs/demo/demo.tape
# Or just:  make demo
#
# The recorded shell cd's into the seeded repo itself (hidden, below); vhs
# writes its Output relative to its own working directory, not that one.
#
# Scenes: populated dashboard + agent-liveness badges -> the chat view, driven
# by the scripted ACP agent (fake-acp-agent.py): streaming reply, tool calls,
# an inline diff, a real permission prompt -> the agent's own commands and
# session mode -> back to the list -> the skills every agent will load.
#
# There is no `Require opentree` here on purpose: seed-demo.sh builds this
# checkout's own binary into the demo's .demo-bin and the hidden setup below
# puts it first on PATH, so the recording can never show a stale install.

Output docs/demo.gif

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
Set TypingSpeed 60ms
Set Framerate 24
Set PlaybackSpeed 1.0

# enter the seeded demo repo (hidden setup — see seed-demo.sh)
Hide
Type 'cd "$TMPDIR/opentree-demo" && export PATH="$PWD/.demo-bin:$PATH"'
Enter
Type "clear"
Enter
Show

# 1. Launch — hold on the full populated dashboard: logo, the three tabs, five
#    workspaces, every liveness badge, the changed-files pane.
Type "opentree"
Enter
Sleep 6s

# 2. Down onto the actively-working row, then into its chat
Down
Sleep 2s
Enter
Sleep 4s

# 3. Ask the agent something — the scripted turn answers it the same way every take
Type "why is the liveness badge stuck on working?"
Sleep 700ms
Enter

# 4. The turn: reasoning, a read, the fix as an inline diff, then the test it
#    wants to run. The permission prompt lands ~7.5s in and blocks until answered.
Sleep 9s

# 5. Approve it — [a] allow once. The test runs, the turn closes, and the
#    header picks up the token cost the agent reported.
Sleep 3s
Type "a"
Sleep 6s

# 6. The agent's own controls: the palette completes the command, running it
#    opens the picker, and the header follows the model that was chosen.
#    (Two Enters on purpose: the first completes /model, the second runs it.)
Type "/model"
Sleep 2s
Enter
Sleep 400ms
Enter
Sleep 2s
Down
Sleep 900ms
Enter
Sleep 3s

# 7. Back to the workspace list, and on to the skills every agent will load
Ctrl+C
Sleep 3s
Tab
Sleep 7s
