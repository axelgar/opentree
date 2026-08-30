package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/axelgar/opentree/pkg/fsutil"
)

// Index is the published registry: a version stamp and the agents. Entries
// that fail validation are dropped one at a time on the way in — the index
// aggregates forty repositories' worth of hourly automation, and one
// malformed entry must cost itself, not the catalogue.
type Index struct {
	Version string  `json:"version"`
	Agents  []Entry `json:"agents"`
}

// maxIndexBytes bounds the index fetch. The real index is well under a
// megabyte; a response past this ceiling is not a bigger catalogue, it is a
// wrong server.
const maxIndexBytes = 8 << 20

// httpClient is what everything in this package is fetched with. A
// package-level var rather than http.DefaultClient for the same two reasons
// pkg/skills has one: the redirect rule below is opentree's, and the tests
// need somewhere to hand a client that trusts their own certificate — https
// is the only scheme left here, so a test served over plaintext would be
// exercising a path production refuses.
var httpClient = &http.Client{CheckRedirect: refuseDowngrade}

// maxRedirects is net/http's own default, which setting CheckRedirect replaces.
const maxRedirects = 10

// refuseDowngrade stops a redirect chain the moment it leaves https. The
// index names archive URLs that later downloads trust; a chain that stepped
// down to plaintext would let the party on the wire choose both the bytes
// and the digest they are checked against.
func refuseDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("%s redirects to %s, which is not https — the registry is only taken over https",
			via[len(via)-1].URL.Host, req.URL.Scheme)
	}
	return nil
}

// fetchBytes GETs a URL, refusing a body past limit. The scheme is checked
// here rather than trusted from the caller, because URLs also arrive inside
// index entries, where "http://" would be the same hole by a shorter route.
func fetchBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%s is not https — the registry is only taken over https", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
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

// Fetch downloads and decodes the index at rawURL. Malformed entries are
// dropped rather than fatal; an index that decodes to nothing at all is an
// error, because "empty catalogue" and "wrong URL" must not read the same.
func Fetch(ctx context.Context, rawURL string) (Index, error) {
	body, err := fetchBytes(ctx, rawURL, maxIndexBytes)
	if err != nil {
		return Index{}, err
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return Index{}, fmt.Errorf("%s does not hold a registry index: %w", rawURL, err)
	}
	kept := idx.Agents[:0]
	for _, e := range idx.Agents {
		if e.Validate() == nil {
			kept = append(kept, e)
		}
	}
	idx.Agents = kept
	if len(idx.Agents) == 0 {
		return Index{}, fmt.Errorf("%s holds no usable agent entries", rawURL)
	}
	return idx, nil
}

// cacheFile keeps the last index this machine saw, so being offline degrades
// to an answer with its age on it instead of no answer.
const cacheFile = "index.json"

// cachedIndex is what the cache file holds: the index, where it came from,
// and when — the age is the part the user is told.
type cachedIndex struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	Index     Index     `json:"index"`
}

func cachePath() string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, cacheFile)
}

// writeCache records the index just fetched. Best-effort by design: a cache
// that cannot be written costs the next offline run its answer, not this
// run its result.
func writeCache(idx Index, fromURL string) {
	path := cachePath()
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(cachedIndex{URL: fromURL, FetchedAt: time.Now().UTC(), Index: idx}, "", "  ")
	if err != nil {
		return
	}
	_ = fsutil.WriteAtomic(path, data)
}

// readCache is the cached index, if one exists and parses. A missing, blank
// or corrupt cache is simply no cache — it is a convenience copy of public
// data, and the fix for a damaged one is the next successful fetch.
func readCache() (cachedIndex, bool) {
	path := cachePath()
	if path == "" {
		return cachedIndex{}, false
	}
	data, err := os.ReadFile(path) // #nosec G304 -- opentree's own registry cache, under the user's home
	if err != nil {
		return cachedIndex{}, false
	}
	var c cachedIndex
	if err := json.Unmarshal(data, &c); err != nil || len(c.Index.Agents) == 0 {
		return cachedIndex{}, false
	}
	return c, true
}

// age says how old a cached index is, in the largest unit that keeps it
// honest — "3h" reads at a glance, "187m" does not.
func age(since time.Time) string {
	d := time.Since(since)
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FetchOrCached is the offline doctrine in one place: a fresh fetch when the
// network allows, the cached index with its age noted when it does not, and
// a clear refusal when there is neither. The note is "" on a fresh answer;
// callers print it verbatim so every command explains staleness the same way.
func FetchOrCached(ctx context.Context, rawURL string) (Index, string, error) {
	idx, err := Fetch(ctx, rawURL)
	if err == nil {
		writeCache(idx, rawURL)
		return idx, "", nil
	}
	if c, ok := readCache(); ok && c.URL == rawURL {
		note := fmt.Sprintf("using the index cached %s ago — %v", age(c.FetchedAt), err)
		return c.Index, note, nil
	}
	return Index{}, "", fmt.Errorf("cannot reach the registry and no cached index exists: %w", err)
}
