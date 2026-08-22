package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
)

var TrustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Approve this repository's setup and run commands",
	Long: `Approve the [workspace] setup and run commands in opentree.toml.

opentree.toml is tracked in git, so those commands arrive with a clone, from
whoever last had commit rights. They run only once this machine has approved
them, and approval covers their exact text — edit one and opentree asks again.

Normally the chat asks the first time it would run setup. This is the way to
answer that question ahead of time, or from a script where nothing can be
asked.

  opentree trust          approve what opentree.toml now says
  opentree trust show     print those commands and whether they are approved
  opentree trust revoke   drop this repository's approvals`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, ws, err := workspaceConfig()
		if err != nil {
			return err
		}
		if err := bootstrap.Approve(repoRoot, ws.Setup, ws.Run); err != nil {
			return err
		}
		fmt.Printf("✓ Approved for %s:\n", repoRoot)
		printCommands(ws)
		return nil
	},
}

var trustShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print this repository's setup and run commands, and whether they are approved",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, ws, err := workspaceConfig()
		if err != nil {
			return err
		}
		if !bootstrap.Executable(ws.Setup, ws.Run) {
			fmt.Println("[workspace] names no setup or run command — nothing to approve.")
			return nil
		}

		fmt.Printf("%s asks to run:\n", workspaceConfigPath())
		printCommands(ws)
		fmt.Println()

		if bootstrap.Trusted(repoRoot, ws.Setup, ws.Run) {
			for _, a := range bootstrap.Approvals(repoRoot) {
				if a.Hash == bootstrap.Hash(ws.Setup, ws.Run) {
					fmt.Printf("Approved on this machine %s.\n", a.ApprovedAt.Format("2006-01-02 15:04"))
					break
				}
			}
			return nil
		}
		fmt.Println("Not approved on this machine. Run `opentree trust` to approve it.")
		return nil
	},
}

var trustRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Drop this repository's approvals",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}
		revoked, err := bootstrap.Revoke(repoRoot)
		if err != nil {
			return err
		}
		if !revoked {
			fmt.Printf("Nothing approved for %s.\n", repoRoot)
			return nil
		}
		fmt.Printf("✓ Revoked every approval for %s\n", repoRoot)
		return nil
	},
}

// workspaceConfig is the repository and the [workspace] block every trust
// subcommand starts from.
func workspaceConfig() (string, config.WorkspaceConfig, error) {
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return "", config.WorkspaceConfig{}, err
	}
	// The repository's config, not whatever the walk finds — the same choice
	// chat.go and setup.go make, and for a sharper reason here. The approval
	// is keyed on repoRoot, so approving a worktree's copy of the file records
	// the branch's commands under the main repository's key: `opentree setup`
	// would then read the repository's file, hash different text, and refuse
	// again with the same "run `opentree trust`" that had just said it was
	// approved. There was no way out of that loop from inside a worktree.
	cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
	if err != nil {
		return "", config.WorkspaceConfig{}, fmt.Errorf("failed to load config: %w", err)
	}
	return repoRoot, cfg.Workspace, nil
}

// workspaceConfigPath is the file workspaceConfig read, for printing.
func workspaceConfigPath() string {
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return config.FindConfigFile()
	}
	return filepath.Join(repoRoot, "opentree.toml")
}

// printCommands shows the block the way it will run: setup in order, then run.
func printCommands(ws config.WorkspaceConfig) {
	for _, c := range ws.Setup {
		fmt.Printf("  setup  %s\n", c)
	}
	if ws.Run != "" {
		fmt.Printf("  run    %s\n", ws.Run)
	}
}

func init() {
	TrustCmd.AddCommand(trustShowCmd)
	TrustCmd.AddCommand(trustRevokeCmd)
}
