//go:build !windows

package agents

// isRetryableRenameErr is always false off Windows: a POSIX rename
// atomically replaces the destination even while another process holds it
// open, so a failed rename there is a genuine, non-transient error (e.g.
// an EXDEV cross-device link) that retrying would not fix.
func isRetryableRenameErr(error) bool { return false }
