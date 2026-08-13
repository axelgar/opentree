package skills

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/axelgar/opentree/pkg/config"
)

// A worktree created by `git worktree add` carries only what git tracks. Most
// repositories leave their skills untracked — .claude/ is a working directory
// as often as it is a committed one — so every fresh worktree starts unable to
// see the skills its own repository defines, and the agent working there is
// quietly less capable than the same agent one directory up.
//
// opentree creates those worktrees, which makes it the only thing in the loop
// that can fix it.

// repoTrees are the skill directories this repository actually has, as paths
// relative to its root.
func repoTrees(repoRoot string) []string {
	var out []string
	for _, agent := range config.PredefinedAgents {
		for _, rel := range agent.Skills.RepoDirs {
			if slices.Contains(out, rel) {
				continue // another agent reads the same tree
			}
			if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
				continue
			}
			out = append(out, rel)
		}
	}
	return out
}

// Link points a worktree at the repository's skills and reports which trees it
// linked. Doing nothing is the common, correct outcome: a repository with no
// skills has nothing to share, and one that commits them has already had them
// checked out by git.
//
// A symlink rather than a copy, because a copy starts drifting the moment
// either side is edited, and a project skill is one thing rather than one per
// branch. Editing it from inside a worktree edits the repository's copy, which
// is what a shared skill means.
func Link(repoRoot, worktreePath string) ([]string, error) {
	var linked []string
	var errs []error
	for _, rel := range repoTrees(repoRoot) {
		dst := filepath.Join(worktreePath, rel)
		// Lstat, not Stat: git having checked the directory out is the signal to
		// leave it alone, and that is exactly the case where opentree should do
		// nothing rather than compete with the branch's own contents.
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Symlink(filepath.Join(repoRoot, rel), dst); err != nil {
			errs = append(errs, err)
			continue
		}
		linked = append(linked, rel)
	}
	return linked, errors.Join(errs...)
}

// Bridge points every agent's repository skills tree at the one this
// repository actually has, and reports the trees it created.
//
// Which agent reads which repository tree is not symmetric. opencode reads
// .claude/skills and .agents/skills as well as its own; Claude Code reads only
// .claude/skills. So a project skill kept under .opencode/skills is one the
// list shows, that opentree links into every worktree, and that Claude Code
// working there still cannot use.
//
// A relative link, so it means the same thing from a worktree and survives
// being committed: a repository that tracks its skills can track the bridge
// with them, and then opentree has nothing left to do.
//
// Not automatic, unlike Link. A worktree is opentree's own directory and
// linking inside it is housekeeping; the repository root is the user's, and a
// new directory there is theirs to ask for.
func Bridge(repoRoot string) ([]string, error) {
	have := repoTrees(repoRoot)
	if len(have) == 0 {
		return nil, nil // no project skills to share
	}
	src := filepath.Join(repoRoot, have[0])

	var made []string
	var errs []error
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.RepoDirs) == 0 {
			continue
		}
		if slices.ContainsFunc(agent.Skills.RepoDirs, func(d string) bool { return slices.Contains(have, d) }) {
			continue // this agent already reads a tree of its own
		}
		// The canonical spelling only. The alternates exist so an existing
		// directory is found, not so opentree can invent one.
		rel := agent.Skills.RepoDirs[0]
		dst := filepath.Join(repoRoot, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		target, err := filepath.Rel(filepath.Dir(dst), src)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Symlink(target, dst); err != nil {
			errs = append(errs, err)
			continue
		}
		made = append(made, rel)
	}
	return made, errors.Join(errs...)
}

// Missing reports the repository skill trees a worktree cannot see — what the
// list warns about and what relinking would repair.
//
// Stat rather than Lstat here, so a link left dangling by a deleted or renamed
// skills directory counts as missing. It is a link that resolves to nothing,
// and the agent behind it sees no skills at all.
func Missing(repoRoot, worktreePath string) []string {
	var missing []string
	for _, rel := range repoTrees(repoRoot) {
		if _, err := os.Stat(filepath.Join(worktreePath, rel)); err == nil {
			continue
		}
		missing = append(missing, rel)
	}
	return missing
}
