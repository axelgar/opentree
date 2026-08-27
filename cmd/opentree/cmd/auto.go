package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var AutoCmd = &cobra.Command{
	Use:               "auto <branch-name> [on|off]",
	Short:             "Switch a workspace's autopilot on or off, or report where it stands",
	Args:              cobra.RangeArgs(1, 2),
	ValidArgsFunction: workspaceCompletions,
	Long: `Autopilot drives a workspace toward a green PR: after each agent turn the
[workspace] check command runs, failures go back to the agent as the next
prompt, and a pass pushes the branch and creates or updates the PR.

With no on/off this reports where the loop stands — the persisted toggle, and
what the live chat says it is doing. The toggle is per workspace and survives
the chat window; the dashboard's P key writes the same flag.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}
		store, err := state.New(repoRoot)
		if err != nil {
			return fmt.Errorf("failed to open state: %w", err)
		}
		ws, err := store.GetWorkspace(name)
		if err != nil {
			return fmt.Errorf("failed to find workspace %q: %w", name, err)
		}

		if len(args) == 1 {
			return reportAutopilot(repoRoot, ws)
		}

		var on bool
		switch args[1] {
		case "on":
			on = true
		case "off":
			on = false
		default:
			return fmt.Errorf("autopilot takes \"on\" or \"off\", not %q", args[1])
		}

		// State first: the flag is what the next chat window reads, so it must
		// land whether or not a chat is running now.
		if err := store.Update(name, func(w *state.Workspace) error {
			w.Autopilot = on
			return nil
		}); err != nil {
			return fmt.Errorf("failed to record the toggle: %w", err)
		}

		if on {
			warnIfNoCheck()
		}

		// The live chat is told directly so the answer takes effect this turn
		// rather than at the next window. Best-effort: no chat running, or a
		// window from before the command existed, still honours the flag when
		// it restarts.
		word := "off"
		if on {
			word = "on"
		}
		err = chat.Send(chat.SocketPath(repoRoot, name), name, chat.Command{
			Type: chat.CommandAutopilot, Text: word,
		})
		if err != nil {
			fmt.Printf("✓ Autopilot %s for %q — takes effect in the chat window (reopen it if it predates this release)\n", word, name)
			return nil
		}
		fmt.Printf("✓ Autopilot %s for %q\n", word, name)
		return nil
	},
}

// reportAutopilot is the bare `opentree auto <branch>`: the persisted flag,
// and whatever the live chat adds to it.
func reportAutopilot(repoRoot string, ws *state.Workspace) error {
	persisted := "off"
	if ws.Autopilot {
		persisted = "on"
	}
	fmt.Printf("%s: autopilot %s", ws.Name, persisted)

	st, ok := chat.Query(chat.SocketPath(repoRoot, ws.Name), ws.Name)
	switch {
	case !ok:
		fmt.Println(" (no chat running)")
	case st.Autopilot == nil:
		fmt.Println(" (the running chat predates autopilot — reopen its window)")
	default:
		fmt.Printf(" · %s", st.Autopilot.Phase)
		if st.Autopilot.PRURL != "" {
			fmt.Printf(" · %s", st.Autopilot.PRURL)
		}
		fmt.Println()
	}
	return nil
}

// warnIfNoCheck says what an autopilot without a check command does, once, at
// the moment the loop is switched on. A warning rather than a refusal:
// publish-without-checking is a legitimate loop for a project whose CI is the
// check.
func warnIfNoCheck() {
	cfg, err := config.Load("")
	if err != nil || cfg.Workspace.Check != "" {
		return
	}
	fmt.Println("Note: no [workspace] check command is configured — autopilot will publish after each turn without checking.")
	fmt.Printf("Set one with: opentree config set workspace.check %q\n", "make test")
}
