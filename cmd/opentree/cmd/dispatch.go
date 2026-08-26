package cmd

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/workspace"
)

// ExitCodeError is an error that names its exit code, for callers that script
// against this process — dispatch's whole audience. main checks for it before
// the generic exit(1).
type ExitCodeError struct {
	Code int
	Msg  string
}

func (e ExitCodeError) Error() string { return e.Msg }

var (
	dispatchHeadless bool
	dispatchTimeout  time.Duration
)

var DispatchCmd = &cobra.Command{
	Use:          "dispatch <issue-number | prompt...>",
	Short:        "Create a workspace, hand the agent a task, and let autopilot drive it to a PR",
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	Long: `Dispatch is the whole pipeline in one command: create a workspace — from a
GitHub issue number, or from the prompt you type — start its agent, switch
autopilot on, and send the task. The agent works, the check command judges,
failures go back, and a green check publishes the PR.

By default it then attaches, so you can watch. With --headless it waits
instead, unattended, and exits with a code a script can branch on:

  0  the PR was published; its URL is printed
  1  autopilot halted (the check kept failing) or reported an error
  2  the agent stopped, or the chat became unreachable
  3  the agent is blocked on a permission only a human can answer
  4  --timeout elapsed; the workspace is still working

The workspace is left alive on every failure — attach to finish by hand.
Headless can ask nothing, so the repository's setup and check commands must be
approved first: run 'opentree trust' once on this machine. A tmux server must
be running (tmux new-session -d starts one).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		baseBranch, _ := cmd.Flags().GetString("base")
		return runDispatch(args, baseBranch)
	},
}

func init() {
	DispatchCmd.Flags().StringP("base", "b", "", "Base branch to create the worktree from (default: config default)")
	DispatchCmd.Flags().BoolVar(&dispatchHeadless, "headless", false,
		"wait for the PR instead of attaching, and exit with a scriptable code")
	DispatchCmd.Flags().DurationVar(&dispatchTimeout, "timeout", 30*time.Minute,
		"how long --headless waits before giving up (the workspace keeps working)")
}

func runDispatch(args []string, baseBranch string) error {
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return err
	}
	// The repository's config, not the walk's: the same choice trust itself
	// makes, because these are the commands being pre-flighted.
	cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Nothing here can ask, in either mode: dispatch walks away from the
	// window, and an unattended chat parked on an approval panel forever is
	// exactly the failure this refusal prevents.
	ws := cfg.Workspace
	if bootstrap.Executable(ws.Setup, ws.Run, ws.Check) &&
		!bootstrap.Trusted(repoRoot, ws.Setup, ws.Run, ws.Check) {
		return fmt.Errorf("this repository's setup/check commands are not approved on this machine — run `opentree trust` first")
	}

	svc, err := workspace.New(repoRoot, cfg)
	if err != nil {
		return err
	}
	store, err := state.New(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to open state: %w", err)
	}

	target, prompt, err := dispatchTarget(svc, args, baseBranch)
	if err != nil {
		return err
	}

	fmt.Printf("✓ Created workspace '%s'\n", target.Name)
	fmt.Printf("✓ Launched %s in tmux window\n", target.Agent)

	// The window just launched `opentree chat`; its socket is up before the
	// setup phase runs, so this wait is ordinarily instant.
	sock := chat.SocketPath(repoRoot, target.Name)
	if err := waitForChat(sock, target.Name, time.Minute); err != nil {
		return fmt.Errorf("the workspace's chat never answered: %w", err)
	}

	// Autopilot on: the chat persists the flag itself, and the state write
	// underneath is for the crash window between the two.
	if err := chat.Send(sock, target.Name, chat.Command{Type: chat.CommandAutopilot, Text: "on"}); err != nil {
		return fmt.Errorf("could not switch autopilot on: %w", err)
	}
	_ = store.Update(target.Name, func(w *state.Workspace) error {
		w.Autopilot = true
		return nil
	})

	// Queued behind setup automatically: a prompt sent before the session
	// exists waits and fires the moment the agent is ready.
	if err := chat.Send(sock, target.Name, chat.Command{Type: chat.CommandPrompt, Text: prompt}); err != nil {
		return fmt.Errorf("could not send the task: %w", err)
	}
	fmt.Printf("✓ Task sent, autopilot on\n")

	if !dispatchHeadless {
		return svc.Process().AttachWindow(target.Name)
	}
	fmt.Printf("Waiting headless (timeout %s) — the workspace keeps working if this exits early.\n", dispatchTimeout)
	return waitHeadless(sock, target.Name, dispatchTimeout, 5*time.Second)
}

// dispatchTarget creates the workspace the arguments describe and composes the
// task: an all-digits single argument is an issue, anything else is the prompt
// itself.
func dispatchTarget(svc *workspace.Service, args []string, baseBranch string) (*state.Workspace, string, error) {
	if n, ok := issueArg(args); ok {
		target, err := svc.CreateFromIssue(n, baseBranch)
		if err != nil {
			return nil, "", err
		}
		return target, issuePrompt(n, target.IssueTitle), nil
	}

	prompt := strings.Join(args, " ")
	name := dispatchBranchName(prompt, func(candidate string) bool {
		for _, existing := range svc.ListWorkspaces() {
			if existing.Name == candidate {
				return true
			}
		}
		return false
	})
	target, err := svc.Create(name, baseBranch)
	if err != nil {
		return nil, "", err
	}
	return target, prompt, nil
}

// issueArg is whether the arguments are one issue number.
func issueArg(args []string) (int, bool) {
	if len(args) != 1 {
		return 0, false
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// issuePrompt is the task as the agent receives it in issue mode. The body is
// fetched fresh — CreateFromIssue keeps only number and title — and skipped
// without complaint when it cannot be: the number and title are enough to
// start, and the agent can read the rest with gh itself.
func issuePrompt(number int, title string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Implement GitHub issue #%d: %s\n", number, title)
	if issue, err := github.New().GetIssue(number); err == nil && issue.Body != "" {
		sb.WriteString("\n" + issue.Body + "\n")
	}
	sb.WriteString("\nWhen you believe the work is complete, end your turn.")
	return sb.String()
}

var dispatchSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// dispatchBranchName derives a branch from the prompt, the way IssueBranchName
// derives one from a title: auto-<slug>, suffixed past collisions so two
// dispatches of similar prompts get workspaces of their own.
func dispatchBranchName(prompt string, taken func(string) bool) string {
	slug := strings.Trim(dispatchSlugRe.ReplaceAllString(strings.ToLower(prompt), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "task"
	}
	name := "auto-" + slug
	for i := 2; taken(name); i++ {
		name = fmt.Sprintf("auto-%s-%d", slug, i)
	}
	return name
}

// waitForChat polls until the workspace's chat answers on its socket.
func waitForChat(sock, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, ok := chat.Query(sock, name); ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no answer on %s after %s", sock, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitHeadless watches the chat until the run reaches an outcome, and turns
// that outcome into the exit contract the help documents. The workspace is
// never torn down here: every non-zero exit describes a workspace still worth
// attaching to.
func waitHeadless(sock, name string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	unreachableSince := time.Time{}

	for {
		st, ok := chat.Query(sock, name)
		now := time.Now()

		switch {
		case !ok:
			// A silent socket is a closed window or a dead process — but give
			// it a grace period, because a chat being restarted is silent for
			// a moment too.
			if unreachableSince.IsZero() {
				unreachableSince = now
			} else if now.Sub(unreachableSince) > 30*time.Second {
				return ExitCodeError{Code: 2, Msg: fmt.Sprintf(
					"%s's chat became unreachable — attach with `opentree attach %s`", name, name)}
			}

		default:
			unreachableSince = time.Time{}

			if st.Autopilot != nil && st.Autopilot.Outcome == "published" {
				fmt.Printf("✓ PR: %s\n", st.Autopilot.PRURL)
				return nil
			}
			if st.Autopilot != nil && st.Autopilot.Phase == "halted" {
				return ExitCodeError{Code: 1, Msg: fmt.Sprintf(
					"autopilot halted: the check is still failing — attach with `opentree attach %s`", name)}
			}
			if st.Error != "" {
				return ExitCodeError{Code: 1, Msg: st.Error}
			}
			if st.State == chat.StateStopped {
				return ExitCodeError{Code: 2, Msg: fmt.Sprintf(
					"the agent stopped — attach with `opentree attach %s` to restart it", name)}
			}
			// A permission is the one thing headless can never answer. A
			// minute's grace covers a human answering from the dashboard.
			if st.State == chat.StateAwaiting && !st.Since.IsZero() && now.Sub(st.Since) > time.Minute {
				title := ""
				if st.Permission != nil {
					title = ": " + st.Permission.Title
				}
				return ExitCodeError{Code: 3, Msg: fmt.Sprintf(
					"blocked on a permission%s — headless cannot answer; `opentree attach %s` to continue", title, name)}
			}
		}

		if now.After(deadline) {
			return ExitCodeError{Code: 4, Msg: fmt.Sprintf(
				"timed out after %s; the workspace is still running — `opentree attach %s`", timeout, name)}
		}
		time.Sleep(interval)
	}
}
