package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// A site publishes its skills as a JSON index at a fixed path, and each entry
// carries the hash of the file it points at. There is no registry in the
// middle: the publisher is the host, which is the whole reason to prefer it
// over a directory someone else operates.
//
// https://github.com/agentskills/agentskills — the spec is still in review, so
// the schema URI is matched by prefix and a version that changes shape will
// read as "does not publish skills" rather than as a corrupt index.
const (
	wellKnownPath = "/.well-known/agent-skills/index.json"
	schemaPrefix  = "https://schemas.agentskills.io/discovery/"

	typeSkillMD = "skill-md"
	typeArchive = "archive"

	// The same ceilings the reference CLI documents. They are here because
	// every one of these bytes arrives from a host opentree has never met.
	maxArtifact = 10 << 20
	maxExtract  = 25 << 20
	maxFiles    = 1000
)

// An Entry is one skill a site offers. Description is carried so the picker can
// show what a skill is for before it is on disk — the same line the agent will
// later match against.
type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Digest      string `json:"digest"`

	// index is where this entry was read from, kept so a relative URL resolves
	// against it later. An Entry that did not come from Discover cannot be
	// installed, which is the intent: the digest is only worth anything when
	// the index it came from is known.
	index *url.URL
}

// Discover asks a site what skills it publishes.
//
// The argument is a site, not a URL to the index: the well-known path is fixed
// by the spec, so anything the user pastes — a bare host, a docs page deep in
// the tree — is reduced to its origin.
func Discover(ctx context.Context, site string) ([]Entry, error) {
	index, err := indexURL(site)
	if err != nil {
		return nil, err
	}
	body, err := fetch(ctx, index, maxArtifact)
	if err != nil {
		return nil, err
	}

	var doc struct {
		Schema string  `json:"$schema"`
		Skills []Entry `json:"skills"`
	}
	// Both failures say the same thing on purpose. A site that serves a single
	// page application answers 200 with HTML at every path, including this one,
	// and "not a publisher" is what that means however it is spelled.
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s does not publish skills", index.Host)
	}
	if !strings.HasPrefix(doc.Schema, schemaPrefix) {
		return nil, fmt.Errorf("%s does not publish skills", index.Host)
	}

	var out []Entry
	for _, e := range doc.Skills {
		// An unknown type is skipped rather than refused: the spec grows by
		// adding them, and one entry this version cannot fetch is no reason to
		// withhold the ones it can.
		if e.Type != typeSkillMD && e.Type != typeArchive {
			continue
		}
		if safeName(e.Name) == "" || e.URL == "" || e.Digest == "" {
			continue
		}
		e.index = index
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s publishes no skills this version can read", index.Host)
	}
	return out, nil
}

// Install downloads one entry into its own directory under dir.
func Install(ctx context.Context, e Entry, dir string) error {
	name := safeName(e.Name)
	if name == "" || e.index == nil {
		return fmt.Errorf("%q did not come from a skills index", e.Name)
	}
	artifact, err := url.Parse(e.URL)
	if err != nil {
		return fmt.Errorf("%s: %s is not a URL", e.Name, e.URL)
	}

	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}

	body, err := fetch(ctx, e.index.ResolveReference(artifact), maxArtifact)
	if err != nil {
		return err
	}
	// Before anything reaches the disk. A digest that does not match is the
	// one signal this format gives that the bytes are not what was advertised.
	if err := verify(body, e.Digest); err != nil {
		return fmt.Errorf("%s: %w", e.Name, err)
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	if e.Type == typeSkillMD {
		// Written as SKILL.md whatever the URL called it: agents look for that
		// name, and publishers do serve it lowercase.
		err = os.WriteFile(filepath.Join(dst, "SKILL.md"), body, 0600)
	} else {
		err = untar(body, dst)
	}
	if err == nil {
		_, err = os.Stat(filepath.Join(dst, "SKILL.md"))
		if err != nil {
			err = fmt.Errorf("%s arrived without a SKILL.md", e.Name)
		}
	}
	if err != nil {
		// Half an unpacked archive would show up in the list as a skill with
		// nothing behind it.
		_ = os.RemoveAll(dst)
		return err
	}
	return nil
}

