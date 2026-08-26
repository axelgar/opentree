package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/axelgar/opentree/pkg/fsutil"
)

// Store manages workspace state persistence
type Store struct {
	filePath string
	lockPath string
	mu       sync.RWMutex // protects in-memory state access
	state    *State
}

// stateVersion is the schema this binary writes, and the highest one it will
// read.
//
// It is deliberately not bumped when a field is added. Keys this binary has no
// field for survive a round trip — see the passthrough below — so a binary that
// predates a field carries it through untouched instead of deleting it, which
// is what a downgrade used to do to session ids, setup hashes and dev-server
// ports. An additive change therefore needs no version at all.
//
// Bump it only for a change an older binary cannot carry through: a field whose
// meaning changes, or one that goes away. That convention is what earns the
// refusal in loadFromDisk. Turning away a file from the future is a heavy thing
// to do — a stale `go install` copy still on PATH stops working — but by the
// rule above the file it turns away is one it would otherwise rewrite into
// something the newer binary no longer understands, and the message says which
// ways out there are. Warning and carrying on was the other option, and it
// trades a loud failure the user can act on for a quiet one they discover
// afterwards.
const stateVersion = 1

// State represents the persisted state
type State struct {
	// Version is the schema the file was last written under. Zero is every
	// file written before the field existed, and that is exactly the schema
	// version 1 describes, so those load without ceremony.
	Version    int                   `json:"version"`
	Workspaces map[string]*Workspace `json:"workspaces"`

	unknown map[string]json.RawMessage
}

// Workspace represents a workspace's metadata
type Workspace struct {
	Name           string    `json:"name"`
	Branch         string    `json:"branch"`
	BaseBranch     string    `json:"base_branch"`
	CreatedAt      time.Time `json:"created_at"`
	Status         string    `json:"status"` // active, idle, stopped
	Agent          string    `json:"agent"`
	WorktreeDir    string    `json:"worktree_dir"`
	PRURL          string    `json:"pr_url,omitempty"`
	PRStatus       string    `json:"pr_status,omitempty"` // "open", "merged", "closed"
	IssueNumber    int       `json:"issue_number,omitempty"`
	IssueTitle     string    `json:"issue_title,omitempty"`
	ACPSessionID   string    `json:"acp_session_id,omitempty"` // resumable agent conversation
	BranchPushed   bool      `json:"branch_pushed,omitempty"`
	MergeConflicts bool      `json:"merge_conflicts,omitempty"`
	RemoteDeleted  bool      `json:"remote_deleted,omitempty"`

	// AdoptedBranch is a branch opentree found rather than made: `opentree new`
	// from a branch that already existed locally, checked out into a worktree.
	// Deleting the workspace deletes the branch, which is right for a branch
	// this tool created and wrong for one it borrowed.
	//
	// Stated this way round on purpose. The zero value is every workspace
	// written before the field existed, and false means "delete it", which is
	// what those workspaces have always done — the opposite spelling would
	// strand every one of them with an undeletable branch and block recreating
	// a workspace under the same name.
	AdoptedBranch bool `json:"adopted_branch,omitempty"`

	// Autopilot marks a workspace the loop drives toward a green PR: after
	// each agent turn the check command runs, failures go back to the agent,
	// and a pass publishes. Phrased so the zero value is every workspace
	// written before the field existed — off, which is what they were — the
	// same polarity argument AdoptedBranch makes.
	Autopilot bool `json:"autopilot,omitempty"`

	// AutopilotCISha and AutopilotReviewsFp are the loop's watermarks: the
	// head commit whose CI failure was already forwarded to the agent, and the
	// fingerprint of the review-comment set that was. Persisted rather than
	// held in the chat because the poll must not re-send the same failure
	// after a window closes and reopens — and advanced only at the moment a
	// prompt is actually sent, so a chat that dies holding one re-fetches
	// rather than losing it.
	AutopilotCISha     string `json:"autopilot_ci_sha,omitempty"`
	AutopilotReviewsFp string `json:"autopilot_reviews_fp,omitempty"`

	// SetupAt and SetupHash are the project's bootstrap commands, as this
	// worktree last ran them. The hash is what earns the pair its keep: a chat
	// starts many times per workspace — losing a window relaunches one — so
	// without a marker the install would run on every attach, and without the
	// hash an edited setup would stay stale forever.
	SetupAt   time.Time `json:"setup_at,omitempty"`
	SetupHash string    `json:"setup_hash,omitempty"`

	// Port is this workspace's dev server port, given once and kept for the
	// workspace's life. Kept rather than picked per start because an OAuth
	// redirect URI is registered against an exact localhost:PORT, and a port
	// that moved would break every login flow set up against it.
	Port int `json:"port,omitempty"`

	// ACPSessions is every conversation opentree has opened here, oldest first.
	// ACPSessionID is the current one; this is what makes the earlier ones
	// offerable again.
	ACPSessions []ACPSession `json:"acp_sessions,omitempty"`

	// unknown is shared by every copy of the record rather than cloned, which
	// is safe because nothing ever writes to the map — a decode replaces it
	// wholesale, and no caller outside this package can reach it at all.
	unknown map[string]json.RawMessage
}

