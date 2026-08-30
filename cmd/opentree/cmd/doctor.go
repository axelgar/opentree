package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/diag"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/registry"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
)

// DoctorCmd is the answer to "what does your machine look like".
//
// opentree drives tmux, git, gh, an agent subprocess, MCP servers under that,
// and npm, and until this existed the only thing a user could send back about
// any of it was a sentence. Several bugs were individually undiagnosable for
// exactly that reason: a config that would not parse, an adapter that was not
// where it was expected, a worktree whose config resolved somewhere surprising.
//
// It reads and reports. Nothing here changes anything, so it is safe to ask
// somebody to run it and paste the output.
var DoctorCmd = &cobra.Command{
	Use:          "doctor",
	Short:        "Report this machine's opentree setup, for a bug report",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	Long: `Print what opentree can see: its own version, the tools it drives, which
config file it resolved and why, whether this repository's setup commands are
approved, and where its state and sockets live.

Everything here is read. Nothing is changed, so it is safe to run and paste.

Paths are printed in full because which one was chosen is usually the answer.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		section := func(name string) { fmt.Fprintf(out, "\n%s\n", name) }
		line := func(label string, v any) { fmt.Fprintf(out, "  %-18s %v\n", label, v) }

		section("opentree")
		line("version", cmd.Root().Version)
		if exe, err := os.Executable(); err == nil {
			line("binary", exe)
		}
		if diag.Enabled() {
			line("log", diag.Path())
		} else {
			line("log", "off — set "+diag.EnvVar+"=<path> and reproduce to record one")
		}

		section("tools")
		line("git", toolVersion("git", "--version"))
		line("tmux", toolVersion("tmux", "-V"))
		line("gh", ghStatus())
		line("node", toolVersion("node", "--version"))

		// Above the repository check: the registry store is per machine, and
		// a report run outside any repo should still say what is installed.
		// Reads only — the promise on the tin covers this section too, so
		// nothing here asks the network what the index says today.
		section("registry")
		records, problems := registry.Installed()
		if len(records) == 0 && len(problems) == 0 {
			line("agents", "none installed — `opentree agents add <id>` installs from the ACP Registry")
		}
		for _, r := range records {
			line("  "+r.Entry.ID, describeRegistryInstall(r))
		}
		for _, p := range problems {
			line("problem", p)
		}

		// Everything below needs a repository. Saying so once beats eight
		// lines each explaining that they could not look.
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			section("repository")
			line("status", "not in a git repository — run this from inside one for the rest")
			return nil
		}

		section("repository")
		line("root", repoRoot)
		if wt, err := gitutil.WorktreeRoot(); err == nil && wt != repoRoot {
			// The distinction that produced a class of bugs of its own: a
			// worktree's checked-out opentree.toml belongs to a branch, and
			// what opentree runs is the project's.
			line("worktree", wt+"  (config and state come from the root above)")
		}

		section("config")
		line("repo file", describeFile(config.FindConfigFile()))
		line("global file", describeFile(config.GlobalConfigPath()))
		cfg, sources, cfgErr := config.LoadWithSources("")
		if rejected := sources.RejectedRepoBaseDir; rejected != "" {
			// Above the resolved values, because it explains one of them: the
			// base_dir printed below is the default, and the file the reader
			// is looking at plainly says otherwise.
			line("base_dir asked", rejected+" — IGNORED, it is outside this repository")
			line("", "yours to set: opentree config set --global worktree.base_dir "+rejected)
		}
		if cfgErr != nil {
			line("status", "WILL NOT PARSE — "+cfgErr.Error())
			line("effect", "every command is running on defaults")
		} else {
			line("agent", cfg.Agent.Command)
			line("base_dir", cfg.Worktree.BaseDir)
			line("default_base", cfg.Worktree.DefaultBase)
			line("run", orNone(cfg.Workspace.Run))
			line("setup", orNone(strings.Join(cfg.Workspace.Setup, " && ")))
		}

		if cfgErr == nil {
			section("agent")
			if agent := config.FindAgent(cfg.Agent.Command); agent == nil {
				line("registry", cfg.Agent.Command+" — not an agent opentree knows how to drive")
			} else {
				line("name", agent.Name)
				switch {
				case agent.Origin != nil:
					// A registry agent's command is a path under ~/.opentree,
					// not a name on PATH — describeTool would report a healthy
					// install as missing.
					line("command", describeFile(agent.ACP.Command))
					line("source", "registry "+agent.Origin.Version+"  ("+agent.Origin.Dir+")")
				default:
					line("command", describeTool(agent.Command))
				}
				if spec := agent.ACPPackageSpec(); spec != "" {
					line("adapter", spec)
					line("adapter binary", describeAdapter(*agent))
				}
			}

			section("trust")
			commands, run, check := cfg.Workspace.Setup, cfg.Workspace.Run, cfg.Workspace.Check
			switch {
			case !bootstrap.Executable(commands, run, check):
				line("status", "nothing to approve — [workspace] names no setup, run or check command")
			case bootstrap.Trusted(repoRoot, commands, run, check):
				line("status", "approved on this machine")
			default:
				line("status", "NOT approved — run `opentree trust` (setup will refuse until then)")
			}
			line("trust file", describeFile(bootstrap.TrustPath()))
		}

		section("state")
		line("file", describeFile(filepath.Join(repoRoot, ".opentree", "state.json")))
		if store, err := state.New(repoRoot); err != nil {
			line("status", "WILL NOT LOAD — "+err.Error())
		} else {
			workspaces := store.ListWorkspaces()
			line("workspaces", len(workspaces))
			for _, ws := range workspaces {
				line("  "+ws.Name, describeWorkspace(repoRoot, ws))
			}
		}

		section("tmux")
		if !tmux.Installed() {
			line("status", "not installed — every workspace runs in a tmux window, so nothing will start")
		} else {
			line("session", tmux.New(configPrefix(cfg, cfgErr)).SessionName())
		}
		return nil
	},
}

// describeWorkspace is one workspace's line: enough to tell a state entry with
// a worktree behind it from one without, and to say whether its chat is up.
func describeWorkspace(repoRoot string, ws *state.Workspace) string {
	parts := []string{ws.Branch}
	if ws.WorktreeDir == "" {
		parts = append(parts, "no worktree recorded")
	} else if _, err := os.Stat(ws.WorktreeDir); err != nil {
		parts = append(parts, "worktree missing (`opentree prune` clears it)")
	}
	if st, ok := chat.Query(chat.SocketPath(repoRoot, ws.Name), ws.Name); ok {
		chatState := "chat " + st.State
		if st.Behind() {
			chatState += " (older opentree — close and reopen the window to update it)"
		}
		parts = append(parts, chatState)
	} else {
		parts = append(parts, "no chat")
	}
	return strings.Join(parts, ", ")
}

// configPrefix is the tmux session prefix, falling back to the default when the
// config is the thing that is broken.
func configPrefix(cfg *config.Config, err error) string {
	if err != nil || cfg == nil {
		return config.Default().Tmux.SessionPrefix
	}
	return cfg.Tmux.SessionPrefix
}

// toolVersion is what a tool says about itself, or why it could not be asked.
func toolVersion(name string, args ...string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return "not on PATH"
	}
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return path + " (would not answer " + strings.Join(args, " ") + ")"
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]) + "  " + path
}

// ghStatus reports gh's version and whether it is logged in, because installed
// and usable are different things for this one — an unauthenticated gh is why
// PR status silently stops updating.
func ghStatus() string {
	if _, err := exec.LookPath("gh"); err != nil {
		return "not on PATH — PR and issue commands are unavailable"
	}
	v := toolVersion("gh", "--version")
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		return v + "  NOT AUTHENTICATED — run `gh auth login`"
	}
	return v + "  authenticated"
}

// describeRegistryInstall is one installed registry agent on one line: the
// version the index pinned, and whether the command it recorded is still
// there. The record naming a path and the path holding a binary are two
// different claims, and the state where they disagree is exactly the one
// somebody would be writing in about.
func describeRegistryInstall(r registry.Record) string {
	if info, err := os.Stat(r.Command); err != nil || info.IsDir() {
		return r.Entry.Version + "  " + r.Command + "  MISSING — `opentree agents update " + r.Entry.ID + "` reinstalls it"
	}
	return r.Entry.Version + "  " + r.Command
}

// describeAdapter locates the ACP adapter the way opentree locates it, which is
// not the way PATH does: it installs into a prefix of its own so uninstalling
// opentree does not leave a stray package behind, and the shim there is found
// by an absolute path rather than by a lookup. Asking PATH reported a perfectly
// good adapter as missing, which is the opposite of what a doctor is for.
//
// The installed version is worth printing beside it. The pinned spec above says
// what opentree would install; this says what is actually there, and the two
// disagreeing is exactly the state somebody would be writing in to ask about.
func describeAdapter(agent config.PredefinedAgent) string {
	resolved := agent.ResolveACPCommand()
	if resolved == "" {
		return "(none)"
	}
	if !filepath.IsAbs(resolved) {
		path, err := exec.LookPath(resolved)
		if err != nil {
			return resolved + " — not installed (press i in the agent picker, or `opentree agents setup`)"
		}
		return path + "  (found on PATH, not installed by opentree)"
	}
	if v := agent.ACPInstalledVersion(); v != "" {
		return resolved + "  (" + v + ")"
	}
	return resolved
}

// describeTool is a command's resolved path, or that there is none.
func describeTool(name string) string {
	if name == "" {
		return "(none)"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return name + " — not on PATH"
	}
	return path
}

// describeFile says whether a path is there, which is most of what a config
// question turns out to be.
func describeFile(path string) string {
	if path == "" {
		return "(could not be determined)"
	}
	info, err := os.Stat(path)
	if err != nil {
		return path + "  (absent)"
	}
	return fmt.Sprintf("%s  (%d bytes, %s)", path, info.Size(), info.Mode().Perm())
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
