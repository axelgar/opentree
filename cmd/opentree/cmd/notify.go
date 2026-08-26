package cmd

import (
	"fmt"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/notify"
)

var NotifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "Notifications for agents that need you",
	Long: `Each chat says something when it starts needing a human: a tmux bell into its
own window, and a desktop banner for when the terminal is closed.

Configure it in the global config only (~/.config/opentree/opentree.toml):

  [notify]
  on      = ["blocked", "stopped"]   # add "done"; [] switches everything off
  desktop = true                     # false: tmux bell only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// sampleGap separates the three test notifications. Three bells inside a
// millisecond are one bell, and three banners are a stack you cannot read.
const sampleGap = 900 * time.Millisecond

var notifyTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send one of each notification",
	Long: `Sends a blocked, a done and a stopped notification through the surfaces this
machine is configured for, and says whether tmux currently considers this pane
watched — a chat in the window you are already reading stays quiet on purpose,
which is the usual answer to "it was working, and then nothing arrived".

Worth running once. macOS silently drops notifications sent by osascript until
they have been allowed, which is otherwise a feature with no symptom and no
error — the banners simply never arrive.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// The global config, not the merged one: [notify] is read from there
		// and nowhere else, and a test that consulted a repository would be
		// testing something the chat never does.
		cfg, err := config.LoadGlobal()
		if err != nil {
			return fmt.Errorf("failed to load global config: %w", err)
		}

		var senders notify.Senders
		if pane := notify.Pane(); pane != "" {
			senders = append(senders, notify.Bell{})
			fmt.Printf("tmux bell     → pane %s\n", pane)
			// The same question a live chat asks before every notification,
			// asked once and reported rather than obeyed: this command sends
			// whatever the answer is, because a delivery test that suppressed
			// itself would be indistinguishable from a broken one. It is here
			// because suppression is invisible from the outside — a chat that
			// notifies perfectly well and a chat you happen to be watching
			// look exactly the same from the window that stayed silent.
			if notify.Watched(pane) {
				fmt.Println("watched       → yes, this window is the current one and a client is attached, so a real chat here would stay quiet")
			} else {
				fmt.Println("watched       → no, so a real chat here would notify")
			}
		} else {
			fmt.Println("tmux bell     → not running inside tmux, so nothing rings")
		}
		desktop := cfg.Notify.Desktop == nil || *cfg.Notify.Desktop
		if desktop {
			senders = append(senders, notify.Desktop{})
			fmt.Printf("desktop       → %s\n", desktopTool())
		} else {
			fmt.Println("desktop       → off (notify.desktop = false)")
		}
		if len(senders) == 0 {
			return fmt.Errorf("no surface to send to — run this inside tmux, or set notify.desktop = true")
		}
		fmt.Println()

		on := map[string]bool{}
		for _, name := range cfg.Notify.On {
			on[name] = true
		}

		// Every event, including the ones switched off: this is a test of
		// delivery, and an event you cannot see is exactly the one worth
		// seeing once before deciding whether to switch it on.
		for i, kind := range notify.Kinds {
			if i > 0 {
				time.Sleep(sampleGap)
			}
			ev := sampleEvent(kind)
			senders.Send(ev)

			_, body := ev.Text()
			state := ""
			if !on[string(kind)] {
				state = fmt.Sprintf(`  (off — add %q to notify.on)`, string(kind))
			}
			fmt.Printf("  %-8s %s%s\n", kind, body, state)
		}

		if runtime.GOOS == "darwin" && desktop {
			fmt.Println("\nNo banner? macOS drops notifications from osascript until they are allowed:")
			fmt.Println("  System Settings → Notifications → Script Editor → Allow notifications")
		}
		return nil
	},
}

// sampleEvent is what each kind looks like when it is real.
func sampleEvent(kind notify.Kind) notify.Event {
	ev := notify.Event{Kind: kind, Workspace: "notify test"}
	switch kind {
	case notify.Blocked:
		ev.Detail = "go test ./..."
	case notify.Done:
		ev.Elapsed = 4 * time.Minute
	case notify.PRReady:
		ev.Detail = "https://github.com/acme/repo/pull/12"
	}
	return ev
}

// desktopTool names the program the banner goes through, so a machine that has
// not got it says so here rather than by staying quiet.
func desktopTool() string {
	switch runtime.GOOS {
	case "darwin":
		return "osascript"
	case "linux":
		return "notify-send"
	default:
		return runtime.GOOS + " has no desktop surface — opentree sends banners on macOS and Linux"
	}
}

func init() {
	NotifyCmd.AddCommand(notifyTestCmd)
}
