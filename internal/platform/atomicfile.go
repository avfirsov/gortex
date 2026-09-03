package platform

import (
	"os"
	"time"
)

// ClaimMarkerSuffix is appended to a path to name the exclusive marker
// ClaimFile and ConsumeFile create while they claim it. A directory sweeper
// recognises a marker abandoned by a process that died mid-claim by this
// suffix; a live marker exists only for the microseconds of the claim.
const ClaimMarkerSuffix = ".claim"

const (
	// A rename, a read or a delete can lose a race with a concurrent holder
	// on Windows; 20 attempts 3ms apart bound the wait at 60ms.
	transientAttempts = 20
	transientDelay    = 3 * time.Millisecond
)

// ReplaceFile moves oldpath onto newpath, replacing an existing newpath.
//
// POSIX rename(2) is atomic: it never fails because somebody holds either end
// open, and concurrent renames onto one destination all report success.
// Windows MoveFileEx(REPLACE_EXISTING) instead fails with a sharing violation
// or "Access is denied" while any handle to either end is open — including one
// held for microseconds by another goroutine that is reading the destination
// or replacing it at the same instant — so on Windows those transient failures
// are retried briefly. A vanished source is never retried: it reports
// ERROR_FILE_NOT_FOUND, which means another caller already moved it and no
// amount of waiting brings it back.
func ReplaceFile(oldpath, newpath string) error {
	return retryTransient(func() error { return os.Rename(oldpath, newpath) })
}

// ClaimFile grants exactly one caller the right to consume path and deletes it
// as the claim. It reports true for that single winner, and false for every
// loser of a concurrent race, for a repeat claim, and for a path that is not
// there.
//
// Neither a rename nor an unlink is a portable single-winner claim. On Windows
// two racers moving one source can both fail with a sharing violation, so the
// resource is claimed by nobody; on macOS concurrent unlink(2) of one path
// reports success to several callers, so it is claimed by everybody. Exclusive
// creation is atomic everywhere, so the marker — not the delete — decides the
// winner, and the delete it guards then runs against no other claimer.
func ClaimFile(path string) bool {
	release, ok := claimMarker(path)
	if !ok {
		return false
	}
	defer release()
	return removeFile(path) == nil
}

// ConsumeFile reads path and claims it, handing the content to exactly one
// caller. ok is false for every loser of a concurrent race, for an
// already-consumed path, and for a file that cannot be read.
func ConsumeFile(path string) ([]byte, bool) {
	release, ok := claimMarker(path)
	if !ok {
		return nil, false
	}
	defer release()
	var data []byte
	err := retryTransient(func() error {
		var readErr error
		data, readErr = os.ReadFile(path) //nolint:gosec // the caller owns the path
		return readErr
	})
	if removeFile(path) != nil || err != nil {
		return nil, false
	}
	return data, true
}

// claimMarker exclusively creates path's claim marker, returning the release
// that removes it again. A caller that dies before releasing leaves the marker
// behind and the claimed file unclaimable, which keeps the at-most-once
// guarantee the claim exists for.
func claimMarker(path string) (func(), bool) {
	if path == "" {
		return nil, false
	}
	markerPath := path + ClaimMarkerSuffix
	marker, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the caller owns the path
	if err != nil {
		return nil, false
	}
	_ = marker.Close()
	return func() { _ = removeFile(markerPath) }, true
}

// removeFile deletes path, absorbing the transient Windows refusals. Go opens
// files without FILE_SHARE_DELETE, so a reader that has the file open for the
// microseconds of its read makes DeleteFile fail with "Access is denied" —
// which on POSIX cannot happen at all, since unlink(2) only detaches the name.
func removeFile(path string) error {
	return retryTransient(func() error { return os.Remove(path) })
}

// retryTransient runs op, repeating it while it fails with a sharing failure
// another holder will shortly release. On POSIX no error is transient, so op
// runs exactly once.
func retryTransient(op func() error) error {
	err := op()
	for attempt := 1; err != nil && attempt < transientAttempts; attempt++ {
		if !transientSharingError(err) {
			break
		}
		time.Sleep(transientDelay)
		err = op()
	}
	return err
}
