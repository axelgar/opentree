package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var NewCmd = &cobra.Command{
	Use:   "new <branch-name>",
	Short: "Create a new workspace",
	Long: `Create a new workspace: a git worktree on its own branch, with the agent
launched in a tmux window.

With --agents, the same name fans out instead: one sibling workspace per
agent (feat/x-claude, feat/x-opencode, ...), all from the same base and
grouped in the dashboard, so one task can be raced across agents and the
winner promoted. --prompt (or a piped stdin) hands every sibling the same
task; without it the siblings start idle, waiting to be messaged.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		if err := gitutil.ValidateBranchName(branchName); err != nil {
			return err
		}
		fromRemote, _ := cmd.Flags().GetBool("remote")
		agentOverride, _ := cmd.Flags().GetString("agent")
		agentsFlag, _ := cmd.Flags().GetStringSlice("agents")
		promptFlag, _ := cmd.Flags().GetString("prompt")

		if err := newFlagConflict(fromRemote, agentOverride, agentsFlag, promptFlag); err != nil {
			return err
		}

		// Load config
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}

		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return err
		}

		if fromRemote {
			if base, _ := cmd.Flags().GetString("base"); base != "" {
				return fmt.Errorf("--base cannot be combined with --remote: the remote branch's own history determines its base")
			}
			ws, err := svc.CreateFromRemoteBranch(branchName)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Checked out remote branch '%s' into new workspace\n", ws.Name)
			fmt.Printf("✓ Launched %s in tmux window\n", ws.Agent)
			fmt.Printf("\nTo attach: opentree attach %s\n", ws.Name)
			return nil
		}

		baseBranch, _ := cmd.Flags().GetString("base")
		if baseBranch == "" {
			baseBranch = cfg.Worktree.DefaultBase
		}

		if len(agentsFlag) > 0 {
			return runFanout(svc, repoRoot, branchName, baseBranch, agentsFlag, promptFlag)
		}

		ws, err := svc.CreateWith(branchName, baseBranch, workspace.CreateOpts{Agent: agentOverride})
		if err != nil {
			return err
		}

		fmt.Printf("✓ Created workspace '%s' based on '%s'\n", ws.Name, ws.BaseBranch)
		fmt.Printf("✓ Launched %s in tmux window\n", ws.Agent)
		fmt.Printf("\nTo attach: opentree attach %s\n", ws.Name)
		return nil
	},
}

// newFlagConflict rejects flag combinations whose meaning would have to be
// guessed. Checked before any work so a refused command costs nothing.
func newFlagConflict(fromRemote bool, agent string, agents []string, prompt string) error {
	switch {
	case len(agents) > 0 && agent != "":
		return fmt.Errorf("--agent and --agents cannot be combined: --agents already names one agent per sibling")
	case len(agents) > 0 && fromRemote:
		return fmt.Errorf("--agents cannot be combined with --remote: a fan-out creates fresh sibling branches, not checkouts of an existing one")
	case agent != "" && fromRemote:
		return fmt.Errorf("--agent cannot be combined with --remote: a remote checkout runs the configured agent — switch it with 'opentree agents use'")
	case prompt != "" && len(agents) == 0:
		return fmt.Errorf("--prompt only makes sense with --agents: to hand one workspace a task and walk away, use 'opentree dispatch'")
	}
	return nil
}

// runFanout creates the siblings and, when there is a task, hands it to each.
//
// The prompt is read before anything is created — a bad pipe should cost
// nothing — and its delivery is best-effort after: the command's contract is
// "workspaces exist, agents launched", and a prompt that could not be sent
// leaves a workspace the user can attach to and paste into, unlike dispatch,
// where the prompt is the job and its failure fails the command.
func runFanout(svc *workspace.Service, repoRoot, base, baseBranch string, agents []string, prompt string) error {
	if prompt == "" {
		stdin, err := stdinPrompt()
		if err != nil {
			return err
		}
		prompt = stdin
	}

	created, err := svc.CreateFanout(base, baseBranch, agents)
	for _, ws := range created {
		fmt.Printf("✓ Created workspace '%s' (%s) based on '%s'\n", ws.Name, ws.Agent, ws.BaseBranch)
	}
	if err != nil {
		return err
	}

	if prompt != "" {
		names := make([]string, len(created))
		for i, ws := range created {
			names[i] = ws.Name
		}
		sockFor := func(name string) string { return chat.SocketPath(repoRoot, name) }
		for _, sendErr := range sendFanoutPrompts(sockFor, names, prompt, time.Minute) {
			fmt.Printf("⚠ %v\n", sendErr)
		}
		fmt.Printf("✓ Task sent to %d workspace(s)\n", len(created))
	}

	fmt.Printf("\n%d agents racing '%s' — the dashboard shows them as one group.\n", len(created), base)
	fmt.Printf("To attach: opentree attach %s\n", created[0].Name)
	return nil
}

// sendFanoutPrompts hands every sibling the same task over its chat socket.
// The windows were all launched during creation, so the sockets came up in
// parallel and the sequential waits are ordinarily instant rather than
// summing; a prompt that arrives before the agent is ready is queued by the
// chat and fires when it is. One sibling failing must not stop the rest —
// each failure comes back as its own error, phrased for the user.
func sendFanoutPrompts(sockFor func(string) string, names []string, prompt string, timeout time.Duration) []error {
	var errs []error
	for _, name := range names {
		sock := sockFor(name)
		if err := waitForChat(sock, name, timeout); err != nil {
			errs = append(errs, fmt.Errorf("could not send the task to '%s' — attach and paste it: %w", name, err))
			continue
		}
		if err := chat.Send(sock, name, chat.Command{Type: chat.CommandPrompt, Text: prompt}); err != nil {
			errs = append(errs, fmt.Errorf("could not send the task to '%s' — attach and paste it: %w", name, err))
		}
	}
	return errs
}

// stdinPrompt is the task read from a pipe, and nothing when stdin is the
// terminal — `opentree new` at a prompt must not hang waiting for input that
// is not coming.
func stdinPrompt() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("checking stdin for a task: %w", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading the task from stdin: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func init() {
	NewCmd.Flags().StringP("base", "b", "", "Base branch to create worktree from (default: config default)")
	NewCmd.Flags().BoolP("remote", "r", false, "Check out an existing remote branch instead of creating a new one")
	NewCmd.Flags().String("agent", "", "Agent to run in this workspace instead of the configured one")
	NewCmd.Flags().StringSlice("agents", nil, "Fan out: one sibling workspace per agent, racing the same task")
	NewCmd.Flags().String("prompt", "", "Task to send every fan-out sibling (default: piped stdin, if any)")
	_ = NewCmd.RegisterFlagCompletionFunc("agent", agentCommandCompletions)
	_ = NewCmd.RegisterFlagCompletionFunc("agents", agentListCompletions)
}

// agentCommandCompletions offers the registry's agent commands, described.
// Commands rather than display names: a flag value with a space in it has to
// be quoted, and "claude" is what FindAgent resolves either way.
func agentCommandCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var completions []string
	for _, a := range config.PredefinedAgents {
		completions = append(completions, fmt.Sprintf("%s\t%s", a.Command, a.Description))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// agentListCompletions completes the agent after the last comma, so
// --agents claude,gem<tab> keeps what is already typed.
func agentListCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prefix := ""
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix = toComplete[:i+1]
	}
	var completions []string
	for _, a := range config.PredefinedAgents {
		completions = append(completions, fmt.Sprintf("%s%s\t%s", prefix, a.Command, a.Description))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
