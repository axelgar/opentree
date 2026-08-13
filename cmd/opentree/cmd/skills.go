package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/skills"
	"github.com/axelgar/opentree/pkg/workspace"
)

var SkillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Inspect the agent skills on this machine and propagate the repository's",
	Long: `Inspect the agent skills on this machine and propagate the repository's.

Skills are a filesystem convention rather than anything an agent exposes over
its API: a directory holding a SKILL.md, read by every agent that has the
feature. opentree reads them the same way.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var skillsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List every skill and the agents that can use it",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		// A repository is optional here. Outside one the machine-wide trees are
		// still worth listing, and refusing to would make this the only skills
		// command that needs a repo to answer a question about ~/.
		repoRoot, _ := gitutil.RepoRoot()
		found := skills.Scan(repoRoot)

		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			out, err := json.MarshalIndent(found, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal skills: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		if len(found) == 0 {
			fmt.Println("No skills found.")
			return nil
		}

		fmt.Printf("%-28s %-8s %-6s %-12s %s\n", "NAME", "AGENTS", "SCOPE", "STATE", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 100))
		for _, s := range found {
			fmt.Printf("%-28s %-8s %-6s %-12s %s\n",
				trim(s.Name, 28), marks(s), s.Scope, skillState(s), trim(s.Description, 44))
		}

		// The marks column is glyphs, so the legend has to be printed with it.
		var legend []string
		for _, agent := range config.PredefinedAgents {
			legend = append(legend, agent.Mark+" "+agent.Name)
		}
		fmt.Printf("\n%s\n", strings.Join(legend, "  ·  "))
		return nil
	},
}

var skillsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Give every agent and every workspace the repository's skills",
	Long: `Give every agent and every workspace the repository's skills.

A worktree carries only what git tracks, and most repositories leave their
skills untracked — so a workspace created before opentree linked them, or one
whose link was removed, cannot see the skills its own repository defines.

Which agent reads which repository directory is also not symmetric, so a
project skill kept under one agent's directory can be invisible to another
until the two are linked together.

Both are repaired here, and both are already done for workspaces opentree
creates from now on.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return err
		}

		bridged, err := skills.Bridge(repoRoot)
		if err != nil {
			return err
		}
		for _, tree := range bridged {
			fmt.Printf("✓ Linked %s to the repository's skills\n", tree)
		}

		done := len(bridged)
		for _, ws := range svc.ListWorkspaces() {
			linked, err := skills.Link(repoRoot, ws.WorktreeDir)
			if err != nil {
				return err
			}
			if len(linked) > 0 {
				fmt.Printf("✓ Linked %s into '%s'\n", strings.Join(linked, ", "), ws.Name)
				done++
			}
		}

		if done == 0 {
			fmt.Println("Nothing to sync.")
		}
		return nil
	},
}

// marks is every agent that reads the skill's tree, as its registry glyph. The
// TUI greys the mark of an agent that has the skill switched off; a pipe has no
// grey, so the STATE column carries that here.
func marks(s skills.Skill) string {
	var out []string
	for _, name := range s.Agents {
		mark, _, _ := config.Brand(name)
		out = append(out, mark)
	}
	return strings.Join(out, " ")
}

// skillState is what the agents will actually do with the skill. One word in
// the usual case, where a single override applies to everyone that can see it;
// several when the agents disagree.
func skillState(s skills.Skill) string {
	seen := map[skills.State]bool{}
	var out []string
	for _, name := range s.Agents {
		st := s.State(name)
		if seen[st] {
			continue
		}
		seen[st] = true
		// The row's short word for it. "on" is the only state Label leaves
		// unsaid, because a screen full of it says nothing — a column has to
		// name it or the cell reads as missing data.
		label := st.Label()
		if label == "" {
			label = "on"
		}
		out = append(out, label)
	}
	if len(out) == 1 {
		return out[0]
	}
	return strings.Join(out, "/")
}

// trim fits a cell. Runes rather than bytes: descriptions are prose and cutting
// one in half puts a replacement character in the column.
func trim(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}

func init() {
	skillsListCmd.Flags().BoolP("json", "j", false, "Output skills as JSON")

	SkillsCmd.AddCommand(skillsListCmd)
	SkillsCmd.AddCommand(skillsSyncCmd)
}