// ACPSession is one agent conversation this workspace has had.
//
// The agent is recorded with it because a session id is that agent's own
// bookkeeping: handing an opencode id to Claude Code gets a failed load, not
// somebody else's conversation.
type ACPSession struct {
	Agent     string    `json:"agent,omitempty"`
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	unknown map[string]json.RawMessage
}

// maxRecordedSessions caps the ledger.
//
// ponytail: a fixed cap with the oldest dropped. The picker shows the recent
// ones and an agent that keeps its own list is the better source anyway; this
// only has to survive the agent that does not.
const maxRecordedSessions = 20

// RecordSession makes s the workspace's current conversation and remembers it
// among the ones that can be reopened later.
//
// Empty fields do not erase what is already known: the id is recorded the
// moment a session is created, and its title only exists once somebody has
// said something to it.
func (w *Workspace) RecordSession(s ACPSession) {
	if s.ID == "" {
		return
	}
	w.ACPSessionID = s.ID

	for i := range w.ACPSessions {
		if w.ACPSessions[i].ID != s.ID {
			continue
		}
		if s.Title != "" {
			w.ACPSessions[i].Title = s.Title
		}
		if s.Agent != "" {
			w.ACPSessions[i].Agent = s.Agent
		}
		if !s.UpdatedAt.IsZero() {
			w.ACPSessions[i].UpdatedAt = s.UpdatedAt
		}
		return
	}

	w.ACPSessions = append(w.ACPSessions, s)
	if len(w.ACPSessions) > maxRecordedSessions {
		w.ACPSessions = w.ACPSessions[len(w.ACPSessions)-maxRecordedSessions:]
	}
}

// ForgetSession drops a conversation the agent no longer has, so nothing offers
// to reopen it. The workspace falls back to the most recent one left rather than
// to nothing: an agent that discarded an empty session did not discard the real
// conversation before it.
func (w *Workspace) ForgetSession(id string) {
	if id == "" {
		return
	}
	w.ACPSessions = slices.DeleteFunc(w.ACPSessions, func(s ACPSession) bool { return s.ID == id })
	if w.ACPSessionID != id {
		return
	}
	w.ACPSessionID = ""
	if n := len(w.ACPSessions); n > 0 {
		w.ACPSessionID = w.ACPSessions[n-1].ID
	}
}

// The passthrough for fields this binary does not know.
//
// Go's decoder drops object keys it has no field for, and every mutation
// re-marshals the whole file, so without this an older opentree reading a newer
// state.json deletes everything it does not recognise the moment the user runs
// any command that writes. No race is needed — one serialized process is enough
// — and a session id, a setup hash or a dev-server port has nothing to
// re-derive it from, so the loss is permanent.
//
// Each type keeps the keys it did not recognise verbatim and folds them back in
// on the way out. A key this binary does know always wins: the passthrough is
// for carrying a stranger's data, never for shadowing our own.

var (
	stateKeys     = jsonKeys(State{})
	workspaceKeys = jsonKeys(Workspace{})
	sessionKeys   = jsonKeys(ACPSession{})
)

// jsonKeys is the set of object keys a struct marshals to, read off its own
// tags so that adding a field to the struct is all adding a field takes.
func jsonKeys(v any) map[string]struct{} {
	t := reflect.TypeOf(v)
	keys := make(map[string]struct{}, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys[name] = struct{}{}
	}
	return keys
}

