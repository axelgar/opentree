package state

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/axelgar/opentree/pkg/fsutil"
)

// writeState puts a state.json where Dir would for root's key. The file's
// contents are the caller's, so a file from before repo_root can be staged.
func writeState(t *testing.T, key, content string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".opentree", "state", key)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if content == "" {
		if err := os.WriteFile(filepath.Join(dir, "state.lock"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRoots(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home, _ := os.UserHomeDir()
	if err := os.RemoveAll(filepath.Join(home, ".opentree", "state")); err != nil {
		t.Fatal(err)
	}

	// Route 1: the file says.
	stamped := t.TempDir()
	writeState(t, fsutil.RepoKey(stamped), `{"version":1,"repo_root":"`+stamped+`","workspaces":{}}`)

	// Route 2: a file from before repo_root, whose worktree still exists.
	repo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	writeState(t, fsutil.RepoKey(repo), `{"version":1,"workspaces":{"a":{"name":"a","worktree_dir":"`+repo+`"}}}`)

	// A stamped root that has since moved, with no worktree to fall back on.
	writeState(t, "opentree-gone", `{"version":1,"repo_root":"`+filepath.Join(home, "nowhere")+`","workspaces":{"a":{"name":"a"}}}`)
	// Only a lock: not a repository, not an error.
	writeState(t, "opentree-lock", "")
	// Workspaces and no way back: reported.
	writeState(t, "opentree-lost", `{"version":1,"workspaces":{"a":{"name":"a"}}}`)

	roots, errs := Roots()
	if len(roots) != 2 || roots[0] != repo && roots[1] != repo || roots[0] != stamped && roots[1] != stamped {
		t.Fatalf("Roots() = %v, want %v and %v", roots, stamped, repo)
	}
	if len(errs) != 2 {
		t.Fatalf("Roots() errs = %v, want one for the lost file and one for the moved root", errs)
	}
}

// A save stamps the repository into the file, so the next Roots needs no git.
func TestSave_StampsRepoRoot(t *testing.T) {
	root := t.TempDir()
	st, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddWorkspace(&Workspace{Name: "a", Branch: "a"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(Dir(root), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.RepoRoot != root {
		t.Fatalf("repo_root = %q, want %q", got.RepoRoot, root)
	}
}
