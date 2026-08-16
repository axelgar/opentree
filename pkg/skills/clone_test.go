package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCloneName(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/someone/my-skill.git", "my-skill"},
		{"https://github.com/someone/my-skill", "my-skill"},
		{"https://github.com/someone/my-skill/", "my-skill"},
		{"git@github.com:someone/my-skill.git", "my-skill"},
		{"/local/path/my-skill", "my-skill"},
		// Refused rather than guessed at: the answer becomes a directory name
		// under the user's own skills.
		{"https://github.com/someone/.git", ""},
		{"", ""},
		{"/", ""},
	}
	for _, tt := range tests {
		if got := CloneName(tt.url); got != tt.want {
			t.Errorf("CloneName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// gitRepo makes a local repository holding one skill, so the clone path can be
// tested without a network.
func gitRepo(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := filepath.Join(t.TempDir(), "shipped-skill")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a skill"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "."},
		{"commit", "-qm", "skill"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestClone(t *testing.T) {
	src := gitRepo(t, "---\nname: shipped-skill\ndescription: Cloned in.\n---\n")
	tree := t.TempDir()

	if err := Clone(src, tree); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "shipped-skill", "SKILL.md")); err != nil {
		t.Fatalf("cloned skill unreadable: %v", err)
	}
	// The .git is kept on purpose: it is the only record of where the skill came
	// from, and what makes `git -C <dir> pull` an update.
	if _, err := os.Stat(filepath.Join(tree, "shipped-skill", ".git")); err != nil {
		t.Errorf("clone has no .git, so the skill can never be updated: %v", err)
	}
}

// A repository that is not a skill leaves nothing behind. Half an install would
// put a row in the list that no agent will ever load.
func TestClone_RefusesARepoWithNoSkill(t *testing.T) {
	src := gitRepo(t, "")
	tree := t.TempDir()

	if err := Clone(src, tree); err == nil {
		t.Fatal("Clone accepted a repository with no SKILL.md at its root")
	}
	if _, err := os.Stat(filepath.Join(tree, "shipped-skill")); !os.IsNotExist(err) {
		t.Error("the failed clone was left behind")
	}
}

func TestClone_RefusesToOverwrite(t *testing.T) {
	src := gitRepo(t, "---\nname: shipped-skill\n---\n")
	tree := t.TempDir()
	writeSkill(t, tree, "shipped-skill", "---\nname: mine\ndescription: Do not clobber.\n---\n")

	if err := Clone(src, tree); err == nil {
		t.Fatal("Clone overwrote an existing skill")
	}
	got, err := os.ReadFile(filepath.Join(tree, "shipped-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "---\nname: mine\ndescription: Do not clobber.\n---\n" {
		t.Errorf("the existing skill was changed: %q", got)
	}
}