// unknownFields picks out the members of a JSON object that known does not name.
func unknownFields(data []byte, known map[string]struct{}) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	for k := range all {
		if _, ok := known[k]; ok {
			delete(all, k)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// marshalWith encodes v and puts the carried keys back beside its own.
//
// It only re-encodes through a map when there is something to put back, so a
// file with no strangers in it keeps the field order the structs declare rather
// than being alphabetised for everybody by a case almost nobody hits.
func marshalWith(v any, unknown map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil || len(unknown) == 0 {
		return data, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(data, &merged); err != nil {
		return nil, err
	}
	for k, raw := range unknown {
		if _, ours := merged[k]; !ours {
			merged[k] = raw
		}
	}
	return json.Marshal(merged)
}

func (s State) MarshalJSON() ([]byte, error) {
	type plain State // a defined type inherits no methods, so this does not recurse
	return marshalWith(plain(s), s.unknown)
}

func (s *State) UnmarshalJSON(data []byte) error {
	type plain State
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	unknown, err := unknownFields(data, stateKeys)
	if err != nil {
		return err
	}
	*s = State(p)
	s.unknown = unknown
	return nil
}

func (w Workspace) MarshalJSON() ([]byte, error) {
	type plain Workspace
	return marshalWith(plain(w), w.unknown)
}

func (w *Workspace) UnmarshalJSON(data []byte) error {
	type plain Workspace
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	unknown, err := unknownFields(data, workspaceKeys)
	if err != nil {
		return err
	}
	*w = Workspace(p)
	w.unknown = unknown
	return nil
}

func (s ACPSession) MarshalJSON() ([]byte, error) {
	type plain ACPSession
	return marshalWith(plain(s), s.unknown)
}

func (s *ACPSession) UnmarshalJSON(data []byte) error {
	type plain ACPSession
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	unknown, err := unknownFields(data, sessionKeys)
	if err != nil {
		return err
	}
	*s = ACPSession(p)
	s.unknown = unknown
	return nil
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

	// 0600, matching the state file it guards. A checkout shared between two
	// Unix users cannot work once state.json itself is private anyway — the
	// second user would take the lock and then fail on the read — and failing
	// at the lock says "permission denied" against a path they can act on
	// rather than leaving them to infer it from a JSON error. A 0644 lock left
	// by an older release keeps its mode: chmod'ing a file that may belong to
	// somebody else, on every single command, to tighten a file that holds no
	// data, is a worse trade than the one it fixes.
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), lockType); err != nil {
		return fmt.Errorf("failed to acquire file lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

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
	// A zero-byte or blank file (crashed writer, stray touch) is empty
	// state, not a reason to brick every command.
	if len(bytes.TrimSpace(data)) == 0 {
		s.state = &State{Workspaces: make(map[string]*Workspace)}
		return nil
	}
	// Unmarshal into a fresh State and swap: unmarshalling into the existing
	// map would merge keys and resurrect workspaces deleted by other processes.
	fresh := &State{}
	if err := json.Unmarshal(data, fresh); err != nil {
		// %w, not %v: callers that want to tell a syntax error from a type
		// mismatch need errors.As to reach the *json.SyntaxError underneath.
		return fmt.Errorf("state file %s is corrupted (%w) — fix it or delete it to reset workspace tracking (worktrees and branches are not affected)", s.filePath, err)
	}
	// A file from the future is one whose schema changed in a way this binary
	// cannot carry through — see stateVersion for why additive changes never
	// get here. Loading it would mean writing it back mangled, so it is refused
	// while the newer binary that wrote it can still read it.
	if fresh.Version > stateVersion {
		return fmt.Errorf("state file %s was written by a newer opentree (schema %d, this one reads %d) — upgrade opentree, or delete it to reset workspace tracking (worktrees and branches are not affected)", s.filePath, fresh.Version, stateVersion)
	}
	if fresh.Workspaces == nil { // e.g. `{}` or `"workspaces": null` on disk
		fresh.Workspaces = make(map[string]*Workspace)
	}
	// Drop null entries (hand edits, merge-conflict leftovers) instead of
	// panicking on the first dereference.
	for name, ws := range fresh.Workspaces {
		if ws == nil {
			delete(fresh.Workspaces, name)
		}
	}
	s.state = fresh
	return nil
}

// atomicWrite marshals and writes state to disk via temp file + fsync + rename.
// Caller must hold an exclusive lock.
//
// The temp file is named rather than left to os.CreateTemp because
// `.opentree/state.json.tmp` is one of the three names the worktree manager
// refuses to hand to a workspace, and because a crashed writer should leave one
// file the next write truncates rather than a new piece of litter every time.
// The flock WriteAtomicVia asks its callers for is the one mutate already
// holds.
func (s *Store) atomicWrite() error {
	// Every write stamps the schema this binary speaks. Doing it here rather
	// than at load keeps a file that arrived without a version honest until it
	// is actually rewritten under one.
	s.state.Version = stateVersion

	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteAtomicVia(s.filePath, s.filePath+".tmp", data); err != nil {
		return fmt.Errorf("failed to write state file %s: %w", s.filePath, err)
	}
	return nil
}

// mutate performs an atomic read-modify-write cycle under an exclusive lock.
// It reloads the latest state from disk, applies the mutation, then writes back.
// If fn returns an error, nothing is written.
func (s *Store) mutate(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withFileLock(syscall.LOCK_EX, func() error {
		if err := s.loadFromDisk(); err != nil {
			return err
		}
		if err := fn(); err != nil {
			return err
		}
		return s.atomicWrite()
	})
}

// Load reads the state from disk under a shared lock.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withFileLock(syscall.LOCK_SH, func() error {
		return s.loadFromDisk()
	})
}

