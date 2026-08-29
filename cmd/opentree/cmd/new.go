package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/workspace"
)

var NewCmd = &cobra.Command{
	Use:   "new <branch-name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := args[0]
		if err := gitutil.ValidateBranchName(branchName); err != nil {
			return err
		}
		fromRemote, _ := cmd.Flags().GetBool("remote")
		agentOverride, _ := cmd.Flags().GetString("agent")

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
			if agentOverride != "" {
				return fmt.Errorf("--agent cannot be combined with --remote: a remote checkout runs the configured agent — switch it with 'opentree agents use'")
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

func init() {
	NewCmd.Flags().StringP("base", "b", "", "Base branch to create worktree from (default: config default)")
	NewCmd.Flags().BoolP("remote", "r", false, "Check out an existing remote branch instead of creating a new one")
	NewCmd.Flags().String("agent", "", "Agent to run in this workspace instead of the configured one")
	_ = NewCmd.RegisterFlagCompletionFunc("agent", agentCommandCompletions)
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
