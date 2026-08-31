package registry

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The budgets. Downloads stream to disk rather than through memory — a
// release archive is hundreds of megabytes, which is also why these ceilings
// are so much higher than the index's: they bound a hostile server's cost in
// disk, not a JSON document's cost in RAM.
const (
	maxArchiveBytes = int64(1) << 30 // one downloaded archive
	maxExtractBytes = int64(2) << 30 // everything an archive expands to
	maxExtractFiles = 20000
)

// download streams a URL into dst, hashing as it writes, and returns the
// hex sha256 of what landed. The hash is computed on the way down rather
// than in a second read so the bytes checked are exactly the bytes kept.
func download(ctx context.Context, rawURL, dst string, limit int64) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("%s is not https — archives are only taken over https", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", u.Host, resp.Status)
	}

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a path this install owns, under ~/.opentree
	if err != nil {
		return "", err
	}
	h := sha256.New()
	// One past the ceiling, so a body sitting exactly on it is still known
	// to be short rather than assumed to be.
	n, err := io.Copy(f, io.TeeReader(io.LimitReader(resp.Body, limit+1), h))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("%s is larger than %d MiB", u.Path, limit>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// archiveKind reads the distribution format off the URL's path, the way the
// registry's own format doc enumerates them. Anything else is a raw binary:
// the format allows that, and several entries use it.
func archiveKind(rawURL string) string {
	name := strings.ToLower(path.Base(rawURL))
	if u, err := url.Parse(rawURL); err == nil {
		name = strings.ToLower(path.Base(u.Path))
	}
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return "tgz"
	case strings.HasSuffix(name, ".tar.bz2"), strings.HasSuffix(name, ".tbz2"):
		return "tbz2"
	case strings.HasSuffix(name, ".zip"):
		return "zip"
	default:
		return "raw"
	}
}

// installBinary is the archive path: download, verify, then extract — in
// that order, so nothing an unverified archive says ever shapes the tree.
// An entry without a published digest is verified only by https, which the
// consent prompt said out loud before this ran.
func (p Plan) installBinary(ctx context.Context) (Record, error) {
	archive := filepath.Join(p.Dir, ".download")
	sum, err := download(ctx, p.Target.Archive, archive, maxArchiveBytes)
	if err != nil {
		return Record{}, err
	}
	if p.Target.SHA256 != "" && !strings.EqualFold(sum, p.Target.SHA256) {
		return Record{}, fmt.Errorf("checksum mismatch for %s: the registry says %s, the download is %s — refusing it",
			p.Target.Archive, strings.ToLower(p.Target.SHA256), sum)
	}

	tree := filepath.Join(p.Dir, "tree")
	relCmd, err := treeCommand(p.Target.Cmd)
	if err != nil {
		return Record{}, err
	}
	kind := archiveKind(p.Target.Archive)
	if kind == "raw" {
		// The download is the program. It lands under the name the entry's
		// own cmd gives it, so the record points at something recognisable.
		dst := filepath.Join(tree, relCmd)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Record{}, err
		}
		if err := os.Rename(archive, dst); err != nil {
			return Record{}, err
		}
	} else {
		if err := extract(kind, archive, tree); err != nil {
			return Record{}, err
		}
		_ = os.Remove(archive)
	}

	cmdPath := filepath.Join(tree, relCmd)
	info, err := os.Stat(cmdPath)
	if err != nil || info.IsDir() {
		return Record{}, fmt.Errorf("the archive does not contain %s where the entry says it is", p.Target.Cmd)
	}
	// Zip has no reliable permission story and a raw download has none at
	// all, so the one file the record points at is made executable here
	// rather than trusted to arrive that way.
	if info.Mode()&0o100 == 0 {
		if err := os.Chmod(cmdPath, 0o700); err != nil {
			return Record{}, err
		}
	}

	return Record{
		Entry:       p.Entry,
		IndexURL:    p.IndexURL,
		Platform:    p.Platform,
		Command:     cmdPath,
		Args:        p.Target.Args,
		Env:         p.Target.Env,
		InstalledAt: time.Now().UTC(),
	}, nil
}

// treeCommand is the entry's cmd as a path inside the extracted tree:
// relative, cleaned, and still inside after cleaning. The "./" prefix the
// format uses is spelling, not meaning.
func treeCommand(cmd string) (string, error) {
	rel := filepath.Clean(strings.TrimPrefix(cmd, "./"))
	if rel == "." || filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("the entry's command %q escapes its own tree", cmd)
	}
	return rel, nil
}

// extract unpacks one archive into dst. The rules differ from the skills
// installer's in one deliberate way: relative symlinks that stay inside the
// tree are allowed, because real release tarballs contain them (a versioned
// binary and its unversioned name), while absolute and escaping links are
// still refused — a link is data until something follows it, and the thing
// that follows it runs as the user.
func extract(kind, archive, dst string) error {
	// dst exists before the first member lands: confined resolves member
	// paths against it, and a root that is not there yet cannot anchor the
	// comparison.
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	f, err := os.Open(archive) // #nosec G304 -- the file download just wrote, under ~/.opentree
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	switch kind {
	case "tgz":
		zr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		return untar(tar.NewReader(zr), dst)
	case "tbz2":
		return untar(tar.NewReader(bzip2.NewReader(f)), dst)
	case "zip":
		info, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return err
		}
		return unzip(zr, dst)
	}
	return fmt.Errorf("unknown archive kind %q", kind)
}

// budget tracks the extraction ceilings across one archive.
type budget struct {
	bytes int64
	files int
}

func (b *budget) file() error {
	b.files++
	if b.files > maxExtractFiles {
		return fmt.Errorf("archive holds more than %d files", maxExtractFiles)
	}
	return nil
}