// AddWorkspace adds a new workspace to the state
func (s *Store) AddWorkspace(ws *Workspace) error {
	return s.mutate(func() error {
		cp := *ws // store a copy so later caller mutations don't alias the map
		// Overwriting a name that is already here: the caller built its struct
		// from nothing, so anything a newer opentree wrote against this record
		// would go out with the old one unless it is carried across.
		if cur, ok := s.state.Workspaces[ws.Name]; ok {
			cp.unknown = cur.unknown
		}
		s.state.Workspaces[ws.Name] = &cp
		return nil
	})
}

// GetWorkspace retrieves a copy of a workspace by name
func (s *Store) GetWorkspace(name string) (*Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws, ok := s.state.Workspaces[name]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", name)
	}
	cp := *ws
	return &cp, nil
}

// Update applies fn to a workspace's stored record and saves the result.
//
// fn is handed the record as it is on disk right now, not as the caller last
// saw it, and it should touch only the fields it means to change. That is the
// whole point of the shape. Handing the store a filled-in struct instead —
// what this replaced — writes back every field as of whenever the caller read
// it, and a caller's copy is always a snapshot taken before mutate reloaded
// the file.
//
// It matters because the dashboard and every `opentree chat` are separate
// processes sharing one state.json. Whole-record writes had them reverting
// each other: the branch and PR fields survived it, since the 30s poll
// re-derives them, but a session id, a setup hash or a port has nothing to
// re-derive it from and was simply gone.
//
// fn runs holding s.mu and the file lock, so it must not call back into the
// Store — ListWorkspaces takes the same mutex and a sync.RWMutex is not
// reentrant. Read what the callback needs before calling Update.
//
// A callback that returns an error writes nothing, and leaves the in-memory
// record as it found it.
func (s *Store) Update(name string, fn func(*Workspace) error) error {
	return s.mutate(func() error {
		cur, ok := s.state.Workspaces[name]
		if !ok {
			return fmt.Errorf("workspace not found: %s", name)
		}
		cp := *cur
		if err := fn(&cp); err != nil {
			return err
		}
		s.state.Workspaces[name] = &cp
		return nil
	})
}

// DeleteWorkspace removes a workspace from the state
func (s *Store) DeleteWorkspace(name string) error {
	return s.mutate(func() error {
		delete(s.state.Workspaces, name)
		return nil
	})
}

// ListWorkspaces returns copies of all workspaces
func (s *Store) ListWorkspaces() []*Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspaces := make([]*Workspace, 0, len(s.state.Workspaces))
	for _, ws := range s.state.Workspaces {
		cp := *ws
		workspaces = append(workspaces, &cp)
	}
	return workspaces
}
