package mcp

// File reads for the simulation engine live in a dedicated file so
// the os dependency stays out of the main simulation logic (which is
// otherwise pure-graph). The helper is package-private and used only
// by tools_simulate.go.

import "os"

// readSimFile reads a file from disk, returning its contents as a
// string. Errors propagate to the caller, which decides whether to
// treat the file as "absent" (new-file simulation) or to surface a
// hard failure.
func readSimFile(absPath string) (string, error) {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
