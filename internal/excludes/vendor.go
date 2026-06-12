package excludes

import (
	"path/filepath"
	"strings"
)

// vendorDirNames is the set of directory names that denote vendored
// dependencies or build outputs, derived from Builtin at init time.
// Glob entries (e.g. "*.tmp") are dropped — they identify files, not trees.
var vendorDirNames = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Builtin))
	for _, p := range Builtin {
		if !strings.HasSuffix(p, "/") {
			continue
		}
		name := strings.TrimSuffix(p, "/")
		if strings.ContainsAny(name, "*?!/") {
			continue
		}
		m[name] = struct{}{}
	}
	return m
}()

// IsVendored reports whether a path lives inside a vendored or build-output
// directory tracked by Builtin (vendor/, node_modules/, Pods/, target/,
// .venv/, ...). It lets callers outside the indexer — e.g. the MCP contracts
// tool — apply the same "not our code" judgment without rolling a new list.
//
// Accepts any of: repo-root-relative ("pkg/x.go"), repo-prefixed
// ("repo/pkg/x.go"), or absolute ("/abs/path/pkg/x.go"). The match is
// path-component-wise because the repo root may not be known to the caller.
func IsVendored(path string) bool {
	if path == "" {
		return false
	}
	p := filepath.ToSlash(path)
	for _, seg := range strings.Split(p, "/") {
		if _, ok := vendorDirNames[seg]; ok {
			return true
		}
	}
	return false
}
