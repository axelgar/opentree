package registry

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
)

// Entry is one agent as the registry describes it: identity, a pinned
// version, and up to three ways to obtain it. The shape follows the
// registry's own agent.schema.json; fields the schema calls optional are
// omitempty here so a record written back to disk says only what the index
// said.
type Entry struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description"`
	Repository   string       `json:"repository,omitempty"`
	Website      string       `json:"website,omitempty"`
	Authors      []string     `json:"authors,omitempty"`
	License      string       `json:"license,omitempty"`
	Icon         string       `json:"icon,omitempty"`
	Distribution Distribution `json:"distribution"`
}

// Distribution is how an entry's agent is obtained. The registry allows any
// combination; opentree prefers npx over binary — the npm path inherits the
// adapter install's whole security posture, where a binary archive is
// trusted only as far as its checksum — and does not install uvx yet.
type Distribution struct {
	Npx    *PackageDist            `json:"npx,omitempty"`
	Uvx    *PackageDist            `json:"uvx,omitempty"`
	Binary map[string]BinaryTarget `json:"binary,omitempty"`
}

// PackageDist is a package-manager distribution: npm for npx, PyPI for uvx.
// The registry pins the version inside Package ("@scope/name@1.2.3"), which
// is what makes an install reproducible enough to show the user beforehand.
type PackageDist struct {
	Package string            `json:"package"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// BinaryTarget is one platform's archive: what to download, the digest to
// hold it to, and the command to run out of the extracted tree. Cmd is a
// path relative to that tree ("./bin/devin"), not a name to look up.
type BinaryTarget struct {
	Archive string            `json:"archive"`
	SHA256  string            `json:"sha256,omitempty"`
	Cmd     string            `json:"cmd"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// PlatformKey is this machine in the registry's vocabulary. The registry
// spells architectures the toolchain way (x86_64, aarch64) where Go spells
// them its own (amd64, arm64); an unknown combination is returned unmapped,
// so the refusal that follows names something the user can recognise.
func PlatformKey() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return runtime.GOOS + "-" + arch
}

// idPattern is the registry's own constraint on ids, re-checked here because
// the id doubles as the install's directory name: what the schema promises
// and what this machine received are two different files, and only the
// second one becomes a path element.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// maxIDLen matches the plugins store's cap on names used as path elements.
const maxIDLen = 64

// ValidateID is the gate every id passes before it touches the filesystem —
// on the way in from the index, and again on the way in from a command line,
// where "id" may be anything at all.
func ValidateID(id string) error {
	if id == "" || len(id) > maxIDLen || !idPattern.MatchString(id) {
		return fmt.Errorf("%q is not a registry id — lowercase letters, digits and hyphens, starting with a letter", id)
	}
	return nil
}

// Validate reports whether the entry is complete enough to act on. Checked
// per entry rather than per index so one malformed entry costs itself, not
// the whole catalogue — the index is assembled from forty repositories'
// worth of automation, and refusing all of it over one is a denial of
// service the format doesn't require.
func (e Entry) Validate() error {
	if err := ValidateID(e.ID); err != nil {
		return err
	}
	if e.Name == "" || e.Version == "" || e.Description == "" {
		return fmt.Errorf("%s: entry is missing name, version or description", e.ID)
	}
	if e.Distribution.Npx == nil && e.Distribution.Uvx == nil && len(e.Distribution.Binary) == 0 {
		return fmt.Errorf("%s: entry has no distribution", e.ID)
	}
	if d := e.Distribution.Npx; d != nil && d.Package == "" {
		return fmt.Errorf("%s: npx distribution names no package", e.ID)
	}
	if d := e.Distribution.Uvx; d != nil && d.Package == "" {
		return fmt.Errorf("%s: uvx distribution names no package", e.ID)
	}
	for platform, t := range e.Distribution.Binary {
		if t.Archive == "" || t.Cmd == "" {
			return fmt.Errorf("%s: binary target %s is missing its archive or command", e.ID, platform)
		}
	}
	return nil
}

// Via names how an entry arrives, in opentree's order of preference. uvx is
// listed but marked: hiding those entries would make the registry look
// smaller than it is, and the honest answer is "exists, not supported yet".
func (e Entry) Via() string {
	var kinds []string
	if e.Distribution.Npx != nil {
		kinds = append(kinds, "npm")
	}
	if len(e.Distribution.Binary) > 0 {
		kinds = append(kinds, "binary")
	}
	if e.Distribution.Uvx != nil {
		kinds = append(kinds, "uvx*")
	}
	return strings.Join(kinds, "+")
}

// Status is what this machine already has under the entry's name: the
// built-in agent that shadows it, the installed version, or nothing.
// FindAgent sees registry installs too — the loader ran first — so one
// lookup answers for both kinds.
func Status(e Entry) string {
	agent := config.FindAgent(e.ID)
	if agent == nil {
		agent = config.FindAgent(e.Name)
	}
	switch {
	case agent == nil:
		return ""
	case agent.Origin != nil:
		return "installed " + agent.Origin.Version
	default:
		return "built-in"
	}
}
