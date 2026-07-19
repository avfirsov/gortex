//go:build !darwin

package indexer

// The ordered startup marker barrier is Darwin-only. Keep the helper defined
// elsewhere so focused platform-independent tests can exercise its fail-closed
// branch without importing platform-specific statfs constants.
func filesystemReadOnly(string) (bool, error) { return false, nil }
