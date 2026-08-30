package registry

import (
	"fmt"
	"os"
	"path/filepath"
)

// PlanUpdate is the staged shape of an update: the same install as a fresh
// add, built in a dotted sibling directory beside the live install and
// swapped in only once it is complete — so a failed update leaves the old
// agent working, and every workspace pointing at it keeps a command that
// runs. The consent prompt shows the staging path because that is where the
// command really runs; printing the prettier final path would be a summary,
// and summaries are what the prompt exists to avoid.
func PlanUpdate(e Entry, indexURL string) (Plan, error) {
	p, err := NewPlan(e, indexURL)
	if err != nil {
		return Plan{}, err
	}
	p.finalDir = p.Dir
	p.Dir = filepath.Join(agentsDir(), ".update-"+e.ID)
	if p.Kind == "npm" {
		p.NpmArgv = npmArgv(filepath.Join(p.Dir, "npm"), packageSpec(*e.Distribution.Npx, e.Version))
	}
	return p, nil
}

// swap moves a completed staging build into place: the live install steps
// aside, the new one takes its name, and only then is the old removed. A
// rename failing halfway puts the old install back — at every moment there
// is either the old agent or the new one under the id, never neither.
func (p Plan) swap(rec Record) (Record, error) {
	aside := filepath.Join(agentsDir(), ".old-"+p.Entry.ID)
	_ = os.RemoveAll(aside)

	hadOld := true
	if err := os.Rename(p.finalDir, aside); err != nil {
		if !os.IsNotExist(err) {
			return Record{}, err
		}
		hadOld = false
	}
	if err := os.Rename(p.Dir, p.finalDir); err != nil {
		if hadOld {
			_ = os.Rename(aside, p.finalDir)
		}
		return Record{}, err
	}

	// The record was written against the staging path; the command moved
	// with the rename, so the record follows it. npm's shims are relative
	// symlinks into their own prefix and survive the move untouched.
	rel, err := filepath.Rel(p.Dir, rec.Command)
	if err != nil {
		return Record{}, err
	}
	rec.Command = filepath.Join(p.finalDir, rel)
	if err := writeRecord(p.finalDir, rec); err != nil {
		return Record{}, fmt.Errorf("the update is in place but its record is not: %w", err)
	}
	rec.Dir = p.finalDir
	_ = os.RemoveAll(aside)
	return rec, nil
}
