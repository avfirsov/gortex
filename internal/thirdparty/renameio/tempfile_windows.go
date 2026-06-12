//go:build windows
// +build windows

// This file is NOT part of upstream github.com/google/renameio v1.0.1,
// which builds only on non-Windows platforms. It was added by the
// Gortex project to give the vendored package a Windows implementation.
// See README.md in this directory.

package renameio

import (
	"os"
	"path/filepath"
)

// TempDir returns a directory suitable for holding a temporary file that
// will later be renamed over a file in dest's directory. On Windows the
// only requirement is that the temporary file sits on the same volume as
// the destination, so the destination's own directory is always used.
func TempDir(dest string) string {
	return filepath.Dir(dest)
}

// tempDir mirrors the non-Windows helper: a caller-specified directory
// always wins, otherwise the destination's directory is used so the
// later rename stays within one volume.
func tempDir(dir, dest string) string {
	if dir != "" {
		return dir
	}
	return filepath.Dir(dest)
}

// PendingFile is a pending temporary file, waiting to replace the
// destination path in a call to CloseAtomicallyReplace.
type PendingFile struct {
	*os.File

	path   string
	done   bool
	closed bool
}

// Cleanup is a no-op if CloseAtomicallyReplace succeeded, and otherwise
// closes and removes the temporary file.
//
// This method is not safe for concurrent use by multiple goroutines.
func (t *PendingFile) Cleanup() error {
	if t.done {
		return nil
	}
	var closeErr error
	if !t.closed {
		closeErr = t.Close()
	}
	if err := os.Remove(t.Name()); err != nil {
		return err
	}
	return closeErr
}

// CloseAtomicallyReplace closes the temporary file and renames it over
// the destination path. Windows requires a file to be closed before it
// can be renamed; os.Rename maps to MoveFileEx with
// MOVEFILE_REPLACE_EXISTING, so the replacement is atomic with respect
// to other processes opening the destination path.
//
// This method is not safe for concurrent use by multiple goroutines.
func (t *PendingFile) CloseAtomicallyReplace() error {
	if err := t.Sync(); err != nil {
		return err
	}
	t.closed = true
	if err := t.Close(); err != nil {
		return err
	}
	if err := os.Rename(t.Name(), t.path); err != nil {
		return err
	}
	t.done = true
	return nil
}

// TempFile creates a temporary file for atomically creating or replacing
// the destination file at path.
//
// If dir is the empty string, TempDir is used. The file's permissions
// will be 0600 by default; change them with Chmod on the returned
// PendingFile.
func TempFile(dir, path string) (*PendingFile, error) {
	f, err := os.CreateTemp(tempDir(dir, path), "."+filepath.Base(path))
	if err != nil {
		return nil, err
	}
	return &PendingFile{File: f, path: path}, nil
}

// Symlink wraps os.Symlink, replacing an existing symlink with the same
// name atomically. Note that creating symlinks on Windows requires
// either Developer Mode or the SeCreateSymbolicLinkPrivilege; without
// it, os.Symlink's error is surfaced unchanged.
func Symlink(oldname, newname string) error {
	if err := os.Symlink(oldname, newname); err == nil || !os.IsExist(err) {
		return err
	}

	d, err := os.MkdirTemp(filepath.Dir(newname), "."+filepath.Base(newname))
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(d)
		}
	}()

	symlink := filepath.Join(d, "tmp.symlink")
	if err := os.Symlink(oldname, symlink); err != nil {
		return err
	}

	if err := os.Rename(symlink, newname); err != nil {
		return err
	}

	cleanup = false
	return os.RemoveAll(d)
}
