package state

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestUpdateWorkspace(t *testing.T) {
	store := newTestStore(t)
	ws := sampleWorkspace("gamma")

	if err := store.AddWorkspace(ws); err != nil {
		t.Fatalf("AddWorkspace() failed: %v", err)
	}

	ws.Status = "idle"
	ws.PRURL = "https://github.com/example/repo/pull/1"
	ws.PRStatus = "open"

	if err := store.UpdateWorkspace(ws); err != nil {
		t.Fatalf("UpdateWorkspace() failed: %v", err)
	}

	got, err := store.GetWorkspace("gamma")
	if err != nil {
		t.Fatalf("GetWorkspace() after update failed: %v", err)
	}
	if got.Status != "idle" {
		t.Errorf("Status after update = %q, want %q", got.Status, "idle")
	}
	if got.PRURL != ws.PRURL {
		t.Errorf("PRURL = %q, want %q", got.PRURL, ws.PRURL)
	}
	if got.PRStatus != ws.PRStatus {
		t.Errorf("PRStatus = %q, want %q", got.PRStatus, ws.PRStatus)
	}
}

func TestUpdateWorkspace_NotFound(t *testing.T) {
	store := newTestStore(t)
	ws := sampleWorkspace("ghost")

	err := store.UpdateWorkspace(ws)
	if err == nil {
		t.Fatal("UpdateWorkspace() expected error for non-existent workspace, got nil")
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

	for i := 0; i < numWriters; i++ {
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
		for i := 0; i < iterations; i++ {
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
		for i := 0; i < iterations; i++ {
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
