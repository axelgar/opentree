// Package bootstrap prepares a worktree for work.
//
// A worktree created by `git worktree add` carries only what git tracks, so a
// fresh one has no .env, no node_modules, no .venv — and the agent's first turn
// goes on discovering that, or worse, on "fixing" a lockfile that was never
// broken. opentree creates those worktrees, which makes it the only thing in
// the loop that can repair what git left behind.
//
// Three parts, one problem: seed the untracked config git will not carry, run
// the setup commands that build what seeding cannot copy, and start the dev
// server on demand. This file is the first of them.
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Seed points a worktree at the repository's own untracked config and reports
// what it linked. The entries are the project's explicit list — deriving them
// from what git ignores would return the worktree directory itself and copy a
// checkout into its own children.
//
// A symlink rather than a copy, for the reason skills.Link chose one: a copy
// starts drifting the moment either side is edited, and one credential set
// shared by every branch is the point. A branch that must diverge detaches
// deliberately rather than discovering it has.
//
// Best-effort by design. Every mistake that can be read out of the config was
// already reported by ValidateSeed before the worktree existed; what remains is
// the filesystem refusing, and a workspace that came up without its .npmrc is
// still a workspace.
func Seed(repoRoot, worktreePath string, entries []string) ([]string, error) {
	root := realpath(repoRoot)

	var seeded []string
	var errs []error
	for _, entry := range entries {
		p, err := resolve(root, entry)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		linked, err := p.linkInto(worktreePath)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if linked {
			seeded = append(seeded, p.rel)
		}
	}
	return seeded, errors.Join(errs...)
}

// ValidateSeed reports the entries opentree would refuse to link, so a config
// mistake is one message at creation time rather than a worktree that quietly
// never gets its .env.
//
// Separate from Seed because the two failures are different kinds. A path that
// leaves the repository is the config being wrong for every worktree it will
// ever make, and there is no per-worktree recovery from it; a symlink the
// filesystem refuses is one worktree's bad luck.
func ValidateSeed(repoRoot string, entries []string) error {
	root := realpath(repoRoot)

	var errs []error
	for _, entry := range entries {
		if _, err := resolve(root, entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// seedPath is one entry located: where the file is, and where in a worktree its
// link belongs. The same relative path on both sides — a seeded file appears
// exactly where the project said it lives.
type seedPath struct {
	rel string // cleaned, relative to the repository root and to the worktree
	src string // absolute, inside the repository root
}

// resolve places one entry, refusing anything that could reach outside the
// repository or that seeding is the wrong tool for.
//
// Escaping is a validation error rather than a trust prompt: there is no
// legitimate seed = ["../../.ssh/id_rsa"], and asking the user is the wrong
// answer to a question with one correct outcome.
func resolve(root, entry string) (seedPath, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return seedPath{}, fmt.Errorf("seed: empty path")
	}
	if filepath.IsAbs(entry) {
		return seedPath{}, fmt.Errorf("seed %q: paths are relative to the repository root", entry)
	}
	rel := filepath.Clean(entry)
	if !staysUnder(rel) {
		return seedPath{}, fmt.Errorf("seed %q: leaves the repository", entry)
	}
	src := filepath.Join(root, rel)

	// A symlink in the repository is a path in disguise: .env -> ~/.ssh/id_rsa
	// passes every check above and still hands a worktree something that was
	// never the project's to share. Only a file that exists can be resolved,
	// and one that does not cannot be linked either way.
	if real, err := filepath.EvalSymlinks(src); err == nil && !staysUnder(relTo(root, real)) {
		return seedPath{}, fmt.Errorf("seed %q: resolves to %s, outside the repository", entry, real)
	}

	// Config gets seeded; state gets built. node_modules is not a file you
	// link, it is the output of a setup command — and a worktree that rm -rf's
	// a linked node_modules has just emptied the main checkout's.
	if info, err := os.Stat(src); err == nil && info.IsDir() {
		return seedPath{}, fmt.Errorf("seed %q: a directory — seed the config a worktree needs and let a setup command build the rest", entry)
	}
	return seedPath{rel: rel, src: src}, nil
}

// linkInto makes one seeded link and reports whether it had to.
func (p seedPath) linkInto(worktreePath string) (bool, error) {
	if _, err := os.Stat(p.src); err != nil {
		// The project names a file this checkout does not have — an .env nobody
		// has created yet. Nothing to link, and nothing wrong: the list is what
		// a worktree should carry when it is there, not a claim that it is.
		return false, nil
	}

	dst := filepath.Join(worktreePath, p.rel)
	// Lstat, not Stat: git having checked the file out is the signal to leave
	// it alone, and that is exactly the case where opentree should do nothing
	// rather than compete with the branch's own contents.
	if _, err := os.Lstat(dst); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return false, err
	}
	if err := os.Symlink(p.src, dst); err != nil {
		return false, err
	}
	return true, nil
}

// staysUnder reports whether a cleaned relative path stays under the directory
// it is relative to.
func staysUnder(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// relTo is filepath.Rel with an answer for the case it cannot compute one: a
// path on another volume is not under root by any measure.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ".."
	}
	return rel
}

// realpath resolves the repository root's own symlinks, so a path compared
// against it is compared against the same spelling the filesystem reports. On
// macOS a temporary directory under /var is reached through one, and every
// seed entry would otherwise look like it escapes.
func realpath(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}
