package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

var setupCheck bool

var SetupCmd = &cobra.Command{
	Use:               "setup <branch-name>",
	Short:             "Prepare a workspace's worktree: re-seed its config, then run the setup commands",
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: workspaceCompletions,
	Long: `Prepare a workspace's worktree the way opening its chat would.

Seeds first, then runs the [workspace] setup commands, here in this terminal
where you can watch them. The chat's own setup phase does the same work and
writes the same marker, so a worktree prepared here is one the chat will not
prepare again.

Use it to repair a worktree whose install went wrong, or to run a setup you
skipped, without restarting a chat and tearing down a live conversation.

  opentree setup <branch>           re-seed, then run the setup commands
  opentree setup <branch> --check   report what is seeded and what is not, and run nothing`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer stop()
		return runSetup(ctx, args[0])
	},
}

func init() {
	SetupCmd.Flags().BoolVar(&setupCheck, "check", false,
		"report the worktree's seeded files and setup state, and change nothing")
}

func runSetup(ctx context.Context, name string) error {
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return err
	}
	// The repository's config, not the worktree's: what opentree runs is the
	// project's, and a branch's checked-out opentree.toml may say anything.
	cfg, err := config.Load(filepath.Join(repoRoot, "opentree.toml"))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	store, err := state.New(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to open state: %w", err)
	}
	ws, err := store.GetWorkspace(name)
	if err != nil {
		return err
	}
	worktreePath := workspaceWorktree(repoRoot, cfg, ws)

	if setupCheck {
		return checkSetup(repoRoot, worktreePath, cfg, ws)
	}

	if err := bootstrap.ValidateSeed(repoRoot, cfg.Workspace.Seed); err != nil {
		return err
	}
	seeded, err := bootstrap.Seed(repoRoot, worktreePath, cfg.Workspace.Seed)
	if err != nil {
		return err
	}
	for _, path := range seeded {
		fmt.Printf("✓ Seeded %s\n", path)
	}

	commands, run := cfg.Workspace.Setup, cfg.Workspace.Run
	if len(commands) == 0 {
		fmt.Println("No [workspace] setup commands to run.")
		return nil
	}
	// Nothing here can ask. The chat is where the question is put, because that
	// is where somebody is looking; a command run from a script or a CI job has
	// to be answered ahead of time.
	if !bootstrap.Trusted(repoRoot, commands, run) {
		return fmt.Errorf("this repository's setup commands are not approved on this machine — run `opentree trust` to see and approve them")
	}

	if err := bootstrap.RunSetup(ctx, worktreePath, commands, func(line string) {
		fmt.Println(line)
	}); err != nil {
		return err
	}

	ws.SetupAt, ws.SetupHash = time.Now(), bootstrap.Hash(commands, run)
	if err := store.UpdateWorkspace(ws); err != nil {
		return fmt.Errorf("setup finished, but recording it failed: %w", err)
	}
	fmt.Printf("✓ %s is set up\n", name)
	return nil
}

// checkSetup reports rather than repairs: what the seed list looks like on
// disk, and whether the setup commands have run in their current form.
func checkSetup(repoRoot, worktreePath string, cfg *config.Config, ws *state.Workspace) error {
	if len(cfg.Workspace.Seed) == 0 && len(cfg.Workspace.Setup) == 0 {
		fmt.Println("[workspace] configures no seed list and no setup commands.")
		return nil
	}

	for _, r := range bootstrap.CheckSeed(repoRoot, worktreePath, cfg.Workspace.Seed) {
		text := r.Path
		if r.Detail != "" {
			text += "  — " + r.Detail
		}
		checkLine(string(r.State), text)
	}

	if len(cfg.Workspace.Setup) == 0 {
		return nil
	}
	commands, run := cfg.Workspace.Setup, cfg.Workspace.Run
	switch hash := bootstrap.Hash(commands, run); {
	case ws.SetupAt.IsZero():
		checkLine("setup", "has not run in this worktree")
	case ws.SetupHash != hash:
		checkLine("setup", fmt.Sprintf("ran %s, before the commands were edited — the next chat runs them again",
			ws.SetupAt.Format("2006-01-02 15:04")))
	default:
		checkLine("setup", "ran "+ws.SetupAt.Format("2006-01-02 15:04"))
	}
	if !bootstrap.Trusted(repoRoot, commands, run) {
		checkLine("trust", "not approved on this machine — `opentree trust`")
	}
	return nil
}

// checkLine keeps --check in two columns, so the states line up and the report
// can be read down its left edge.
func checkLine(state, text string) {
	fmt.Printf("%-10s %s\n", state, text)
}

// workspaceWorktree is where a workspace's worktree is: what state recorded,
// falling back to where the config says it would have been put. A workspace
// created before opentree recorded the path still has one.
func workspaceWorktree(repoRoot string, cfg *config.Config, ws *state.Workspace) string {
	if ws.WorktreeDir != "" {
		return ws.WorktreeDir
	}
	return filepath.Join(repoRoot, cfg.Worktree.BaseDir, gitutil.SanitizeBranchName(ws.Name))
}
