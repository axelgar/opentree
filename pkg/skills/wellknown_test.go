package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tarball builds a .tar.gz in memory. A nil-bodied entry with a link type
// stands in for the archive tricks the unpacker has to refuse.
func tarball(t *testing.T, entries ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, hdr := range entries {
		body := []byte(hdr.Linkname)
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(body))
		} else {
			body = nil
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func regular(name, contents string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Linkname: contents}
}

// publisher serves an index plus the files it names.
func publisher(t *testing.T, index string, files map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(wellKnownPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(index))
	})
	for path, body := range files {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscover(t *testing.T) {
	skill := []byte("---\nname: review\ndescription: look at code\n---\n")
	srv := publisher(t, fmt.Sprintf(`{
		"$schema": "%s0.2.0/schema.json",
		"skills": [
			{"name":"review","type":"skill-md","description":"look at code",
			 "url":"review/skill.md","digest":"%s"},
			{"name":"tomorrow","type":"something-new","description":"later",
			 "url":"x","digest":"sha256:00"}
		]}`, schemaPrefix, digestOf(skill)),
		map[string][]byte{"/.well-known/agent-skills/review/skill.md": skill})

	got, err := Discover(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// The unknown type is dropped, not fatal: a newer spec must not make an
	// index unreadable to an older opentree.
	if len(got) != 1 || got[0].Name != "review" {
		t.Fatalf("entries = %+v, want just review", got)
	}
	if got[0].Description != "look at code" {
		t.Errorf("description = %q", got[0].Description)
	}
}

// A site that answers every path with its own page is the common case, not a
// corrupt index, and it has to read as "no skills here".
func TestDiscover_NotAPublisher(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"html", "<!DOCTYPE html><html><body>hello</body></html>"},
		{"unknown schema", `{"$schema":"https://example.com/other/1.0.json","skills":[]}`},
		{"empty", `{"$schema":"` + schemaPrefix + `0.2.0/schema.json","skills":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := publisher(t, tc.body, nil)
			if _, err := Discover(t.Context(), srv.URL); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestDiscover_NoIndex(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if _, err := Discover(t.Context(), srv.URL); err == nil {
		t.Fatal("a 404 has to be an error — it is how a non-publisher answers")
	}
}

func TestInstall_SkillMD(t *testing.T) {
	skill := []byte("---\nname: review\n---\nlook at code\n")
	srv := publisher(t, fmt.Sprintf(`{"$schema":"%s0.2.0/schema.json","skills":[
		{"name":"review","type":"skill-md","description":"d",
		 "url":"review/skill.md","digest":"%s"}]}`, schemaPrefix, digestOf(skill)),
		map[string][]byte{"/.well-known/agent-skills/review/skill.md": skill})

	entries, err := Discover(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Install(t.Context(), entries[0], dir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Served as skill.md, stored as SKILL.md: the agent looks for the latter.
	got, err := os.ReadFile(filepath.Join(dir, "review", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md: %v", err)
	}
	if !bytes.Equal(got, skill) {
		t.Errorf("contents = %q", got)
	}
}

func TestInstall_DigestMismatch(t *testing.T) {
	srv := publisher(t, fmt.Sprintf(`{"$schema":"%s0.2.0/schema.json","skills":[
		{"name":"review","type":"skill-md","description":"d",
		 "url":"review/skill.md","digest":"%s"}]}`, schemaPrefix, digestOf([]byte("promised"))),
		map[string][]byte{"/.well-known/agent-skills/review/skill.md": []byte("delivered")})

	entries, err := Discover(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err = Install(t.Context(), entries[0], dir)
	if err == nil {
		t.Fatal("want a digest error, got none")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("error = %v, want it to name the digest", err)
	}
	// Nothing on disk: a file that failed its hash must not be left where the
	// agent will read it anyway.
	if _, err := os.Stat(filepath.Join(dir, "review")); !os.IsNotExist(err) {
		t.Error("the rejected skill was left behind")
	}
}

func TestInstall_Archive(t *testing.T) {
	archive := tarball(t,
		regular("SKILL.md", "---\nname: sdk\n---\n"),
		&tar.Header{Name: "scripts", Typeflag: tar.TypeDir, Mode: 0755},
		regular("scripts/run.sh", "#!/bin/sh\necho hi\n"),
	)
	srv := publisher(t, fmt.Sprintf(`{"$schema":"%s0.2.0/schema.json","skills":[
		{"name":"sdk","type":"archive","description":"d",
		 "url":"sdk.tar.gz","digest":"%s"}]}`, schemaPrefix, digestOf(archive)),
		map[string][]byte{"/.well-known/agent-skills/sdk.tar.gz": archive})

	entries, err := Discover(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Install(t.Context(), entries[0], dir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, rel := range []string{"SKILL.md", "scripts/run.sh"} {
		if _, err := os.Stat(filepath.Join(dir, "sdk", rel)); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
}

// An archive with no SKILL.md at its root is not a skill, whatever else it
// holds — the same rule Clone applies to a repository.
func TestInstall_ArchiveWithoutSkillMD(t *testing.T) {
	archive := tarball(t, regular("docs/README.md", "hello"))
	srv := publisher(t, fmt.Sprintf(`{"$schema":"%s0.2.0/schema.json","skills":[
		{"name":"sdk","type":"archive","description":"d",
		 "url":"sdk.tar.gz","digest":"%s"}]}`, schemaPrefix, digestOf(archive)),
		map[string][]byte{"/.well-known/agent-skills/sdk.tar.gz": archive})

	entries, err := Discover(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Install(t.Context(), entries[0], dir); err == nil {
		t.Fatal("want an error, got none")
	}
	if _, err := os.Stat(filepath.Join(dir, "sdk")); !os.IsNotExist(err) {
		t.Error("the unpacked archive was left behind")
	}
}

func TestUntar_Refuses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []*tar.Header
	}{
		{"path traversal", []*tar.Header{regular("../escaped.md", "x")}},
		{"absolute path", []*tar.Header{regular("/etc/passwd", "x")}},
		{"symlink", []*tar.Header{{
			Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0777}}},
		{"hard link", []*tar.Header{{
			Name: "link", Typeflag: tar.TypeLink, Linkname: "/etc/passwd", Mode: 0644}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := untar(tarball(t, tc.entries...), dir); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestUntar_NotAnArchive(t *testing.T) {
	if err := untar([]byte("PK\x03\x04 a zip, which this version cannot read"), t.TempDir()); err == nil {
		t.Fatal("want an error naming the format, got none")
	}
}

// A tarball that expands without bound must not fill the disk. 64 KiB of zeros
// per file compresses to almost nothing, so this stays a small test.
func TestUntar_DecompressionBomb(t *testing.T) {
	var entries []*tar.Header
	entries = append(entries, regular("SKILL.md", "---\nname: bomb\n---\n"))
	for i := 0; i < 500; i++ {
		entries = append(entries, regular(fmt.Sprintf("f%d", i), strings.Repeat("0", 64<<10)))
	}
	if err := untar(tarball(t, entries...), t.TempDir()); err == nil {
		t.Fatal("want the expansion to be refused, got none")
	}
}

func TestSafeName(t *testing.T) {
	for name, want := range map[string]string{
		"review":     "review",
		"":           "",
		"..":         "",
		"../escape":  "",
		"a/b":        "",
		`a\b`:        "",
		"/absolute":  "",
		".":          "",
		".hidden":    "",
		"with space": "with space",
	} {
		if got := safeName(name); got != want {
			t.Errorf("safeName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestIndexURL(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":                    "https://example.com" + wellKnownPath,
		"https://example.com":            "https://example.com" + wellKnownPath,
		"https://example.com/docs/deep":  "https://example.com" + wellKnownPath,
		"https://example.com?a=b#c":      "https://example.com" + wellKnownPath,
		"http://localhost:8080/anything": "http://localhost:8080" + wellKnownPath,
	} {
		u, err := indexURL(in)
		if err != nil {
			t.Errorf("indexURL(%q): %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("indexURL(%q) = %q, want %q", in, u, want)
		}
	}
	for _, in := range []string{"", "git@github.com:o/r.git", "ssh://example.com", "://"} {
		if u, err := indexURL(in); err == nil {
			t.Errorf("indexURL(%q) = %q, want an error", in, u)
		}
	}
}
