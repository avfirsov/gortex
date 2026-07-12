//go:build windows

package agents

import (
	"errors"
	"syscall"
)

// Windows system error codes returned by MoveFileEx (which os.Rename
// wraps) when the destination path is transiently held open by another
// process without FILE_SHARE_DELETE — an editor's language server,
// antivirus, a search indexer, or Gortex's own file watcher re-indexing
// the file we just wrote. Unlike POSIX, Windows refuses to replace a file
// while such a handle is open. The holder releases within milliseconds,
// so these are safe to retry.
const (
	errSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
	errLockViolation    syscall.Errno = 33 // ERROR_LOCK_VIOLATION
	errAccessDenied     syscall.Errno = 5  // ERROR_ACCESS_DENIED
)

// isRetryableRenameErr reports whether a failed os.Rename is a transient
// Windows sharing/lock violation worth retrying. os.Rename returns a
// *os.LinkError whose Err unwraps to a syscall.Errno.
func isRetryableRenameErr(err error) bool {
	return errors.Is(err, errSharingViolation) ||
		errors.Is(err, errLockViolation) ||
		errors.Is(err, errAccessDenied)
}
