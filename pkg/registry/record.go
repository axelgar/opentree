package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// recordFile is the install's record, beside what it describes — the same
// pattern the plugins store and the skills publisher installs use. It is
// written last, so its presence is the install's commit marker: a directory
// without one is a stage that died, not an agent.
const recordFile = "install.json"

// Record is everything the loader needs to put an installed agent back into
// the runtime registry without the network: the entry as it was installed
// (not as the index says today — update is the command that moves it), where
// it came from, and the resolved launch line. Command is absolute because the
// install lives under ~/.opentree, where no PATH lookup will find it.
type Record struct {
	Entry       Entry             `json:"entry"`
	IndexURL    string            `json:"index_url"`
	Platform    string            `json:"platform,omitempty"` // binary installs only
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	InstalledAt time.Time         `json:"installed_at"`

	// Dir is where the record was loaded from — derived, not persisted, so a
	// store moved wholesale (a restored backup, a renamed home) keeps working.
	Dir string `json:"-"`
}

// readRecord loads and checks one install's record. The entry inside is
// validated again on the way in: the file is opentree's own, but "opentree
// wrote it" is a claim about the past, and the id is about to become a
// command the chat executes.
func readRecord(dir string) (Record, error) {
	data, err := os.ReadFile(filepath.Join(dir, recordFile)) // #nosec G304 -- opentree's own registry store, under the user's home
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("unreadable %s: %w", recordFile, err)
	}
	if err := r.Entry.Validate(); err != nil {
		return Record{}, err
	}
	if !filepath.IsAbs(r.Command) {
		return Record{}, fmt.Errorf("%s: recorded command %q is not absolute", r.Entry.ID, r.Command)
	}
	if r.Entry.ID != filepath.Base(dir) {
		return Record{}, fmt.Errorf("record inside %s names %q", filepath.Base(dir), r.Entry.ID)
	}
	r.Dir = dir
	return r, nil
}

// Installed is every agent the store holds, sorted by id, with a problem
// line for each directory that would not load. A broken install still being
// named is the point — an invisible broken install is one nobody can remove.
func Installed() ([]Record, []string) {
	dir := agentsDir()
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A store that does not exist yet is empty, not broken.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("cannot read %s: %v", dir, err)}
	}
	var records []Record
	var problems []string
	for _, e := range entries {
		// Dotted entries are staging directories: an install in flight, or one
		// that died mid-stage. Skipped, the way the plugins store skips its own.
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		r, err := readRecord(filepath.Join(dir, e.Name()))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v — `opentree agents remove %s` clears it", e.Name(), err, e.Name()))
			continue
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Entry.ID < records[j].Entry.ID })
	return records, problems
}