// indexURL is the index a site would publish at, or an error if the argument
// does not name a site.
func indexURL(site string) (*url.URL, error) {
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	u, err := url.Parse(site)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q does not name a site", site)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("%s is not http", u.Scheme)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: wellKnownPath}, nil
}

// fetch GETs a URL, refusing a body past limit. Redirects are followed, which
// the spec requires and http.DefaultClient does.
func fetch(ctx context.Context, u *url.URL, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u.Host, resp.Status)
	}
	// One past the ceiling, so a body sitting exactly on it is still known to
	// be short rather than assumed to be.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than %d MiB", u.Path, limit>>20)
	}
	return body, nil
}

// verify checks a body against a digest of the form sha256:<hex>.
func verify(body []byte, digest string) error {
	want, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return fmt.Errorf("digest %q is not sha256", digest)
	}
	sum := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return fmt.Errorf("digest does not match — this is not the file the index describes")
	}
	return nil
}

// safeName is the entry's name when it can stand as a directory of its own,
// and empty otherwise. The name arrives over the network and becomes a path.
//
// The leading dot is CloneName's rule, and it is load-bearing twice over here:
// filepath.IsLocal(".") is true — "." is a local path, just not a directory of
// its own — so without it a skill could name itself the tree it installs into.
func safeName(name string) string {
	if name == "" || strings.HasPrefix(name, ".") ||
		!filepath.IsLocal(name) || strings.ContainsAny(name, `/\`) {
		return ""
	}
	return name
}

// untar unpacks a gzipped tarball into dst.
//
// Only .tar.gz: the spec also allows .zip, and neither publisher serving an
// index today uses one.
func untar(body []byte, dst string) error {
	if !bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		return fmt.Errorf("not a .tar.gz archive")
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	var files int
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if files++; files > maxFiles {
			return fmt.Errorf("archive holds more than %d files", maxFiles)
		}
		// Rejects an absolute path and any ".." on the way, which is both of
		// the path rules the spec asks for.
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("archive entry %q escapes the skill directory", hdr.Name)
		}
		// #nosec G305 -- filepath.IsLocal on the line above is the traversal
		// check, and a stricter one than the prefix comparison gosec looks for:
		// it rejects absolute paths and any ".." anywhere in the name.
		path := filepath.Join(dst, hdr.Name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			n, err := writeEntry(path, tr, maxExtract-written, hdr.FileInfo().Mode())
			if err != nil {
				return err
			}
			written += n
		default:
			// A link is how an archive reaches out of the directory it unpacks
			// into, and a skill is files. Refusing the archive rather than
			// dropping the entry: a bundle missing a piece it thinks it has is
			// worse than one that never landed.
			return fmt.Errorf("archive entry %q is a link, not a file", hdr.Name)
		}
	}
}

// writeEntry copies one archive member to disk, stopping if the archive
// expands past what is left of the budget.
func writeEntry(path string, r io.Reader, budget int64, mode fs.FileMode) (int64, error) {
	// The archive's own bits decide only whether a file is executable — skills
	// bundle scripts, and a script that arrives unrunnable is a bug report.
	// Everything else about the mode is opentree's: a downloaded tarball does
	// not get to hand itself setuid, or a group and a world to be read by.
	perm := fs.FileMode(0600)
	if mode.Perm()&0100 != 0 {
		perm = 0700
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	n, err := io.Copy(f, io.LimitReader(r, budget+1))
	if err != nil {
		return n, err
	}
	if n > budget {
		return n, fmt.Errorf("archive expands past %d MiB", int64(maxExtract)>>20)
	}
	return n, nil
}
