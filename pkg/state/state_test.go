package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return store
}

func sampleWorkspace(name string) *Workspace {
	return &Workspace{
		Name:        name,
		Branch:      "feature/" + name,
		BaseBranch:  "main",
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:      "active",
		Agent:       "opencode",
		WorktreeDir: "/tmp/" + name,
	}
}

func TestNew_FreshDirectory(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() on empty dir failed: %v", err)
	}
	if store == nil {
		t.Fatal("New() returned nil")
	}
	workspaces := store.ListWorkspaces()
	if len(workspaces) != 0 {
		t.Errorf("expected 0 workspaces on fresh store, got %d", len(workspaces))
	}
}

func TestNew_LoadsExistingState(t *testing.T) {
	dir := t.TempDir()

	// Populate state via one store instance.
	store1, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	ws := sampleWorkspace("alpha")
	if err := store1.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	// Open a second store pointing at the same directory.
	store2, err := New(dir)
	if err != nil {
		t.Fatalf("New() with existing state failed: %v", err)
	}
	got, err := store2.GetWorkspace("alpha")
	if err != nil {
		t.Fatalf("GetWorkspace() failed after reload: %v", err)
	}
	if got.Branch != ws.Branch {
		t.Errorf("Branch = %q, want %q", got.Branch, ws.Branch)
	}
}

func TestAddWorkspace_And_GetWorkspace(t *testing.T) {
	store := newTestStore(t)
	ws := sampleWorkspace("beta")

	if err := store.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	got, err := store.GetWorkspace("beta")
	if err != nil {
		t.Fatalf("GetWorkspace() failed: %v", err)
	}

	if got.Name != ws.Name {
		t.Errorf("Name = %q, want %q", got.Name, ws.Name)
	}
	if got.Branch != ws.Branch {
		t.Errorf("Branch = %q, want %q", got.Branch, ws.Branch)
	}
	if got.BaseBranch != ws.BaseBranch {
		t.Errorf("BaseBranch = %q, want %q", got.BaseBranch, ws.BaseBranch)
	}
	if got.Status != ws.Status {
		t.Errorf("Status = %q, want %q", got.Status, ws.Status)
	}
	if got.Agent != ws.Agent {
		t.Errorf("Agent = %q, want %q", got.Agent, ws.Agent)
	}
	if got.WorktreeDir != ws.WorktreeDir {
		t.Errorf("WorktreeDir = %q, want %q", got.WorktreeDir, ws.WorktreeDir)
	}
}

func TestGetWorkspace_NotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.GetWorkspace("nonexistent")
	if err == nil {
		t.Fatal("GetWorkspace() expected error for missing workspace, got nil")
	}
}

