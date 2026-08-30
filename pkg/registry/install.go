package registry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Plan is an install decided but not yet run, split from the running so the
// command line can put the whole thing in front of the user first — the
// consent doctrine the adapter download and the trust gate already follow:
// what will be fetched, from where, and what will run, printed in full.
type Plan struct {
	Entry    Entry
	IndexURL string
	Dir      string // agents/<id>, the directory the install owns

	Kind    string // "npm"
	NpmArgv []string
}

// NewPlan resolves how this entry installs on this machine, preferring npm:
// that path inherits the adapter install's whole security posture — pinned
// spec, private prefix, no install scripts — where a binary archive is
// trusted only as far as its checksum.
func NewPlan(e Entry, indexURL string) (Plan, error) {
	if err := e.Validate(); err != nil {
		return Plan{}, err
	}
	dir := agentsDir()
	if dir == "" {
		return Plan{}, fmt.Errorf("cannot find your home directory")
	}
	p := Plan{Entry: e, IndexURL: indexURL, Dir: filepath.Join(dir, e.ID)}

	if d := e.Distribution.Npx; d != nil {
		p.Kind = "npm"
		p.NpmArgv = npmArgv(filepath.Join(p.Dir, "npm"), packageSpec(*d, e.Version))
		return p, nil
	}
	if len(e.Distribution.Binary) > 0 {
		return Plan{}, fmt.Errorf("%s ships only binary builds, which opentree cannot install yet", e.ID)
	}
	return Plan{}, fmt.Errorf("%s is distributed via uvx, which opentree does not support yet", e.ID)
}

// Describe is the consent body: what the entry is, and exactly what will
// happen. The command and URLs are printed rather than summarised — this
// runs a package manager, or executes a downloaded archive's contents, and
// the parts worth being able to check are the pin, the prefix and the
// source.
func (p Plan) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s (%s, %s)\n", p.Entry.Name, p.Entry.Description, p.Entry.Version, p.Kind)
	fmt.Fprintf(&b, "opentree will run:\n\n  %s\n", strings.Join(p.NpmArgv, " "))
	return b.String()
}

// Run performs the install. The record is written last, as the commit
// marker: a directory holding anything less than a complete install must
// never load, so any failure removes the whole directory rather than
// leaving a stage behind under the agent's name.
func (p Plan) Run(ctx context.Context) (Record, error) {
	if _, err := os.Lstat(p.Dir); err == nil {
		return Record{}, fmt.Errorf("%s is already installed — `opentree agents update %s` refreshes it", p.Entry.ID, p.Entry.ID)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return Record{}, err
	}
	rec, err := p.install(ctx)
	if err != nil {
		_ = os.RemoveAll(p.Dir)
		return Record{}, err
	}
	if err := writeRecord(p.Dir, rec); err != nil {
		_ = os.RemoveAll(p.Dir)
		return Record{}, err
	}
	rec.Dir = p.Dir
	return rec, nil
}

// install produces the Record for the plan's kind, into p.Dir.
func (p Plan) install(ctx context.Context) (Record, error) {
	switch p.Kind {
	case "npm":
		return p.installNpm(ctx)
	}
	return Record{}, fmt.Errorf("unknown install kind %q", p.Kind)
}

// installNpm runs the argv the consent prompt printed, verbatim, and then
// asks the installed package which command it provides.
func (p Plan) installNpm(ctx context.Context) (Record, error) {
	argv := p.NpmArgv
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- assembled from the pinned registry entry and printed to the user before it runs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return Record{}, fmt.Errorf("npm install failed: %w", err)
	}
	name, _ := splitPackageSpec(p.Entry.Distribution.Npx.Package)
	shim, err := resolveBin(filepath.Join(p.Dir, "npm"), name)
	if err != nil {
		// The likeliest cause is a package that needed the install scripts
		// opentree refuses to run — say so, because "file not found" would
		// send the user debugging npm instead.
		return Record{}, fmt.Errorf("%w — the package was installed with npm's install hooks disabled, which some packages cannot survive", err)
	}
	return Record{
		Entry:       p.Entry,
		IndexURL:    p.IndexURL,
		Command:     shim,
		Args:        p.Entry.Distribution.Npx.Args,
		Env:         p.Entry.Distribution.Npx.Env,
		InstalledAt: time.Now().UTC(),
	}, nil
}

// Remove deletes an installed registry agent: the one directory the install
// owns, nothing else. It takes an id, never a path, and the same gate the
// ids pass everywhere else keeps it one — which is also why a store entry
// whose record is broken can still be removed by name.
func Remove(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	dir := agentsDir()
	if dir == "" {
		return fmt.Errorf("cannot find your home directory")
	}
	target := filepath.Join(dir, id)
	if _, err := os.Lstat(target); err != nil {
		return fmt.Errorf("%s is not installed from the registry", id)
	}
	return os.RemoveAll(target)
}
