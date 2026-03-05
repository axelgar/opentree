package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Store manages workspace state persistence
type Store struct {
	filePath string
	state    *State
}

// State represents the persisted state
type State struct {
	Workspaces map[string]*Workspace `json:"workspaces"`
}

// Workspace represents a workspace's metadata
type Workspace struct {
	Name       string    `json:"name"`
	Branch     string    `json:"branch"`
	BaseBranch string    `json:"base_branch"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status"` // active, idle, stopped
	Agent      string    `json:"agent"`
	WorktreeDir string   `json:"worktree_dir"`
}

// New creates a new state store
func New(repoRoot string) (*Store, error) {
	stateFile := filepath.Join(repoRoot, ".opentree", "state.json")
	
	store := &Store{
		filePath: stateFile,
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

// Load reads the state from disk
func (s *Store) Load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	
	return json.Unmarshal(data, s.state)
}

// Save writes the state to disk
func (s *Store) Save() error {
	// Ensure .opentree directory exists
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(s.filePath, data, 0644)
}

// AddWorkspace adds a new workspace to the state
func (s *Store) AddWorkspace(ws *Workspace) error {
	s.state.Workspaces[ws.Name] = ws
	return s.Save()
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
	if _, ok := s.state.Workspaces[ws.Name]; !ok {
		return fmt.Errorf("workspace not found: %s", ws.Name)
	}
	s.state.Workspaces[ws.Name] = ws
	return s.Save()
}

// DeleteWorkspace removes a workspace from the state
func (s *Store) DeleteWorkspace(name string) error {
	delete(s.state.Workspaces, name)
	return s.Save()
}

// ListWorkspaces returns all workspaces
func (s *Store) ListWorkspaces() []*Workspace {
	workspaces := make([]*Workspace, 0, len(s.state.Workspaces))
	for _, ws := range s.state.Workspaces {
		workspaces = append(workspaces, ws)
	}
	return workspaces
}