func TestUpdate(t *testing.T) {
	store := newTestStore(t)

	if err := store.AddWorkspace(sampleWorkspace("gamma")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	const prURL = "https://github.com/example/repo/pull/1"
	if err := store.Update("gamma", func(ws *Workspace) error {
		ws.Status = "idle"
		ws.PRURL = prURL
		ws.PRStatus = "open"
		return nil
	}); err != nil {
		t.Fatalf("Update() failed: %v", err)
	}

	got, err := store.GetWorkspace("gamma")
	if err != nil {
		t.Fatalf("GetWorkspace() after update failed: %v", err)
	}
	if got.Status != "idle" {
		t.Errorf("Status after update = %q, want %q", got.Status, "idle")
	}
	if got.PRURL != prURL {
		t.Errorf("PRURL = %q, want %q", got.PRURL, prURL)
	}
	if got.PRStatus != "open" {
		t.Errorf("PRStatus = %q, want %q", got.PRStatus, "open")
	}
}

func TestUpdate_NotFound(t *testing.T) {
	store := newTestStore(t)

	called := false
	err := store.Update("ghost", func(*Workspace) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("Update() expected error for non-existent workspace, got nil")
	}
	if called {
		t.Error("Update() ran the callback for a workspace that does not exist")
	}
}

// A callback that fails leaves the record exactly as it was — both on disk and
// in memory. Callers use the error to abandon a change, not to half-apply it.
func TestUpdate_CallbackErrorWritesNothing(t *testing.T) {
	store := newTestStore(t)
	if err := store.AddWorkspace(sampleWorkspace("delta")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	sentinel := errors.New("no")
	err := store.Update("delta", func(ws *Workspace) error {
		ws.Status = "clobbered"
		ws.ACPSessionID = "ses_x"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Update() error = %v, want the callback's own error", err)
	}

	got, err := store.GetWorkspace("delta")
	if err != nil {
		t.Fatalf("GetWorkspace(): %v", err)
	}
	if got.Status == "clobbered" || got.ACPSessionID != "" {
		t.Errorf("failed Update() still applied its changes: %+v", got)
	}
}

// The defect Update exists for. The dashboard and every `opentree chat` are
// separate processes over one state.json. Each reads the workspace, changes the
// field it cares about, and writes. Under the whole-record write this replaced,
// whichever wrote last put back its own stale copy of the other's field — and
// nothing re-derives a session id or a setup hash, so it was gone for good.
func TestUpdate_ConcurrentProcessesKeepEachOthersFields(t *testing.T) {
	dir := t.TempDir()

	a, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := a.AddWorkspace(sampleWorkspace("shared")); err != nil {
		t.Fatalf("AddWorkspace(): %v", err)
	}

	// b opens the store now, so it holds a snapshot from before a's write.
	b, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := a.Update("shared", func(ws *Workspace) error {
		ws.ACPSessionID = "ses_a"
		ws.SetupHash = "hash_a"
		ws.Port = 3001
		return nil
	}); err != nil {
		t.Fatalf("a.Update(): %v", err)
	}

	if err := b.Update("shared", func(ws *Workspace) error {
		ws.PRURL = "https://github.com/example/repo/pull/7"
		return nil
	}); err != nil {
		t.Fatalf("b.Update(): %v", err)
	}

	final, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	got, err := final.GetWorkspace("shared")
	if err != nil {
		t.Fatalf("GetWorkspace(): %v", err)
	}
	if got.ACPSessionID != "ses_a" {
		t.Errorf("ACPSessionID = %q, want %q — b's write erased a's session", got.ACPSessionID, "ses_a")
	}
	if got.SetupHash != "hash_a" {
		t.Errorf("SetupHash = %q, want %q", got.SetupHash, "hash_a")
	}
	if got.Port != 3001 {
		t.Errorf("Port = %d, want 3001", got.Port)
	}
	if got.PRURL == "" {
		t.Error("PRURL is empty — b's own write did not survive either")
	}
}

func TestDeleteWorkspace(t *testing.T) {
	store := newTestStore(t)
	ws := sampleWorkspace("delta")

	if err := store.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	if err := store.DeleteWorkspace("delta"); err != nil {
		t.Fatalf("DeleteWorkspace() failed: %v", err)
	}

	_, err := store.GetWorkspace("delta")
	if err == nil {
		t.Fatal("GetWorkspace() expected error after delete, got nil")
	}
}

func TestListWorkspaces(t *testing.T) {
	store := newTestStore(t)

	// Empty store returns non-nil empty slice.
	list := store.ListWorkspaces()
	if list == nil {
		t.Fatal("ListWorkspaces() returned nil, want empty slice")
	}
	if len(list) != 0 {
		t.Errorf("ListWorkspaces() len = %d, want 0", len(list))
	}

	names := []string{"ws1", "ws2", "ws3"}
	for _, n := range names {
		if err := store.AddWorkspace(sampleWorkspace(n)); err != nil {
			t.Fatalf("AddWorkspace(%q) failed: %v", n, err)
		}
	}

	list = store.ListWorkspaces()
	if len(list) != len(names) {
		t.Errorf("ListWorkspaces() len = %d, want %d", len(list), len(names))
	}
}

func TestPersistenceAcrossInstances(t *testing.T) {
	dir := t.TempDir()

	store1, _ := New(dir)
	if err := store1.AddWorkspace(sampleWorkspace("persist-me")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	// Verify the state file was written.
	stateFile := filepath.Join(dir, ".opentree", "state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// New instance reads from the same file.
	store2, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ws, err := store2.GetWorkspace("persist-me")
	if err != nil {
		t.Fatalf("GetWorkspace() after reload failed: %v", err)
	}
	if ws.Agent != "opencode" {
		t.Errorf("Agent = %q, want %q", ws.Agent, "opencode")
	}
}

func TestFanoutGroup_RoundTripsAndOmitsWhenEmpty(t *testing.T) {
	dir := t.TempDir()

	store1, _ := New(dir)
	grouped := sampleWorkspace("feat/x-claude")
	grouped.FanoutGroup = "feat/x"
	if err := store1.AddWorkspace(grouped); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}
	if err := store1.AddWorkspace(sampleWorkspace("loner")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	store2, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	ws, err := store2.GetWorkspace("feat/x-claude")
	if err != nil {
		t.Fatalf("GetWorkspace() after reload failed: %v", err)
	}
	if ws.FanoutGroup != "feat/x" {
		t.Errorf("FanoutGroup = %q, want %q", ws.FanoutGroup, "feat/x")
	}

	// The empty value is every pre-fanout workspace; it must not start
	// appearing in their records just because the field now exists.
	raw, err := os.ReadFile(filepath.Join(dir, ".opentree", "state.json"))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	if got := strings.Count(string(raw), `"fanout_group"`); got != 1 {
		t.Errorf("state file mentions fanout_group %d times, want once (grouped workspace only)", got)
	}
}

func TestAddWorkspace_OverwritesExisting(t *testing.T) {
	store := newTestStore(t)
	ws := sampleWorkspace("overwrite")
	if err := store.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace() first call failed: %v", err)
	}

	ws.Status = "stopped"
	if err := store.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace() second call failed: %v", err)
	}

	got, err := store.GetWorkspace("overwrite")
	if err != nil {
		t.Fatalf("GetWorkspace() failed: %v", err)
	}
	if got.Status != "stopped" {
		t.Errorf("Status = %q, want %q", got.Status, "stopped")
	}
	if len(store.ListWorkspaces()) != 1 {
		t.Errorf("expected 1 workspace after overwrite, got %d", len(store.ListWorkspaces()))
	}
}

func TestConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	const numWriters = 10

	var wg sync.WaitGroup
	errs := make(chan error, numWriters)

	for i := range numWriters {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// Each goroutine gets its own Store instance (simulates separate processes).
			store, err := New(dir)
			if err != nil {
				errs <- fmt.Errorf("writer %d: New() failed: %w", id, err)
				return
			}
			ws := sampleWorkspace(fmt.Sprintf("concurrent-%d", id))
			if err := store.AddWorkspace(ws); err != nil {
				errs <- fmt.Errorf("writer %d: AddWorkspace() failed: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}

	// Verify: reload from disk and check all workspaces survived.
	final, err := New(dir)
	if err != nil {
		t.Fatalf("final New() failed: %v", err)
	}
	if got := len(final.ListWorkspaces()); got != numWriters {
		t.Errorf("expected %d workspaces after concurrent writes, got %d", numWriters, got)
	}
}

func TestMutate_DoesNotResurrectDeletedWorkspace(t *testing.T) {
	dir := t.TempDir()

	s1, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if err := s1.AddWorkspace(sampleWorkspace("keep")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}
	if err := s1.AddWorkspace(sampleWorkspace("victim")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	// Second store instance (a separate process in real usage) loads both.
	s2, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Process 1 deletes "victim" on disk.
	if err := s1.DeleteWorkspace("victim"); err != nil {
		t.Fatalf("DeleteWorkspace() failed: %v", err)
	}

	// Process 2 performs an unrelated mutation; it must not write "victim" back.
	if err := s2.AddWorkspace(sampleWorkspace("extra")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	final, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if _, err := final.GetWorkspace("victim"); err == nil {
		t.Error("deleted workspace was resurrected by a concurrent store's write")
	}
	for _, name := range []string{"keep", "extra"} {
		if _, err := final.GetWorkspace(name); err != nil {
			t.Errorf("GetWorkspace(%q) failed: %v", name, err)
		}
	}
}

func TestGetWorkspace_And_List_ReturnCopies(t *testing.T) {
	store := newTestStore(t)
	if err := store.AddWorkspace(sampleWorkspace("iso")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	got, err := store.GetWorkspace("iso")
	if err != nil {
		t.Fatalf("GetWorkspace() failed: %v", err)
	}
	got.Status = "mutated"

	again, err := store.GetWorkspace("iso")
	if err != nil {
		t.Fatalf("GetWorkspace() failed: %v", err)
	}
	if again.Status == "mutated" {
		t.Error("GetWorkspace() returned a live pointer into the store")
	}

	store.ListWorkspaces()[0].Status = "mutated-via-list"
	again, err = store.GetWorkspace("iso")
	if err != nil {
		t.Fatalf("GetWorkspace() failed: %v", err)
	}
	if again.Status == "mutated-via-list" {
		t.Error("ListWorkspaces() returned live pointers into the store")
	}
}

func TestAtomicWrite_NoPartialReads(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Seed with initial workspace.
	if err := store.AddWorkspace(sampleWorkspace("seed")); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	const iterations = 50
	var wg sync.WaitGroup
	errs := make(chan error, iterations*2)

	// Writer: continuously adds workspaces.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range iterations {
			s, err := New(dir)
			if err != nil {
				errs <- fmt.Errorf("writer iteration %d: New() failed: %w", i, err)
				return
			}
			ws := sampleWorkspace(fmt.Sprintf("w-%d", i))
			if err := s.AddWorkspace(ws); err != nil {
				errs <- fmt.Errorf("writer iteration %d: AddWorkspace() failed: %w", i, err)
				return
			}
		}
	}()

	// Reader: continuously reloads and verifies valid state.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range iterations {
			s, err := New(dir)
			if err != nil {
				errs <- fmt.Errorf("reader iteration %d: New() failed (partial/corrupt JSON?): %w", i, err)
				return
			}
			// Verify state is non-empty (at least the seed workspace should exist).
			if len(s.ListWorkspaces()) == 0 {
				errs <- fmt.Errorf("reader iteration %d: got 0 workspaces (expected >= 1)", i)
				return
			}
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
}

// Regression: a zero-byte state.json (crashed writer, stray touch) used to
// fail JSON parsing and brick every opentree command.
func TestNew_EmptyStateFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".opentree"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".opentree", "state.json"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() with empty state file: %v", err)
	}
	if got := len(store.ListWorkspaces()); got != 0 {
		t.Errorf("ListWorkspaces() = %d entries, want 0", got)
	}
}

// Regression: a JSON null workspace entry (hand edit, merge-conflict
// resolution) used to panic on the first dereference.
func TestNew_NullWorkspaceEntrySkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".opentree"), 0755); err != nil {
		t.Fatal(err)
	}
	content := `{"workspaces": {"broken": null, "ok": {"name": "ok", "branch": "ok"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".opentree", "state.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	list := store.ListWorkspaces() // used to panic here
	if len(list) != 1 || list[0].Name != "ok" {
		t.Errorf("ListWorkspaces() = %+v, want just the ok workspace", list)
	}
	if _, err := store.GetWorkspace("broken"); err == nil {
		t.Error("GetWorkspace(broken) should report not-found, not panic")
	}
}

// A corrupt state file should fail with a recovery hint, not a bare JSON error.
func TestNew_CorruptStateFileHasRecoveryHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".opentree"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".opentree", "state.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := New(dir)
	if err == nil {
		t.Fatal("expected error for corrupt state file")
	}
	if !strings.Contains(err.Error(), "delete it to reset") {
		t.Errorf("error = %q, want a recovery hint", err)
	}
}

// ---------------------------------------------------------------------------
// The session ledger
// ---------------------------------------------------------------------------

// RecordSession is called several times for the same conversation: once when it
// is created, again once somebody has said something to it. The later call must
// not erase what the earlier one knew, and vice versa.
func TestRecordSession_UpsertsWithoutErasing(t *testing.T) {
	ws := &Workspace{Name: "fix-auth"}
	when := time.Now()

	ws.RecordSession(ACPSession{Agent: "claude", ID: "ses_a"})
	ws.RecordSession(ACPSession{ID: "ses_a", Title: "the auth bug", UpdatedAt: when})

	if len(ws.ACPSessions) != 1 {
		t.Fatalf("ACPSessions = %+v, want one conversation, not two", ws.ACPSessions)
	}
	got := ws.ACPSessions[0]
	if got.Title != "the auth bug" || got.UpdatedAt.IsZero() {
		t.Errorf("session = %+v, want the title and time added", got)
	}
	if got.Agent != "claude" {
		t.Errorf("Agent = %q, want the one recorded when it was created", got.Agent)
	}

	// A later call with nothing new says nothing, rather than blanking it.
	ws.RecordSession(ACPSession{ID: "ses_a"})
	if ws.ACPSessions[0].Title != "the auth bug" {
		t.Errorf("Title = %q, want it kept", ws.ACPSessions[0].Title)
	}
}

// An agent discards a session that was opened and never used. Keeping the id
// would offer a conversation that resumes into "not found", which reads as one
// that was lost rather than one nobody ever had.
func TestForgetSession_FallsBackToTheOneBefore(t *testing.T) {
	ws := &Workspace{Name: "fix-auth"}
	ws.RecordSession(ACPSession{ID: "ses_a", Title: "the auth bug"})
	ws.RecordSession(ACPSession{ID: "ses_b"})

	ws.ForgetSession("ses_b")
	if len(ws.ACPSessions) != 1 || ws.ACPSessions[0].ID != "ses_a" {
		t.Fatalf("ACPSessions = %+v, want only the conversation that happened", ws.ACPSessions)
	}
	if ws.ACPSessionID != "ses_a" {
		t.Errorf("ACPSessionID = %q, want the real conversation before it", ws.ACPSessionID)
	}

	// The last one out leaves the workspace with nothing to resume, which is
	// true and better than a dangling id.
	ws.ForgetSession("ses_a")
	if ws.ACPSessionID != "" || len(ws.ACPSessions) != 0 {
		t.Errorf("ledger = %q %+v, want it empty", ws.ACPSessionID, ws.ACPSessions)
	}

	ws.ForgetSession("") // no id is a no-op, not a wipe
}

// Whichever conversation was recorded last is the one this workspace is in, so
// closing the window and coming back lands where it left off.
func TestRecordSession_TracksTheCurrentOne(t *testing.T) {
	ws := &Workspace{Name: "fix-auth"}
	ws.RecordSession(ACPSession{ID: "ses_a"})
	ws.RecordSession(ACPSession{ID: "ses_b"})

	if ws.ACPSessionID != "ses_b" {
		t.Errorf("ACPSessionID = %q, want the one just opened", ws.ACPSessionID)
	}
	if len(ws.ACPSessions) != 2 {
		t.Errorf("ACPSessions = %+v, want both remembered", ws.ACPSessions)
	}
	// An id is the only thing worth recording; a call without one is a no-op
	// rather than an empty row in the ledger.
	ws.RecordSession(ACPSession{Title: "no id"})
	if len(ws.ACPSessions) != 2 || ws.ACPSessionID != "ses_b" {
		t.Errorf("ACPSessions = %+v, ACPSessionID = %q, want both untouched", ws.ACPSessions, ws.ACPSessionID)
	}
}

func TestRecordSession_DropsTheOldestPastTheCap(t *testing.T) {
	ws := &Workspace{Name: "fix-auth"}
	for i := range maxRecordedSessions + 5 {
		ws.RecordSession(ACPSession{ID: fmt.Sprintf("ses_%02d", i)})
	}

	if len(ws.ACPSessions) != maxRecordedSessions {
		t.Fatalf("ACPSessions = %d, want the cap of %d", len(ws.ACPSessions), maxRecordedSessions)
	}
	if ws.ACPSessions[0].ID != "ses_05" {
		t.Errorf("oldest kept = %q, want the first five dropped", ws.ACPSessions[0].ID)
	}
}

// The ledger has to survive a round trip, or /resume would only ever offer the
// conversations from this run.
func TestRecordSession_Persists(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	ws := &Workspace{Name: "fix-auth", Branch: "fix-auth", WorktreeDir: dir}
	if err := store.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace(): %v", err)
	}
	if err := store.Update("fix-auth", func(w *Workspace) error {
		w.RecordSession(ACPSession{Agent: "opencode", ID: "ses_a", Title: "the auth bug"})
		return nil
	}); err != nil {
		t.Fatalf("Update(): %v", err)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	got, err := reopened.GetWorkspace("fix-auth")
	if err != nil {
		t.Fatalf("GetWorkspace(): %v", err)
	}
	if len(got.ACPSessions) != 1 || got.ACPSessions[0].Title != "the auth bug" {
		t.Errorf("ACPSessions = %+v, want the recorded conversation back", got.ACPSessions)
	}
}

// ---------------------------------------------------------------------------
// The file on disk: its schema, and its permissions
// ---------------------------------------------------------------------------

// writeStateFile puts raw JSON where a Store opened on dir will find it.
func writeStateFile(t *testing.T, dir, content string, perm os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".opentree"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".opentree", "state.json")
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	return path
}

// Regression: a newer opentree writes fields this binary has no struct field
// for. The decoder dropped them and the next mutation rewrote the whole file,
// so one command from an older binary left on PATH deleted them for good — and
// a session id, a setup hash or a port has nothing to re-derive it from. The
// three levels are all tested because the file nests: the document, a
// workspace record, and an entry in its session ledger.
func TestMutate_KeepsFieldsThisBinaryDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	path := writeStateFile(t, dir, `{
  "version": 1,
  "telemetry_opt_in": true,
  "workspaces": {
    "fix-auth": {
      "name": "fix-auth",
      "branch": "fix-auth",
      "review_state": {"approved_by": "kim"},
      "acp_sessions": [{"id": "ses_a", "model": "sonnet-9"}]
    }
  }
}`, 0600)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := store.Update("fix-auth", func(ws *Workspace) error {
		ws.Port = 3001
		return nil
	}); err != nil {
		t.Fatalf("Update(): %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		TelemetryOptIn bool `json:"telemetry_opt_in"`
		Workspaces     map[string]struct {
			Port        int `json:"port"`
			ReviewState struct {
				ApprovedBy string `json:"approved_by"`
			} `json:"review_state"`
			ACPSessions []struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"acp_sessions"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the rewritten file is not valid JSON (%v):\n%s", err, raw)
	}

	if !got.TelemetryOptIn {
		t.Errorf("the document's own telemetry_opt_in was dropped:\n%s", raw)
	}
	ws := got.Workspaces["fix-auth"]
	if ws.ReviewState.ApprovedBy != "kim" {
		t.Errorf("the workspace's review_state was dropped:\n%s", raw)
	}
	if len(ws.ACPSessions) != 1 || ws.ACPSessions[0].Model != "sonnet-9" {
		t.Errorf("the session's model was dropped:\n%s", raw)
	}
	if ws.ACPSessions[0].ID != "ses_a" {
		t.Errorf("session id = %q, want the one that was already there", ws.ACPSessions[0].ID)
	}
	if ws.Port != 3001 {
		t.Errorf("port = %d, want this binary's own write to have landed as well", ws.Port)
	}
}

// A workspace re-added under a name that is already taken keeps whatever a
// newer opentree wrote against it. The caller's struct was built from nothing
// and cannot carry those fields itself.
func TestAddWorkspace_OverwriteKeepsTheFieldsItCannotSee(t *testing.T) {
	dir := t.TempDir()
	path := writeStateFile(t, dir, `{"workspaces": {"alpha": {"name": "alpha", "review_state": "approved"}}}`, 0600)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := store.AddWorkspace(sampleWorkspace("alpha")); err != nil {
		t.Fatalf("AddWorkspace(): %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "review_state") {
		t.Errorf("re-adding the workspace dropped review_state:\n%s", raw)
	}
}

// The schema this binary speaks is stamped on every write, so a newer one can
// tell what wrote the file. Every state.json in existence predates the field,
// and those load as version 0 without complaint.
func TestAtomicWrite_StampsTheSchemaOnAVersionlessFile(t *testing.T) {
	dir := t.TempDir()
	path := writeStateFile(t, dir, `{"workspaces": {"alpha": {"name": "alpha"}}}`, 0600)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() on a file from before the version field: %v", err)
	}
	if _, err := store.GetWorkspace("alpha"); err != nil {
		t.Fatalf("GetWorkspace(): %v", err)
	}
	if err := store.AddWorkspace(sampleWorkspace("beta")); err != nil {
		t.Fatalf("AddWorkspace(): %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != stateVersion {
		t.Errorf("version on disk = %d, want %d", got.Version, stateVersion)
	}
}

// A schema this binary has never heard of is one whose known fields may have
// changed meaning, so opening the file would mean writing it back mangled. It
// is refused instead, while the opentree that wrote it can still read it — and
// the error has to name both ways out, since a user who does not want the
// upgrade needs the other one.
func TestNew_RefusesAStateFileFromTheFuture(t *testing.T) {
	dir := t.TempDir()
	writeStateFile(t, dir, `{"version": 99, "workspaces": {}}`, 0600)

	_, err := New(dir)
	if err == nil {
		t.Fatal("New() opened a state file written by a newer opentree")
	}
	for _, want := range []string{"newer opentree", "upgrade opentree", "delete it to reset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// state.json holds session ids, PR URLs, issue titles and branch names — the
// same class of thing as the trust file, which has always been 0600. On a
// shared host 0644 handed all of it to every other account, and the lock beside
// it is no use to anyone who cannot read what it guards.
func TestAtomicWrite_TheStateFileAndItsLockArePrivate(t *testing.T) {
	dir := t.TempDir()
	// An 0644 file left by an older opentree: the rename installs a private
	// one over it, so the first mutating command repairs it.
	path := writeStateFile(t, dir, `{"workspaces": {}}`, 0644)

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := store.AddWorkspace(sampleWorkspace("alpha")); err != nil {
		t.Fatalf("AddWorkspace(): %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("state.json mode = %o, want 0600", perm)
	}

	lock, err := os.Stat(filepath.Join(dir, ".opentree", "state.lock"))
	if err != nil {
		t.Fatalf("stat state.lock: %v", err)
	}
	if perm := lock.Mode().Perm(); perm&0077 != 0 {
		t.Errorf("state.lock mode = %o, want nothing for group or other", perm)
	}
}
