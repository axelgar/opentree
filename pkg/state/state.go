package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Store manages workspace state persistence
type Store struct {
	filePath string
	lockPath string
	state    *State
}

// State represents the persisted state
type State struct {
	Workspaces map[string]*Workspace `json:"workspaces"`
}

// Workspace represents a workspace's metadata
type Workspace struct {
	Name        string    `json:"name"`
	Branch      string    `json:"branch"`
	BaseBranch  string    `json:"base_branch"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"` // active, idle, stopped
	Agent       string    `json:"agent"`
	WorktreeDir string    `json:"worktree_dir"`
	PRURL          string `json:"pr_url,omitempty"`
	PRStatus       string `json:"pr_status,omitempty"` // "open", "merged", "closed"
	IssueNumber    int    `json:"issue_number,omitempty"`
	IssueTitle     string `json:"issue_title,omitempty"`
	BranchPushed   bool   `json:"branch_pushed,omitempty"`
	MergeConflicts bool   `json:"merge_conflicts,omitempty"`
	RemoteDeleted  bool   `json:"remote_deleted,omitempty"`
}

// New creates a new state store
func New(repoRoot string) (*Store, error) {
	opentreeDir := filepath.Join(repoRoot, ".opentree")
	stateFile := filepath.Join(opentreeDir, "state.json")
	lockFile := filepath.Join(opentreeDir, "state.lock")

	store := &Store{
		filePath: stateFile,
		lockPath: lockFile,
		state:    &State{Workspaces: make(map[string]*Workspace)},
	}

	// Load existing state if it exists
	if _, err := os.Stat(stateFile); err == nil {
		if err := store.Load(); err != nil {
			return nil, fmt.Errorf("failed to load state: %w", err)
		}
	}

	return store, nil
}

// withFileLock acquires a file lock (shared or exclusive), runs fn, then releases.
func (s *Store) withFileLock(lockType int, fn func() error) error {
	// Ensure directory exists for the lock file
	if err := os.MkdirAll(filepath.Dir(s.lockPath), 0755); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), lockType); err != nil {
		return fmt.Errorf("failed to acquire file lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}

// loadFromDisk reads and unmarshals the state file without locking.
// Caller must hold the appropriate lock.
func (s *Store) loadFromDisk() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.state = &State{Workspaces: make(map[string]*Workspace)}
			return nil
		}
		return err
	}
	return json.Unmarshal(data, s.state)
}

// atomicWrite marshals and writes state to disk via temp file + rename.
// Caller must hold an exclusive lock.
func (s *Store) atomicWrite() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp state file: %w", err)
	}
	return nil
}

// mutate performs an atomic read-modify-write cycle under an exclusive lock.
// It reloads the latest state from disk, applies the mutation, then writes back.
func (s *Store) mutate(fn func()) error {
	return s.withFileLock(syscall.LOCK_EX, func() error {
		if err := s.loadFromDisk(); err != nil {
			return err
		}
		fn()
		return s.atomicWrite()
	})
}

// Load reads the state from disk under a shared lock.
func (s *Store) Load() error {
	return s.withFileLock(syscall.LOCK_SH, func() error {
		return s.loadFromDisk()
	})
}

// Save writes the state to disk under an exclusive lock using atomic write.
func (s *Store) Save() error {
	return s.withFileLock(syscall.LOCK_EX, func() error {
		return s.atomicWrite()
	})
}

// AddWorkspace adds a new workspace to the state
func (s *Store) AddWorkspace(ws *Workspace) error {
	return s.mutate(func() {
		s.state.Workspaces[ws.Name] = ws
	})
}

// GetWorkspace retrieves a workspace by name
func (s *Store) GetWorkspace(name string) (*Workspace, error) {
	ws, ok := s.state.Workspaces[name]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", name)
	}
	return ws, nil
}

// UpdateWorkspace updates an existing workspace
func (s *Store) UpdateWorkspace(ws *Workspace) error {
	var notFound bool
	err := s.mutate(func() {
		if _, ok := s.state.Workspaces[ws.Name]; !ok {
			notFound = true
			return
		}
		s.state.Workspaces[ws.Name] = ws
	})
	if notFound {
		return fmt.Errorf("workspace not found: %s", ws.Name)
	}
	return err
}

// DeleteWorkspace removes a workspace from the state
func (s *Store) DeleteWorkspace(name string) error {
	return s.mutate(func() {
		delete(s.state.Workspaces, name)
	})
}

// ListWorkspaces returns all workspaces
func (s *Store) ListWorkspaces() []*Workspace {
	workspaces := make([]*Workspace, 0, len(s.state.Workspaces))
	for _, ws := range s.state.Workspaces {
		workspaces = append(workspaces, ws)
	}
	return workspaces
}
