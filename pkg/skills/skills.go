// Package skills reads and manages the agent skills installed on this machine.
//
// A skill is a directory holding a SKILL.md whose YAML frontmatter names and
// describes it. Every agent that supports skills uses that same shape, and none
// of them expose skills over ACP — so opentree discovers them by walking
// directories, and can add, copy, and remove them the same way.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
)

// Scope is where a skill lives, which decides who can see it.
type Scope int

const (
	// ScopeUser is the machine-wide tree, available in every repository.
	ScopeUser Scope = iota
	// ScopeRepo is this repository's own tree — the one a fresh worktree misses
	// unless the skills are committed.
	ScopeRepo
)

func (s Scope) String() string {
	if s == ScopeRepo {
		return "repo"
	}
	return "user"
}

// Skill is one SKILL.md and the directory holding it.
type Skill struct {
	Name        string // frontmatter name, falling back to the directory name
	Description string
	Dir         string // the directory holding SKILL.md
	// Agents are every registry agent that reads this skill's tree, in registry
	// order. Usually more than one: agents auto-load each other's directories,
	// so a skill installed once is commonly usable from all of them.
	Agents []string
	Scope  Scope
	// States is each agent's effective availability, keyed by agent name. An
	// agent reading the same file can still have been told to ignore it, so
	// this is per agent rather than per skill.
	States map[string]State
	// ManualOnly records the skill's own request not to be model-invoked,
	// before any override. Kept apart from States so clearing an override
	// returns the skill to what it asked for rather than to fully automatic.
	ManualOnly bool
}

// State is the skill's availability to one agent, defaulting to on for an agent
// with no override mechanism.
func (s Skill) State(agent string) State {
	if state, ok := s.States[agent]; ok {
		return state
	}
	return StateOn
}

// Tree is one skills directory and the agents that read it.
//
// Directories are the unit rather than agents because the mapping is
// many-to-many — opencode loads Claude Code's global tree as its own — and
// scanning per agent would read the same directory twice and report one skill
// as two.
type Tree struct {
	Dir    string
	Scope  Scope
	Agents []string
}

// Trees is every skills directory opentree knows about, each with its readers
// collected. An empty repoRoot yields only the machine-wide trees.
func Trees(repoRoot string) []Tree {
	var out []Tree
	index := map[string]int{} // dir -> position in out

	add := func(dir string, scope Scope, agent string) {
		if dir == "" {
			return
		}
		if i, seen := index[dir]; seen {
			if !slices.Contains(out[i].Agents, agent) {
				out[i].Agents = append(out[i].Agents, agent)
			}
			return
		}
		index[dir] = len(out)
		out = append(out, Tree{Dir: dir, Scope: scope, Agents: []string{agent}})
	}

	for _, agent := range config.PredefinedAgents {
		for _, dir := range agent.Skills.UserDirs {
			add(ExpandUserDir(dir), ScopeUser, agent.Name)
		}
		for _, dir := range agent.Skills.ExternalDirs {
			add(ExpandUserDir(dir), ScopeUser, agent.Name)
		}
		if repoRoot != "" {
			for _, dir := range agent.Skills.RepoDirs {
				add(filepath.Join(repoRoot, dir), ScopeRepo, agent.Name)
			}
		}
	}
	return out
}

