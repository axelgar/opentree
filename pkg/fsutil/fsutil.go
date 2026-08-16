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
// approved.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(data)
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