// write copies one member to disk under what remains of the byte budget.
// The limit is enforced on what is written, not what the header claims —
// headers are the archive's own testimony.
func (b *budget) write(dst string, r io.Reader, executable bool) error {
	perm := os.FileMode(0o644)
	if executable {
		perm = 0o755
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) // #nosec G304 -- inside the tree entryPath just vetted
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxExtractBytes-b.bytes+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	b.bytes += n
	if b.bytes > maxExtractBytes {
		return fmt.Errorf("archive expands past %d MiB", maxExtractBytes>>20)
	}
	return nil
}

// entryPath vets one member name and returns where it lands. Two gates, both
// before anything touches disk. The lexical one: the name is cleaned and must
// stay local, so nothing an archive says places a file outside dst by
// spelling. The physical one: the member's parent directory is resolved with
// the symlinks already extracted — this package allows in-tree relative
// links, and a path that is innocent as text can still ride an earlier
// member's link somewhere else entirely. Only the resolved place counts.
func entryPath(dst, name string) (string, error) {
	rel := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(name), "./"))
	if rel == "." {
		return "", nil // the archive's own root directory
	}
	if filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("archive member %q escapes the extraction directory", name)
	}
	target := filepath.Join(dst, rel)
	if err := confined(dst, target); err != nil {
		return "", fmt.Errorf("archive member %q: %w", name, err)
	}
	return target, nil
}

// confined refuses a target whose deepest already-existing ancestor resolves
// outside dst. Ancestors that do not exist yet cannot be links — the MkdirAll
// that follows creates them as real directories — so resolving the existing
// part of the chain is resolving all there is to resolve.
func confined(dst, target string) error {
	realDst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		return err
	}
	ancestor := filepath.Dir(target)
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			if resolved != realDst && !strings.HasPrefix(resolved, realDst+string(os.PathSeparator)) {
				return fmt.Errorf("resolves outside the extraction directory")
			}
			return nil
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return nil
		}
		ancestor = next
	}
}

// archiveLink is one symlink an archive asked for: where it was created,
// and what its header said it points to.
type archiveLink struct {
	path, linkname string
}

// resolveLinks is the last word on every symlink the archive created, once
// the whole tree is on disk. The per-member checks reason about spelling;
// only now, with every target extracted, can each link be actually resolved
// — a chain assembled out of order, or a link whose meaning changed as later
// members landed, is invisible any earlier. A link that resolves outside
// the tree fails the archive; one that resolves to nothing is removed and
// the extraction continues, because a link to nothing serves nobody and
// failing a release over one would refuse archives that work everywhere
// else.
func resolveLinks(dst string, links []archiveLink) error {
	if len(links) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(dst)
	if err != nil {
		return err
	}
	for _, l := range links {
		joined := filepath.Join(filepath.Dir(l.path), l.linkname) // #nosec G305 -- handed to EvalSymlinks below, whose answer is only compared against the root
		resolved, err := filepath.EvalSymlinks(joined)
		if err != nil {
			if removeErr := os.Remove(l.path); removeErr != nil {
				return removeErr
			}
			continue
		}
		if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return fmt.Errorf("archive link %s resolves outside the extraction directory", filepath.Base(l.path))
		}
	}
	return nil
}

// linkTarget vets a symlink member: the linkname must be relative, and the
// place it points to — resolved from the link's own directory — must still
// sit under root. The check is spelled as a prefix comparison against the
// root rather than the equivalent filepath.IsLocal, deliberately: this is
// the shape static analysis recognises as the guard on symlink extraction,
// and a guard only a human can see is half a guard.
func linkTarget(root, target, linkname string) error {
	if linkname == "" || filepath.IsAbs(linkname) {
		return fmt.Errorf("archive links to %q — absolute links are refused", linkname)
	}
	resolved := filepath.Join(filepath.Dir(target), linkname)
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return fmt.Errorf("archive links outside the extraction directory (%q)", linkname)
	}
	return nil
}

func untar(tr *tar.Reader, dst string) error {
	var b budget
	var links []archiveLink
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return resolveLinks(dst, links)
		}
		if err != nil {
			return err
		}
		target, err := entryPath(dst, hdr.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := b.file(); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := b.write(target, tr, hdr.FileInfo().Mode()&0o100 != 0); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := linkTarget(dst, target, hdr.Linkname); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
			links = append(links, archiveLink{path: target, linkname: hdr.Linkname})
		default:
			// Hard links, devices, FIFOs: nothing a release archive needs,
			// and each is its own way to write somewhere unexpected. Skipped
			// rather than fatal — some tars carry PAX headers as members.
		}
	}
}

func unzip(zr *zip.Reader, dst string) error {
	var b budget
	var links []archiveLink
	for _, f := range zr.File {
		target, err := entryPath(dst, f.Name)
		if err != nil {
			return err
		}
		if target == "" || f.FileInfo().IsDir() {
			if target != "" {
				if err := os.MkdirAll(target, 0o755); err != nil {
					return err
				}
			}
			continue
		}
		if err := b.file(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			// Zip spells a symlink as a file whose content is the target.
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			if err != nil {
				return err
			}
			if err := linkTarget(dst, target, string(link)); err != nil {
				return err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
			links = append(links, archiveLink{path: target, linkname: string(link)})
			continue
		}
		// The mode is applied from the header because zip is exactly where
		// the exec bit gets lost — a cursor-agent extracted 0644 is a chat
		// that can never open.
		err = b.write(target, rc, f.FileInfo().Mode()&0o100 != 0)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return resolveLinks(dst, links)
}
