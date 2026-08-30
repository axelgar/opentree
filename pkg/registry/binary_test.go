package registry

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// archiveSite serves fabricated release archives over TLS, the way the
// index tests serve the index.
func archiveSite(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	useHTTPS(t, srv)
	return srv
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type tarMember struct {
	name, link string
	body       []byte
	mode       int64
}

func makeTarGz(t *testing.T, members ...tarMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Mode: m.mode}
		if m.link != "" {
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = m.link
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(m.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if m.link == "" {
			if _, err := tw.Write(m.body); err != nil {
				t.Fatal(err)
			}
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

// binaryEntry is a registry entry whose one target points at the test server.
func binaryEntry(archiveURL, sum, cmd string) Entry {
	return Entry{
		ID: "bin-agent", Name: "Bin Agent", Version: "1.0.0", Description: "a binary agent",
		Distribution: Distribution{Binary: map[string]BinaryTarget{
			PlatformKey(): {Archive: archiveURL, SHA256: sum, Cmd: cmd, Args: []string{"acp"}},
		}},
	}
}

func TestInstallBinary_TarGzEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	archive := makeTarGz(t,
		tarMember{name: "./bin/agent", body: []byte("#!/bin/sh\nexit 0\n"), mode: 0o755},
		tarMember{name: "share/doc.txt", body: []byte("read me"), mode: 0o644},
	)
	srv := archiveSite(t, map[string][]byte{"/agent.tar.gz": archive})

	entry := binaryEntry(srv.URL+"/agent.tar.gz", sha256hex(archive), "./bin/agent")
	plan, err := NewPlan(entry, DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "binary" {
		t.Fatalf("Kind = %q", plan.Kind)
	}
	if desc := plan.Describe(); !strings.Contains(desc, srv.URL) || !strings.Contains(desc, sha256hex(archive)) {
		t.Errorf("Describe() = %q, want the URL and digest in it", desc)
	}

	rec, err := plan.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := filepath.Join(plan.Dir, "tree", "bin", "agent"); rec.Command != want {
		t.Errorf("Command = %q, want %q", rec.Command, want)
	}
	info, err := os.Stat(rec.Command)
	if err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("command not executable: %v %v", info, err)
	}
	if rec.Platform != PlatformKey() || len(rec.Args) != 1 || rec.Args[0] != "acp" {
		t.Errorf("record = %+v, want platform and args carried", rec)
	}
	if records, problems := Installed(); len(records) != 1 || len(problems) != 0 {
		t.Errorf("store = %d records, %v", len(records), problems)
	}
	if _, err := os.Stat(filepath.Join(plan.Dir, ".download")); !os.IsNotExist(err) {
		t.Error("the downloaded archive was kept after extraction")
	}
}

func TestInstallBinary_ChecksumMismatchLeavesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	archive := makeTarGz(t, tarMember{name: "bin/agent", body: []byte("x"), mode: 0o755})
	srv := archiveSite(t, map[string][]byte{"/agent.tar.gz": archive})

	entry := binaryEntry(srv.URL+"/agent.tar.gz", strings.Repeat("ab", 32), "./bin/agent")
	plan, err := NewPlan(entry, DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want the checksum refusal", err)
	}
	if _, err := os.Lstat(plan.Dir); !os.IsNotExist(err) {
		t.Error("a refused download left its directory behind")
	}
}

// The digest the registry publishes may be upper-case hex; the comparison
// must not care.
func TestInstallBinary_ChecksumCaseInsensitive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	archive := makeTarGz(t, tarMember{name: "agent", body: []byte("#!/bin/sh\n"), mode: 0o755})
	srv := archiveSite(t, map[string][]byte{"/a.tgz": archive})
	entry := binaryEntry(srv.URL+"/a.tgz", strings.ToUpper(sha256hex(archive)), "./agent")
	plan, err := NewPlan(entry, DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Run(context.Background()); err != nil {
		t.Errorf("upper-case digest refused: %v", err)
	}
}

func TestInstallBinary_RefusesTraversalAndEscapingLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	traversal := makeTarGz(t, tarMember{name: "../outside", body: []byte("x"), mode: 0o644})
	escaping := makeTarGz(t,
		tarMember{name: "bin/agent", body: []byte("x"), mode: 0o755},
		tarMember{name: "bin/link", link: "../../outside"},
	)
	absolute := makeTarGz(t,
		tarMember{name: "bin/agent", body: []byte("x"), mode: 0o755},
		tarMember{name: "bin/link", link: "/etc/passwd"},
	)
	srv := archiveSite(t, map[string][]byte{
		"/traversal.tar.gz": traversal, "/escaping.tar.gz": escaping, "/absolute.tar.gz": absolute,
	})

	for _, name := range []string{"traversal", "escaping", "absolute"} {
		url := srv.URL + "/" + name + ".tar.gz"
		var body []byte
		switch name {
		case "traversal":
			body = traversal
		case "escaping":
			body = escaping
		case "absolute":
			body = absolute
		}
		plan, err := NewPlan(binaryEntry(url, sha256hex(body), "./bin/agent"), DefaultIndexURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Run(context.Background()); err == nil {
			t.Errorf("%s archive extracted", name)
		}
		if _, err := os.Lstat(plan.Dir); !os.IsNotExist(err) {
			t.Errorf("%s archive left its directory behind", name)
		}
	}
}

// The tbz2 fixture holds a versioned binary and a relative symlink to it —
// the shape real release tarballs have, and the reason in-tree links are
// allowed where the skills installer refuses all of them.
func TestInstallBinary_TarBz2WithInTreeSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	archive, err := os.ReadFile(filepath.Join("testdata", "agent.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	srv := archiveSite(t, map[string][]byte{"/agent.tar.bz2": archive})
	plan, err := NewPlan(binaryEntry(srv.URL+"/agent.tar.bz2", sha256hex(archive), "./bin/agent"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := plan.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(rec.Command)
	if err != nil {
		t.Fatalf("the recorded command does not resolve: %v", err)
	}
	if filepath.Base(resolved) != "agent-1.0" {
		t.Errorf("resolved = %q, want the versioned binary behind the link", resolved)
	}
}

func TestInstallBinary_ZipKeepsTheExecBit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "dist/agent", Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("#!/bin/sh\n")); err != nil {
		t.Fatal(err)
	}
	plain, err := zw.Create("dist/readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := archiveSite(t, map[string][]byte{"/agent.zip": buf.Bytes()})
	plan, err := NewPlan(binaryEntry(srv.URL+"/agent.zip", sha256hex(buf.Bytes()), "./dist/agent"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := plan.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	info, err := os.Stat(rec.Command)
	if err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("zip lost the exec bit: %v %v", info, err)
	}
	if readme, err := os.Stat(filepath.Join(plan.Dir, "tree", "dist", "readme.txt")); err != nil || readme.Mode()&0o100 != 0 {
		t.Errorf("plain member wrong: %v %v", readme, err)
	}
}

// A raw download is the program itself; it lands under the entry's own cmd
// name and is made executable, since a bare HTTP body arrives with no mode.
func TestInstallBinary_RawBinary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	body := []byte("#!/bin/sh\nexit 0\n")
	srv := archiveSite(t, map[string][]byte{"/agent-linux-amd64": body})
	plan, err := NewPlan(binaryEntry(srv.URL+"/agent-linux-amd64", sha256hex(body), "./agent"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := plan.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	info, err := os.Stat(rec.Command)
	if err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("raw binary not executable: %v %v", info, err)
	}
}

func TestNewPlan_NamesThePlatformsItDoesShip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	e := Entry{
		ID: "elsewhere", Name: "Elsewhere", Version: "1.0.0", Description: "ships for windows only",
		Distribution: Distribution{Binary: map[string]BinaryTarget{
			"windows-x86_64": {Archive: "https://example.com/a.zip", Cmd: "./a.exe"},
		}},
	}
	_, err := NewPlan(e, DefaultIndexURL)
	if err == nil || !strings.Contains(err.Error(), "windows-x86_64") || !strings.Contains(err.Error(), PlatformKey()) {
		t.Errorf("err = %v, want the missing platform and the shipped one named", err)
	}
}

func TestTreeCommand_StaysInside(t *testing.T) {
	if rel, err := treeCommand("./bin/devin"); err != nil || rel != filepath.Join("bin", "devin") {
		t.Errorf("treeCommand(./bin/devin) = %q, %v", rel, err)
	}
	for _, bad := range []string{"../up", "./..", "/abs/path", "./bin/../../x", "."} {
		if _, err := treeCommand(bad); err == nil {
			t.Errorf("treeCommand(%q) accepted", bad)
		}
	}
}
