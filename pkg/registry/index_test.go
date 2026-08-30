package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var testCerts *x509.CertPool

// useHTTPS points the package's fetches at a test server, the way
// pkg/skills' tests do. The first call in a test installs a client and
// undoes it afterwards; later ones only add their certificate. The redirect
// policy is the real one, so a test can watch it refuse. Swapping the
// package-level client is only safe because nothing in this package's tests
// calls t.Parallel.
func useHTTPS(t *testing.T, srv *httptest.Server) {
	t.Helper()
	if testCerts == nil {
		testCerts = x509.NewCertPool()
		prev := httpClient
		httpClient = &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				RootCAs: testCerts, MinVersion: tls.VersionTLS12,
			}},
			CheckRedirect: refuseDowngrade,
		}
		t.Cleanup(func() { httpClient, testCerts = prev, nil })
	}
	testCerts.AddCert(srv.Certificate())
}

// indexSite serves the fixture index and counts hits per path, so a test can
// assert not just what was answered but what was asked for.
type indexSite struct {
	srv  *httptest.Server
	hits map[string]int
	body []byte
	down bool
}

func newIndexSite(t *testing.T) *indexSite {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &indexSite{hits: map[string]int{}, body: data}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits[r.URL.Path]++
		if s.down {
			// Close without answering: to the client this is the registry
			// being unreachable, which is what the offline tests simulate.
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		_, _ = w.Write(s.body)
	}))
	t.Cleanup(s.srv.Close)
	useHTTPS(t, s.srv)
	return s
}

func (s *indexSite) url() string { return s.srv.URL + "/registry.json" }

func TestFetch_ParsesTheIndexAndDropsBrokenEntries(t *testing.T) {
	s := newIndexSite(t)
	// One entry with no distribution, spliced in front of the good five: it
	// must cost itself, not the catalogue.
	var raw struct {
		Version string            `json:"version"`
		Agents  []json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(s.body, &raw); err != nil {
		t.Fatal(err)
	}
	broken := json.RawMessage(`{"id":"broken","name":"B","version":"1.0.0","description":"x","distribution":{}}`)
	raw.Agents = append([]json.RawMessage{broken}, raw.Agents...)
	var err error
	if s.body, err = json.Marshal(raw); err != nil {
		t.Fatal(err)
	}

	idx, err := Fetch(context.Background(), s.url())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(idx.Agents) != 5 {
		t.Errorf("kept %d entries, want the 5 valid ones", len(idx.Agents))
	}
	for _, e := range idx.Agents {
		if e.ID == "broken" {
			t.Error("the invalid entry survived")
		}
	}
}

func TestFetch_RefusesPlaintextAndGarbage(t *testing.T) {
	s := newIndexSite(t)
	if _, err := Fetch(context.Background(), "http://example.com/registry.json"); err == nil {
		t.Error("an http URL was fetched")
	}
	s.body = []byte("<html>not a registry</html>")
	if _, err := Fetch(context.Background(), s.url()); err == nil {
		t.Error("an HTML body decoded as an index")
	}
	s.body = []byte(`{"version":"1.0.0","agents":[]}`)
	if _, err := Fetch(context.Background(), s.url()); err == nil {
		t.Error("an empty catalogue should read as a wrong URL, not as success")
	}
}

func TestFetch_RefusesAnOversizedBody(t *testing.T) {
	s := newIndexSite(t)
	s.body = make([]byte, maxIndexBytes+1)
	if _, err := Fetch(context.Background(), s.url()); err == nil {
		t.Error("a body past the ceiling was accepted")
	}
}

func TestFetchOrCached_FreshWhenReachableCachedWhenNot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIndexSite(t)

	idx, note, err := FetchOrCached(context.Background(), s.url())
	if err != nil || note != "" {
		t.Fatalf("fresh fetch: err=%v note=%q", err, note)
	}
	if len(idx.Agents) != 5 {
		t.Fatalf("fresh fetch kept %d entries", len(idx.Agents))
	}
	if _, err := os.Stat(filepath.Join(Dir(), cacheFile)); err != nil {
		t.Fatalf("no cache written: %v", err)
	}

	// Reachable again: the answer is fetched fresh, not served from cache —
	// the cache is for being offline, not for saving a round trip the user
	// explicitly asked for.
	if _, _, err := FetchOrCached(context.Background(), s.url()); err != nil {
		t.Fatal(err)
	}
	if s.hits["/registry.json"] != 2 {
		t.Errorf("hits = %d, want a fetch per call while reachable", s.hits["/registry.json"])
	}

	s.down = true
	idx, note, err = FetchOrCached(context.Background(), s.url())
	if err != nil {
		t.Fatalf("offline with cache: %v", err)
	}
	if note == "" || len(idx.Agents) != 5 {
		t.Errorf("offline answer = %d entries, note %q; want the cache with its age noted", len(idx.Agents), note)
	}
}

func TestFetchOrCached_OfflineWithoutCacheIsAClearError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIndexSite(t)
	s.down = true
	if _, _, err := FetchOrCached(context.Background(), s.url()); err == nil {
		t.Error("offline with no cache should refuse, not invent an answer")
	}
}

func TestFetchOrCached_ACacheForAnotherIndexDoesNotAnswer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIndexSite(t)
	if _, _, err := FetchOrCached(context.Background(), s.url()); err != nil {
		t.Fatal(err)
	}
	s.down = true
	// A different URL must not be answered by the first one's cache: the
	// cache says what one index said, not what any index would say.
	if _, _, err := FetchOrCached(context.Background(), s.srv.URL+"/other.json"); err == nil {
		t.Error("a cache recorded for one URL answered for another")
	}
}

func TestAge_ReadableUnits(t *testing.T) {
	if got := age(time.Now().Add(-3 * time.Hour)); got != "3h" {
		t.Errorf("age(3h) = %q", got)
	}
	if got := age(time.Now().Add(-72 * time.Hour)); got != "3d" {
		t.Errorf("age(3d) = %q", got)
	}
	if got := age(time.Now().Add(-30 * time.Second)); got != "moments" {
		t.Errorf("age(30s) = %q", got)
	}
}
