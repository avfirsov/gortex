//go:build windows

package daemon

// isEMFILE reports whether err is a file-descriptor-exhaustion error.
// Windows has no RLIMIT_NOFILE and its socket layer surfaces exhaustion
// differently, so the accept loop never takes the EMFILE branch there —
// this is a constant false.
func isEMFILE(error) bool {
	return false
}

// FDLimit mirrors the Unix struct so callers compile unchanged. On
// Windows both fields stay zero — there is no per-process descriptor
// cap to report.
type FDLimit struct {
	Soft uint64
	Hard uint64
}

// RaiseFDLimit is a no-op on Windows: the OS imposes no RLIMIT_NOFILE
// equivalent on a user process, so there is nothing to raise.
func RaiseFDLimit() (FDLimit, error) {
	return FDLimit{}, nil
}
