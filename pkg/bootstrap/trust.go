package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/axelgar/opentree/pkg/fsutil"
)

// opentree.toml is tracked in git, which is the whole reason a bootstrap
// sequence is worth having: a project describes how to prepare a worktree once,
// and everyone who clones it gets that. It also means setup and run are
// executable code that arrives with a clone, from whoever last had commit
// rights — so they run once this machine has said they may.
//
// The gate covers setup and run together. run is code from the same tracked
// file, and gating only setup would move a hostile payload one key down.
//
// The record lives here, not in the repository: a repository cannot vouch for
// itself. Approval is per machine, per repository, and per exact text — edit a
// command and it is a new thing to approve, which is also what makes an edited
// setup re-run rather than stay stale forever.

// Approval is one recorded "yes" and what it was for.
type Approval struct {
	Hash       string    `json:"hash"`
	ApprovedAt time.Time `json:"approved_at"`
	// Setup and Run are kept in plain text so the file answers the question a
	// user actually asks of it — what did I agree to? — without needing
	// opentree to read it back to them.
	Setup []string `json:"setup,omitempty"`
	Run   string   `json:"run,omitempty"`
}

// trustFile is the on-disk shape: repository path to that repository's
// approvals, newest first.
type trustFile struct {
	Repos map[string][]Approval `json:"repos"`
}

// TrustPath is where this machine records what it has approved, beside the
// agent adapters opentree installs for itself.
func TrustPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".opentree", "trust.json")
}

// Hash identifies one setup+run block exactly.
//
// Exact rather than normalised: whitespace inside a shell command changes what
// it does often enough that "close enough" is not a judgement opentree should
// make on the user's behalf.
//
// The same hash records what ran (state.Workspace.SetupHash) as gates whether
// it may. One identity for one block, so the two can never disagree about
// which version of the setup this workspace has.
func Hash(setup []string, run string) string {
	if setup == nil {
		setup = []string{}
	}
	// JSON rather than joining on a separator: a command may contain any
	// character, and a length-delimited encoding cannot be confused by one.
	payload, err := json.Marshal(struct {
		Setup []string `json:"setup"`
		Run   string   `json:"run"`
	}{setup, run})
	if err != nil {
		// Marshalling strings cannot fail; a hash of nothing is still not a
		// hash that matches anything recorded.
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Executable reports whether the project asks to run anything at all. An empty
// block is not a thing to trust — there is nothing in it to run.
func Executable(setup []string, run string) bool {
	return len(setup) > 0 || run != ""
}

// Trusted reports whether this machine has approved what the project now asks
// to run in repoRoot.
//
// Fails closed. An unreadable or corrupt trust file means "not approved", which
// costs the user one prompt; the alternative reading of a damaged file is to
// run whatever the repository says, which costs rather more.
func Trusted(repoRoot string, setup []string, run string) bool {
	if !Executable(setup, run) {
		return true // nothing to run, nothing to approve
	}
	file, err := loadTrust()
	if err != nil {
		return false
	}
	want := Hash(setup, run)
	for _, a := range file.Repos[trustKey(repoRoot)] {
		if a.Hash == want {
			return true
		}
	}
	return false
}

// Approve records this machine's approval of what the project now asks to run.
//
// Earlier approvals for the same repository are kept. A user who approved main
// and then a branch that edits setup has read both, and switching back should
// not ask again about the text they already agreed to.
func Approve(repoRoot string, setup []string, run string) error {
	if !Executable(setup, run) {
		return fmt.Errorf("nothing to trust: [workspace] names no setup or run command")
	}
	path := TrustPath()
	if path == "" {
		return fmt.Errorf("could not determine the trust file path: home directory not found")
	}
	// Loaded rather than rewritten from scratch: this file holds every other
	// repository's approvals too, and a parse error must not cost them.
	file, err := loadTrust()
	if err != nil {
		return fmt.Errorf("refusing to overwrite %s: %w", path, err)
	}

	key := trustKey(repoRoot)
	hash := Hash(setup, run)
	kept := make([]Approval, 0, len(file.Repos[key])+1)
	kept = append(kept, Approval{
		Hash:       hash,
		ApprovedAt: time.Now(),
		Setup:      setup,
		Run:        run,
	})
	for _, a := range file.Repos[key] {
		if a.Hash != hash {
			kept = append(kept, a) // re-approving moves it to the front
		}
	}
	if file.Repos == nil {
		file.Repos = map[string][]Approval{}
	}
	file.Repos[key] = kept

	return writeTrust(path, file)
}

// Revoke drops every approval recorded for a repository, so the next setup asks
// again. The way back from "I should not have said yes to that".
func Revoke(repoRoot string) (bool, error) {
	path := TrustPath()
	if path == "" {
		return false, fmt.Errorf("could not determine the trust file path: home directory not found")
	}
	file, err := loadTrust()
	if err != nil {
		return false, fmt.Errorf("refusing to overwrite %s: %w", path, err)
	}
	key := trustKey(repoRoot)
	if _, ok := file.Repos[key]; !ok {
		return false, nil
	}
	delete(file.Repos, key)
	return true, writeTrust(path, file)
}

// Approvals is what this machine has approved for a repository, newest first.
func Approvals(repoRoot string) []Approval {
	file, err := loadTrust()
	if err != nil {
		return nil
	}
	return file.Repos[trustKey(repoRoot)]
}

// trustKey identifies a repository by its resolved path. A repository that
// moves loses its approvals and is asked again — the safe direction, and the
// only one available without asking git for an identity a fresh clone would
// share with the repository it was cloned from.
func trustKey(repoRoot string) string {
	return realpath(repoRoot)
}

// loadTrust reads the trust file. Absent is not an error: nobody has approved
// anything yet.
func loadTrust() (trustFile, error) {
	file := trustFile{Repos: map[string][]Approval{}}
	path := TrustPath()
	if path == "" {
		return file, fmt.Errorf("home directory not found")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- opentree's own file, under the user's home
	if err != nil {
		if os.IsNotExist(err) {
			return file, nil
		}
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return trustFile{Repos: map[string][]Approval{}}, fmt.Errorf("%s is not readable as JSON: %w", path, err)
	}
	if file.Repos == nil {
		file.Repos = map[string][]Approval{}
	}
	return file, nil
}

func writeTrust(path string, file trustFile) error {
	// Newest first, and indented: this is a file people read to check what they
	// agreed to, and one whose diff should mean something.
	for key := range file.Repos {
		sort.SliceStable(file.Repos[key], func(i, j int) bool {
			return file.Repos[key][i].ApprovedAt.After(file.Repos[key][j].ApprovedAt)
		})
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, append(data, '\n'))
}
