package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLink_GivesAWorktreeTheReposSkills(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".opentree", "feature")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	repoDir := claudeRepoDir(t)
	writeSkill(t, filepath.Join(repo, repoDir), "release", "---\nname: release\ndescription: Ship it.\n---\n")

	linked, err := Link(repo, worktree)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 1 || linked[0] != repoDir {
		t.Fatalf("linked = %v, want [%s]", linked, repoDir)
	}

	// The agent in the worktree must be able to read the skill through the link.
	got, err := os.ReadFile(filepath.Join(worktree, repoDir, "release", "SKILL.md"))
	if err != nil {
		t.Fatalf("skill unreadable from the worktree: %v", err)
	}
	if len(got) == 0 {
		t.Error("skill read through the link is empty")
	}

	// A link, not a copy: an edit in the repo is visible in the worktree, so the
	// two can never disagree about what a project skill says.
	if err := os.WriteFile(filepath.Join(repo, repoDir, "release", "SKILL.md"), []byte("---\nname: edited\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(worktree, repoDir, "release", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "---\nname: edited\n---\n" {
		t.Errorf("worktree saw %q after the repo was edited — the link drifted", got)
	}
}

// A repository that commits its skills has already had them checked out by git,
// and opentree replacing that with a link would hide whatever the branch says.
func TestLink_LeavesAnExistingDirectoryAlone(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".opentree", "feature")
	repoDir := claudeRepoDir(t)
	writeSkill(t, filepath.Join(repo, repoDir), "release", "---\nname: repo version\n---\n")
	writeSkill(t, filepath.Join(worktree, repoDir), "release", "---\nname: branch version\n---\n")

	linked, err := Link(repo, worktree)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 0 {
		t.Errorf("linked = %v, want nothing — git already provided the directory", linked)
	}
	got, err := os.ReadFile(filepath.Join(worktree, repoDir, "release", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "---\nname: branch version\n---\n" {
		t.Errorf("worktree content = %q, want the branch's own", got)
	}
}

func TestLink_NothingToShare(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".opentree", "feature")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	linked, err := Link(repo, worktree)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 0 {
		t.Errorf("linked = %v, want nothing", linked)
	}
}

func TestMissing(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".opentree", "feature")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	repoDir := claudeRepoDir(t)
	writeSkill(t, filepath.Join(repo, repoDir), "release", "---\nname: release\n---\n")

	if got := Missing(repo, worktree); len(got) != 1 || got[0] != repoDir {
		t.Errorf("Missing = %v, want [%s]", got, repoDir)
	}
	if _, err := Link(repo, worktree); err != nil {
		t.Fatal(err)
	}
	if got := Missing(repo, worktree); len(got) != 0 {
		t.Errorf("Missing after Link = %v, want nothing", got)
	}
}

// A link left pointing at a path that no longer exists — a repository moved
// since the worktree was made — resolves to nothing, so the agent behind it
// sees no skills at all. That is missing, however present the link looks.
func TestMissing_DanglingLinkCounts(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".opentree", "feature")
	repoDir := claudeRepoDir(t)
	writeSkill(t, filepath.Join(repo, repoDir), "release", "---\nname: release\n---\n")

	dst := filepath.Join(worktree, repoDir)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "moved-away", ".claude", "skills"), dst); err != nil {
		t.Fatal(err)
	}

	if got := Missing(repo, worktree); len(got) != 1 || got[0] != repoDir {
		t.Errorf("Missing = %v, want [%s] — the link resolves to nothing", got, repoDir)
	}
	// Link leaves it alone rather than replacing it: something put that link
	// there, and repairing it is a decision the user makes from the Skills view.
	linked, err := Link(repo, worktree)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 0 {
		t.Errorf("linked = %v, want nothing over an existing entry", linked)
	}
}
