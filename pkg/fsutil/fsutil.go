// Package fsutil holds the file operations opentree needs in more than one
// place, so a fix to one of them is a fix to all of them.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes data via a temp file and a rename, so an interrupted write
// cannot leave the user with a truncated file where a valid one used to be.
//
// The result is private (0600) because everything opentree writes this way is
// the user's own: a config file, an agent's settings, a record of what they
// approved, the registry of workspaces with the session ids in it.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	return replace(tmp, tmp.Name(), path, data)
}

// WriteAtomicVia is WriteAtomic with the temp file's name given rather than
// invented. tmpPath has to sit in the same directory as path, because a rename
// across filesystems fails, and the caller has to serialise its own writes:
// two of them through one tmpPath would interleave into a single file and then
// rename the result into place.
//
// pkg/state is why it exists. Its writes are already serialised by a flock, and
// a fixed `.opentree/state.json.tmp` is a name the worktree manager refuses to
// hand to a workspace, so a writer that dies mid-write leaves one file the next
// write truncates rather than a fresh piece of litter in the user's repository
// for every crash.
func WriteAtomicVia(path, tmpPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	return replace(tmp, tmpPath, path, data)
}

// replace fills tmp, closes it and moves it over path, clearing up after itself
// wherever that fails.
//
// The Sync is what makes the rename worth doing. Without it the directory entry
// can reach the disk before the bytes do, and a crash in that window leaves the
// new name pointing at a zero-length file — the very loss the temp file was
// there to prevent, except now it has eaten the old contents as well. The
// directory itself is deliberately not synced: a crash that loses the rename
// leaves the old file whole, which is a perfectly good outcome.
//
// The Chmod is not redundant with the open mode. A temp file left behind by an
// older opentree keeps whatever mode it was created with, and O_CREATE on a
// file that already exists does not touch it.
func replace(tmp *os.File, tmpPath, path string, data []byte) error {
	_, err := tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmpPath, 0600)
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
