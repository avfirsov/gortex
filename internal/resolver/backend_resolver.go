package resolver

import (
	"os"
	"strings"
)

// backendResolverEnabled reports whether the resolver should consult
// graph.BackendResolver before running its Go-side worker pool.
// Default on for the disk-backed daemon: the backend resolver runs
// one query per rule rather than one round-trip per unresolved edge.
// With the multi-repo encoding exposing 100k+ `unresolved::*` edges
// at warmup, the per-edge Go path is the difference between a sub-
// 10-minute warmup and a hang / OOM. Set GORTEX_BACKEND_RESOLVER=0
// to opt back out for the edge case where a small in-memory corpus
// can be heuristically resolved faster in RAM.
func backendResolverEnabled() bool {
	v := os.Getenv("GORTEX_BACKEND_RESOLVER")
	if v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	return true
}
