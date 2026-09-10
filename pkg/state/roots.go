package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/axelgar/opentree/pkg/gitutil"
)

// Roots is every repository that has state under ~/.opentree/state, sorted.
//
// The directories are named after a hash of the root, so each file is read to
// find its way back: the repo_root it was last saved with, or — for a file
// written before that field existed — the repository any of its worktrees
// belongs to. A directory holding only a lock is not a repository and is
// skipped; a file with workspaces but no way back is reported in errs and
// left out.
func Roots() (roots []string, errs []error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(home, ".opentree", "state"))
	if err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, e := range entries {
		file := filepath.Join(home, ".opentree", "state", e.Name(), "state.json")
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			errs = append(errs, errors.New(file+": "+err.Error()))
			continue
		}
		root := recoverRoot(st)
		if root == "" {
			if len(st.Workspaces) > 0 {
				errs = append(errs, errors.New(file+": cannot tell which repository it belongs to"))
			}
			continue
		}
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots, errs
}

// recoverRoot is the file's repository, or "" when nothing in it still says.
func recoverRoot(st State) string {
	if st.RepoRoot != "" {
		if _, err := os.Stat(st.RepoRoot); err == nil {
			return st.RepoRoot
		}
	}
	for _, ws := range st.Workspaces {
		if ws.WorktreeDir == "" {
			continue
		}
		if root, err := gitutil.RepoRootIn(ws.WorktreeDir); err == nil {
			return root
		}
	}
	return ""
}
