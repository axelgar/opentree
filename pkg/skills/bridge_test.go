package skills

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

// opencodeRepoDir is opencode's canonical per-repository skills directory —
// the one Claude Code does not read.
func opencodeRepoDir(t *testing.T) string {
	t.Helper()
	dirs := opencodeSpec(t).RepoDirs
	if len(dirs) == 0 {
		t.Fatal("opencode has no repo skills directory")
	}
	return dirs[0]
}

// agentsWithRepoTrees is how many registry agents have a per-repository skills
// directory at all — the population Bridge works over, counted rather than
// written down so a fifth agent does not turn these tests red.
func agentsWithRepoTrees() int {
	n := 0
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.RepoDirs) > 0 {
			n++
		}
	}
	return n
}

// The gap Bridge closes, established by asking each agent what it loads from a
// directory holding one tree at a time: .opencode/skills is read by opencode
// alone. So a project skill kept there is one the list shows, that opentree
// links into every worktree, and that no other agent can use.
func TestBridge_MakesARepoSkillReadableByEveryAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, opencodeRepoDir(t)), "release",
		"---\nname: release\ndescription: Ship it.\n---\n")

	before := Scan(repo)
	if len(before) != 1 || len(before[0].Agents) != 1 {
		t.Fatalf("before bridging: %d skills — want one, readable by one agent", len(before))
	}

	made, err := Bridge(repo)
	if err != nil {
		t.Fatalf("Bridge: %v", err)
	}
	if len(made) != agentsWithRepoTrees()-1 {
		t.Fatalf("made = %v, want a tree for every other agent", made)
	}

	// One skill still, and now read by all of them: each bridge is another path
	// to the same directory, which Scan collapses.
	after := Scan(repo)
	if len(after) != 1 {
		t.Fatalf("after bridging: %d skills, want the same single skill", len(after))
	}
	if len(after[0].Agents) != agentsWithRepoTrees() {
		t.Errorf("Agents = %v, want every agent to read it", after[0].Agents)
	}
}

// An agent that already reads someone else's tree needs no bridge, and asking
// for one would put a directory in the user's repository that changes nothing.
// opencode reads Claude Code's repository tree directly — which its own
// documentation does not say, and which the first cut of this feature got wrong
// in the visible direction: it warned that a skill under .claude/skills was
// invisible to opencode while opencode was busy answering to it.
func TestBridge_ATreeAnAgentAlreadyReadsGetsNoBridge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, claudeRepoDir(t)), "release", "---\nname: release\n---\n")

	found := Scan(repo)
	if len(found) != 1 || len(found[0].Agents) < 2 {
		t.Fatalf("Scan() = %d skills with agents %v, want one read by more than its owner", len(found), agentsOf(found))
	}
	readers := found[0].Agents

	made, err := Bridge(repo)
	if err != nil {
		t.Fatalf("Bridge: %v", err)
	}
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.RepoDirs) == 0 || !slices.Contains(readers, agent.Name) {
			continue
		}
		if slices.Contains(made, agent.Skills.RepoDirs[0]) {
			t.Errorf("bridged %s for %s, which was already reading the tree", agent.Skills.RepoDirs[0], agent.Name)
		}
	}
}

// Relative, so it means the same thing from a worktree and can be committed
// alongside the skills it points at.
func TestBridge_LinksRelatively(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, opencodeRepoDir(t)), "release", "---\nname: release\n---\n")

	made, err := Bridge(repo)
	if err != nil || len(made) == 0 {
		t.Fatalf("Bridge: %v, made %v", err, made)
	}
	target, err := os.Readlink(filepath.Join(repo, made[0]))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("link points at %q — an absolute path does not survive being committed", target)
	}
}

func TestBridge_LeavesAnAgentThatHasItsOwnTreeAlone(t *testing.T) {
	repo := t.TempDir()
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.RepoDirs) > 0 {
			writeSkill(t, filepath.Join(repo, agent.Skills.RepoDirs[0]), "own", "---\nname: own\n---\n")
		}
	}
	made, err := Bridge(repo)
	if err != nil {
		t.Fatalf("Bridge: %v", err)
	}
	if len(made) != 0 {
		t.Errorf("made = %v, want nothing — every agent already has a tree of its own", made)
	}
}

func TestBridge_NothingToShare(t *testing.T) {
	made, err := Bridge(t.TempDir())
	if err != nil {
		t.Fatalf("Bridge: %v", err)
	}
	if len(made) != 0 {
		t.Errorf("made = %v, want nothing for a repository with no skills", made)
	}
}

// A bridge is a repository tree like any other, so a worktree gets it too —
// otherwise bridging would fix the agent one directory up and leave every
// workspace exactly as blind as before.
func TestBridge_ThenLinkReachesAWorktree(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".opentree", "feature")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(repo, opencodeRepoDir(t)), "release", "---\nname: release\n---\n")

	made, err := Bridge(repo)
	if err != nil || len(made) == 0 {
		t.Fatalf("Bridge: %v, made %v", err, made)
	}
	if _, err := Link(repo, worktree); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, made[0], "release", "SKILL.md")); err != nil {
		t.Errorf("the bridged tree is unreadable from the worktree: %v", err)
	}
}

// agentsOf flattens a scan for a failure message.
func agentsOf(list []Skill) [][]string {
	var out [][]string
	for _, s := range list {
		out = append(out, s.Agents)
	}
	return out
}