// Scan finds every skill opentree can see, once each.
//
// Trees overlap by symlink as well as by configuration: a skill installer may
// keep the real directory in one tree and link it into another, so the same
// SKILL.md is reachable by two paths. Those are one skill readable by both
// agents, not two — collapsing them on the resolved path is what keeps the
// list honest, and what leaves the "duplicate" tag meaning a genuine conflict
// between two different files.
//
// The surviving entry is the resolved directory, so editing and deleting act
// on the real skill rather than on a link to it.
//
// Ordering is by name then directory, so the listing is stable and two
// same-named skills sit together where the difference is visible.
func Scan(repoRoot string) []Skill {
	var out []Skill
	index := map[string]int{} // resolved dir -> position in out

	// Read each agent's overrides once rather than per skill: they live in
	// settings files, and a scan touches every skill on the machine.
	overrides := map[string]map[string]State{}
	for _, agent := range config.PredefinedAgents {
		overrides[agent.Name] = readOverrides(agent.Skills, repoRoot)
	}

	for _, tree := range Trees(repoRoot) {
		for _, skill := range read(tree) {
			skill.Dir = resolve(skill.Dir)
			if i, seen := index[skill.Dir]; seen {
				out[i].Agents = mergeAgents(out[i].Agents, skill.Agents)
				applyStates(&out[i], overrides)
				continue
			}
			index[skill.Dir] = len(out)
			applyStates(&skill, overrides)
			out = append(out, skill)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// applyStates works out what each agent that can see this skill will actually
// do with it: its own settings override if there is one, the skill's own
// request not to be model-invoked if there is not, and otherwise plain on.
func applyStates(skill *Skill, overrides map[string]map[string]State) {
	skill.States = make(map[string]State, len(skill.Agents))
	for _, agent := range skill.Agents {
		switch state, ok := overrides[agent][skill.Name]; {
		case ok:
			skill.States[agent] = state
		case skill.ManualOnly:
			skill.States[agent] = StateManualOnly
		default:
			skill.States[agent] = StateOn
		}
	}
}

// resolve follows symlinks to the directory actually holding the skill,
// falling back to the path as given when it cannot be resolved.
func resolve(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// mergeAgents unions two reader lists back into registry order, so the marks
// on a row always appear in the same sequence.
func mergeAgents(a, b []string) []string {
	merged := slices.Clone(a)
	for _, name := range b {
		if !slices.Contains(merged, name) {
			merged = append(merged, name)
		}
	}
	slices.SortFunc(merged, func(x, y string) int {
		return registryIndex(x) - registryIndex(y)
	})
	return merged
}

func registryIndex(name string) int {
	for i, agent := range config.PredefinedAgents {
		if agent.Name == name {
			return i
		}
	}
	return len(config.PredefinedAgents)
}

// read lists the skills in one tree. A tree no agent ever created is not an
// error — most of these directories are absent on any given machine.
func read(tree Tree) []Skill {
	entries, err := os.ReadDir(tree.Dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		// No IsDir check: a skill symlinked in from a dotfiles repo reports as a
		// link rather than a directory, and reading through it is the whole
		// point. A plain file simply fails the read below.
		skillDir := filepath.Join(tree.Dir, e.Name())
		data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			continue
		}
		meta := frontmatter(string(data))
		if meta.name == "" {
			meta.name = e.Name()
		}
		out = append(out, Skill{
			Name:        meta.name,
			Description: meta.description,
			ManualOnly:  meta.manualOnly,
			Dir:         skillDir,
			// Cloned: the caller merges readers into this slice when another
			// tree turns out to hold the same skill, and appending into the
			// tree's own would leak that into every other skill it holds.
			Agents: slices.Clone(tree.Agents),
			Scope:  tree.Scope,
		})
	}
	return out
}

// skillMeta is what the list needs out of a SKILL.md's frontmatter.
type skillMeta struct {
	name        string
	description string
	// manualOnly is the skill declaring that the model should not reach for it
	// unaided. Such a skill is loaded and slash-invocable, which is why it can
	// be absent from an agent's own skill listing and still answer to its name.
	manualOnly bool
}

// frontmatter pulls what the list needs out of the YAML block at the top of a
// SKILL.md.
//
// Hand-rolled rather than adding a YAML dependency: three keys out of a fenced
// block is a line scan, and the format is fixed by the skill convention. A
// description folded across several lines keeps only its first — the list
// renders one line per skill, so the rest would be cut anyway. Anything this
// does not understand yields a zero value and the caller falls back to the
// directory name, which is always right enough to render.
func frontmatter(text string) skillMeta {
	var meta skillMeta
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return meta
	}
	block, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return meta
	}
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		// Cut on the first colon only, so a description containing one keeps it.
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			meta.name = value
		case "description":
			meta.description = value
		case "disable-model-invocation":
			meta.manualOnly = value == "true"
		}
	}
	return meta
}

// ExpandUserDir resolves a registry skills path against the user's home.
//
// XDG_CONFIG_HOME is honoured for the ~/.config prefix because opencode reads
// it: a user who moved their config would otherwise be shown an empty list and
// told they have no skills.
func ExpandUserDir(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/.config/"); ok {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, rest)
		}
	}
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, rest)
}

// Delete removes a skill's directory.
func Delete(s Skill) error {
	if s.Dir == "" {
		return fmt.Errorf("skill %q has no directory", s.Name)
	}
	return os.RemoveAll(s.Dir)
}

// CopyTo installs a copy of a skill into another tree — the way a skill written
// for one agent reaches another, since they all read the same format.
//
// An existing skill of that name is never overwritten: the two may have
// diverged, and clobbering the destination would be the one mistake that loses
// work no git history is holding.
func CopyTo(s Skill, dir string) error {
	if s.Dir == "" {
		return fmt.Errorf("skill %q has no directory", s.Name)
	}
	dst := filepath.Join(dir, filepath.Base(s.Dir))
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.CopyFS(dst, os.DirFS(s.Dir)); err != nil {
		// A half-copied skill is worse than none: the agent would load it and
		// fail on a missing reference.
		_ = os.RemoveAll(dst)
		return err
	}
	return nil
}
