package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// newRepo returns a repository root and a worktree path under it, in the shape
// opentree creates them: .opentree/<branch> inside the repository.
func newRepo(t *testing.T) (repo, worktree string) {
	t.Helper()
	repo = t.TempDir()
	worktree = filepath.Join(repo, ".opentree", "feature")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	return repo, worktree
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSeed_GivesAWorktreeTheReposUntrackedConfig(t *testing.T) {
	repo, worktree := newRepo(t)
	write(t, filepath.Join(repo, ".env"), "TOKEN=hunter2\n")

	seeded, err := Seed(repo, worktree, []string{".env"})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(seeded) != 1 || seeded[0] != ".env" {
		t.Fatalf("seeded = %v, want [.env]", seeded)
	}

	got, err := os.ReadFile(filepath.Join(worktree, ".env"))
	if err != nil {
		t.Fatalf("seeded file unreadable from the worktree: %v", err)
	}
	if string(got) != "TOKEN=hunter2\n" {
		t.Errorf("worktree read %q, want the repository's own", got)
	}

	// A link, not a copy: one credential set, shared. Rotating it in the
	// repository rotates it everywhere rather than in one worktree out of five.
	write(t, filepath.Join(repo, ".env"), "TOKEN=rotated\n")
	got, err = os.ReadFile(filepath.Join(worktree, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "TOKEN=rotated\n" {
		t.Errorf("worktree read %q after the repository was edited — the link drifted", got)
	}
}

func TestSeed_NestedPathMakesItsParents(t *testing.T) {
	repo, worktree := newRepo(t)
	write(t, filepath.Join(repo, "config", "local.json"), "{}\n")

	seeded, err := Seed(repo, worktree, []string{"config/local.json"})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(seeded) != 1 {
		t.Fatalf("seeded = %v, want one entry", seeded)
	}
	if _, err := os.Stat(filepath.Join(worktree, "config", "local.json")); err != nil {
		t.Errorf("nested seed not readable: %v", err)
	}
}

// The list says what a worktree should carry when the repository has it, not
// that the repository has it. An .env nobody has created yet is the ordinary
// state of a fresh clone.
func TestSeed_MissingSourceIsNotAnError(t *testing.T) {
	repo, worktree := newRepo(t)

	seeded, err := Seed(repo, worktree, []string{".env", ".npmrc"})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(seeded) != 0 {
		t.Errorf("seeded = %v, want nothing", seeded)
	}
	if _, err := os.Lstat(filepath.Join(worktree, ".env")); err == nil {
		t.Error("a dangling link was left where the file does not exist")
	}
}

// A branch that tracks its own .env has already had it checked out by git, and
// opentree replacing that with a link would hide what the branch says.
func TestSeed_LeavesACheckedOutFileAlone(t *testing.T) {
	repo, worktree := newRepo(t)
	write(t, filepath.Join(repo, ".env"), "TOKEN=repo\n")
	write(t, filepath.Join(worktree, ".env"), "TOKEN=branch\n")

	seeded, err := Seed(repo, worktree, []string{".env"})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(seeded) != 0 {
		t.Errorf("seeded = %v, want nothing — git already provided the file", seeded)
	}
	got, err := os.ReadFile(filepath.Join(worktree, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "TOKEN=branch\n" {
		t.Errorf("worktree content = %q, want the branch's own", got)
	}
}

// Seeding twice is what happens every time a worktree is repaired, so the
// second run must be a no-op rather than a failure over an existing link.
func TestSeed_IsIdempotent(t *testing.T) {
	repo, worktree := newRepo(t)
	write(t, filepath.Join(repo, ".env"), "TOKEN=hunter2\n")

	if _, err := Seed(repo, worktree, []string{".env"}); err != nil {
		t.Fatalf("first Seed: %v", err)
	}
	seeded, err := Seed(repo, worktree, []string{".env"})
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	if len(seeded) != 0 {
		t.Errorf("second Seed reported %v, want nothing left to do", seeded)
	}
}

// node_modules is the output of a setup command, not a file to link — and a
// worktree that rm -rf's a linked one has just emptied the main checkout's.
func TestSeed_RefusesADirectory(t *testing.T) {
	repo, worktree := newRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}

	seeded, err := Seed(repo, worktree, []string{"node_modules"})
	if err == nil {
		t.Fatal("Seed accepted a directory")
	}
	if len(seeded) != 0 {
		t.Errorf("seeded = %v, want nothing", seeded)
	}
	if _, err := os.Lstat(filepath.Join(worktree, "node_modules")); err == nil {
		t.Error("the directory was linked anyway")
	}
}

// One bad entry is not the others' problem: the .env still arrives.
func TestSeed_OneBadEntryDoesNotCostTheRest(t *testing.T) {
	repo, worktree := newRepo(t)
	write(t, filepath.Join(repo, ".env"), "TOKEN=hunter2\n")

	seeded, err := Seed(repo, worktree, []string{"../outside", ".env"})
	if err == nil {
		t.Fatal("Seed accepted a path outside the repository")
	}
	if len(seeded) != 1 || seeded[0] != ".env" {
		t.Errorf("seeded = %v, want [.env] — the good entry still applies", seeded)
	}
}

func TestValidateSeed_RefusesPathsThatLeaveTheRepository(t *testing.T) {
	repo, _ := newRepo(t)

	for _, entry := range []string{
		"../.ssh/id_rsa",
		"../../.ssh/id_rsa",
		"nested/../../escape",
		filepath.Join(t.TempDir(), "elsewhere"), // absolute
		"",
	} {
		if err := ValidateSeed(repo, []string{entry}); err == nil {
			t.Errorf("ValidateSeed(%q) = nil, want an error", entry)
		}
	}
}

// A symlink in the repository is a path in disguise: this one passes every
// lexical check and still hands the worktree a key that was never the
// project's to share.
func TestValidateSeed_RefusesASymlinkOutOfTheRepository(t *testing.T) {
	repo, worktree := newRepo(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "id_rsa"), "PRIVATE KEY\n")
	if err := os.Symlink(filepath.Join(outside, "id_rsa"), filepath.Join(repo, ".env")); err != nil {
		t.Fatal(err)
	}

	if err := ValidateSeed(repo, []string{".env"}); err == nil {
		t.Fatal("ValidateSeed accepted a link out of the repository")
	}
	if _, err := Seed(repo, worktree, []string{".env"}); err == nil {
		t.Fatal("Seed followed a link out of the repository")
	}
	if _, err := os.Lstat(filepath.Join(worktree, ".env")); err == nil {
		t.Error("the escaping link was seeded anyway")
	}
}

// A link inside the repository is ordinary — .env -> .env.local is how plenty
// of projects spell it — and refusing it would fail the common case to catch
// the rare one.
func TestValidateSeed_AllowsASymlinkInsideTheRepository(t *testing.T) {
	repo, worktree := newRepo(t)
	write(t, filepath.Join(repo, ".env.local"), "TOKEN=hunter2\n")
	if err := os.Symlink(filepath.Join(repo, ".env.local"), filepath.Join(repo, ".env")); err != nil {
		t.Fatal(err)
	}

	if err := ValidateSeed(repo, []string{".env"}); err != nil {
		t.Fatalf("ValidateSeed: %v", err)
	}
	seeded, err := Seed(repo, worktree, []string{".env"})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(seeded) != 1 {
		t.Fatalf("seeded = %v, want [.env]", seeded)
	}
	got, err := os.ReadFile(filepath.Join(worktree, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "TOKEN=hunter2\n" {
		t.Errorf("worktree read %q through the chain of links", got)
	}
}

// Nothing configured is the state every repository starts in.
func TestSeed_NothingToDo(t *testing.T) {
	repo, worktree := newRepo(t)

	seeded, err := Seed(repo, worktree, nil)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(seeded) != 0 {
		t.Errorf("seeded = %v, want nothing", seeded)
	}
	if err := ValidateSeed(repo, nil); err != nil {
		t.Errorf("ValidateSeed(nil) = %v, want nil", err)
	}
}
