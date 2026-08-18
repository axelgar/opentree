package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
)

var agentsSetupCmd = &cobra.Command{
	Use:   "setup <name>",
	Short: "Install a coding agent's ACP adapter",
	Long: `Install the Agent Client Protocol server an agent needs, if it needs one.

opentree talks to every agent over ACP and draws the conversation itself, so it
reads status straight from the protocol and no hooks are involved. Agents that
serve ACP themselves have nothing to install; the ones reached through an
adapter get it fetched into ~/.opentree/tools, not the user's global npm root,
at the version this opentree was built against and with npm's install hooks
switched off. The command it runs is printed before it runs.`,
	Args: cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []string
		for _, a := range config.PredefinedAgents {
			completions = append(completions, fmt.Sprintf("%s\t%s", a.Name, a.Description))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := config.FindAgent(args[0])
		if agent == nil {
			fmt.Printf("Unknown agent %q. Available agents:\n", args[0])
			for _, a := range config.PredefinedAgents {
				fmt.Printf("  - %s (%s)\n", a.Name, a.Command)
			}
			return fmt.Errorf("agent %q not found", args[0])
		}
		return setupACPAgent(agent)
	},
}

// setupACPAgent installs the agent's ACP adapter when it needs one. Agents that
// serve ACP themselves have nothing to set up, and say so.
func setupACPAgent(agent *config.PredefinedAgent) error {
	install := agent.ACPInstallCommand()
	if len(install) == 0 {
		return reportNoHooksNeeded(agent.Name)
	}
	// "Already there" and "already the version opentree was built against" used
	// to be the same sentence, because the install resolved `latest` and this
	// command's advice was to run it again. The version is a decision recorded
	// in the registry now, so a stale copy is replaced rather than reported as
	// fine — otherwise pinning would leave every existing install frozen on
	// whatever it happened to fetch.
	if agent.ACPInstalled() {
		switch have := agent.ACPInstalledVersion(); have {
		case "":
			// On PATH but not in opentree's prefix: somebody installed this
			// themselves, and their copy is theirs to manage. Naming the
			// pinned version is the whole remedy on offer.
			fmt.Printf("✓ %s is already on PATH at %s\n", agent.ACPCommand(), agent.ResolveACPCommand())
			fmt.Printf("opentree is built against %s and leaves your own copy alone.\n",
				agent.ACPPackageSpec())
			return nil
		case agent.ACP.Version:
			fmt.Printf("✓ %s %s is already available at %s\n",
				agent.ACPCommand(), have, agent.ResolveACPCommand())
			return nil
		default:
			fmt.Printf("%s %s is installed; opentree is built against %s.\n\n",
				agent.ACPCommand(), have, agent.ACP.Version)
		}
	}

	fmt.Printf("%s speaks the Agent Client Protocol through %s.\n", agent.Name, agent.ACPCommand())
	fmt.Printf("Installing %s", agent.ACPPackageSpec())
	if agent.ACP.InstallSize != "" {
		fmt.Printf(" (%s, needs node)", agent.ACP.InstallSize)
	}
	// The command is printed rather than summarised: this runs a package
	// manager over a few hundred packages, and the pin, the prefix and the
	// refusal to run install hooks are the parts worth being able to check.
	fmt.Printf(" into %s by running:\n\n  %s\n\n", config.ToolsDir(), strings.Join(install, " "))

	cmd := exec.Command(install[0], install[1:]...) // #nosec G204 -- from the agent registry
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	fmt.Printf("\n✓ %s ready\n", agent.ACPCommand())
	return nil
}

// reportNoHooksNeeded explains why an agent has no setup step, and points at the
// plugin an earlier opentree may have installed. The file is left alone rather
// than deleted: it lives in the user's own agent config.
func reportNoHooksNeeded(name string) error {
	fmt.Printf("%s speaks the Agent Client Protocol, so opentree runs it through\n", name)
	fmt.Println("its own chat view and reads status straight from the protocol.")
	fmt.Println("There is nothing to install.")

	if path, err := homePath(".config", "opencode", "plugin", "opentree-status.js"); err == nil {
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("\nAn older opentree left a status plugin at\n  %s\n", path)
			fmt.Println("It is now unused and safe to delete.")
		}
	}
	return nil
}

func homePath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}
