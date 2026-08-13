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
	"os/exec"
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
	// Deep searches the tree at any depth rather than listing it one level
	// down. True only for a directory the user registered in their own config:
	// that is a place to look, and the agent reading it searches it for
	// **/SKILL.md. The standard trees are listed one level deep, because there
	// a nested SKILL.md is a skill's own reference material rather than a
	// second skill.
	Deep bool
}

// Trees is every skills directory opentree knows about, each with its readers
// collected. An empty repoRoot yields only the machine-wide trees.
func Trees(repoRoot string) []Tree {
	var out []Tree
	index := map[string]int{} // dir -> position in out

	add := func(dir string, scope Scope, agent string, deep bool) {
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
		out = append(out, Tree{Dir: dir, Scope: scope, Agents: []string{agent}, Deep: deep})
	}

	for _, agent := range config.PredefinedAgents {
		for _, dir := range agent.Skills.UserDirs {
			add(ExpandUserDir(dir), ScopeUser, agent.Name, false)
		}
		for _, dir := range agent.Skills.ExternalDirs {
			add(ExpandUserDir(dir), ScopeUser, agent.Name, false)
		}
		if repoRoot != "" {
			for _, dir := range agent.Skills.RepoDirs {
				add(filepath.Join(repoRoot, dir), ScopeRepo, agent.Name, false)
			}
		}
		for _, dir := range configTrees(agent.Skills, repoRoot) {
			add(dir, treeScope(dir, repoRoot), agent.Name, true)
		}
	}
	return out
}

// treeScope places a registered directory. Inside the repository it is the
// project's, and a worktree that cannot see it is missing something; anywhere
// else it belongs to the machine and every directory on it can see the skill.
func treeScope(dir, repoRoot string) Scope {
	if repoRoot != "" && strings.HasPrefix(dir, repoRoot+string(filepath.Separator)) {
		return ScopeRepo
	}
	return ScopeUser
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
// do with it: its own settings override if there is one, then the two things
// that leave a skill loaded but out of the model's reach, and otherwise plain
// on.
//
// A skill with no description is one of those two. The description is what a
// model matches a skill against, so a skill without one is filtered out of what
// the agent offers it — the same place `disable-model-invocation` puts a skill,
// reached by accident rather than on purpose. Both agents still advertise such
// a skill as a command, which is how it stays slash-invocable and why it cannot
// be reported as simply absent.
//
// Applied to every agent, on opencode's documentation and the shape of the
// format rather than on a probe: available_commands says whether a skill is
// loaded, not whether the model may reach for it, so `v` cannot settle this one
// either way. Understating is the safe direction — a row that says "manual" for
// a skill the model would have used costs less than one promising a capability
// that is not there.
func applyStates(skill *Skill, overrides map[string]map[string]State) {
	skill.States = make(map[string]State, len(skill.Agents))
	for _, agent := range skill.Agents {
		switch state, ok := overrides[agent][skill.Name]; {
		case ok:
			skill.States[agent] = state
		case skill.ManualOnly, skill.Description == "":
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
	if tree.Deep {
		return walk(tree)
	}
	entries, err := os.ReadDir(tree.Dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		// No IsDir check: a skill symlinked in from a dotfiles repo reports as a
		// link rather than a directory, and reading through it is the whole
		// point. A plain file simply fails the read below.
		if skill, ok := skillAt(filepath.Join(tree.Dir, e.Name()), tree); ok {
			out = append(out, skill)
		}
	}
	return out
}

// walk finds skills at any depth, which is how an agent reads a directory its
// user registered rather than one it defines itself.
func walk(tree Tree) []Skill {
	var out []Skill
	_ = filepath.WalkDir(tree.Dir, func(path string, d os.DirEntry, err error) error {
		// Errors are skipped rather than returned: an unreadable subdirectory
		// should cost its own skills, not the whole tree's.
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		if skill, ok := skillAt(filepath.Dir(path), tree); ok {
			out = append(out, skill)
		}
		return nil
	})
	return out
}

// skillAt reads one candidate directory. Anything without a readable SKILL.md
// is not a skill and is not an error either — trees hold ordinary files.
func skillAt(dir string, tree Tree) (Skill, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md")) // #nosec G304 -- a directory under a registered skills tree
	if err != nil {
		return Skill{}, false
	}
	meta := frontmatter(string(data))
	if meta.name == "" {
		meta.name = filepath.Base(dir)
	}
	return Skill{
		Name:        meta.name,
		Description: meta.description,
		ManualOnly:  meta.manualOnly,
		Dir:         dir,
		// Cloned: the caller merges readers into this slice when another tree
		// turns out to hold the same skill, and appending into the tree's own
		// would leak that into every other skill it holds.
		Agents: slices.Clone(tree.Agents),
		Scope:  tree.Scope,
	}, true
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

// Clone installs a skill from a git repository — the other way a skill arrives,
// alongside copying one that is already here.
//
// The .git directory is kept, so `git -C <dir> pull` updates the skill later.
// opentree records no provenance of its own and there is no registry to
// re-fetch from; the clone is the only thing that remembers where the skill
// came from.
func Clone(url, dir string) error {
	name := CloneName(url)
	if name == "" {
		return fmt.Errorf("%q does not name a skill", url)
	}
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// #nosec G702 -- the URL is the user's own, typed into their own terminal,
	// and "--" ends the option list so it cannot become a git flag.
	cmd := exec.Command("git", "clone", "--depth", "1", "--", url, dst)
	// A private repository over https would otherwise sit waiting for a
	// password on a terminal the TUI has taken over, with nothing on screen to
	// say so. Failing is the recoverable outcome.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dst)
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:] // git's own last word is the useful one
		}
		return fmt.Errorf("git clone: %s", msg)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		// A repository of several skills, or not a skill at all. Either way no
		// agent would load what was just cloned, and leaving it behind would
		// put a row in the list that means nothing.
		_ = os.RemoveAll(dst)
		return fmt.Errorf("%s has no SKILL.md at its root", url)
	}
	return nil
}

// cloneName is the directory a URL clones into: its last path element, without
// the .git suffix. Anything hidden or with no name at all is refused rather
// than guessed at — the name becomes a directory under the user's skills.
func CloneName(url string) string {
	name := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || strings.HasPrefix(name, ".") {
		return ""
	}
	return name
}
