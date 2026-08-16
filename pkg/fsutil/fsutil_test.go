package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomic_CreatesTheFileAndItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "trust.json")

	if err := WriteAtomic(path, []byte("{}\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "{}\n" {
		t.Errorf("content = %q, want %q", got, "{}\n")
	}

	// Private: everything written this way is the user's own.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("mode = %o, want 0600", perm)
	}
}

// The point of the rename: a reader either sees the old file or the new one,
// never a truncated one, and no temp file is left behind either way.
func TestWriteAtomic_ReplacesWithoutLeavingLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(path, []byte("new")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want just the file", len(entries))
	}
}
