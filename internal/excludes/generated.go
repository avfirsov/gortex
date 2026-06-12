package excludes

import (
	"path"
	"strings"
)

// generatedSuffixes lists file-name suffixes that mark generated code
// across the languages Gortex indexes. Kept here (a leaf package) so
// both the MCP response-envelope notes and the search rerank pipeline
// share one source of truth without an import cycle.
var generatedSuffixes = []string{
	".pb.go", ".pb.cc", ".pb.h", ".pb.swift", "_pb2.py", "_pb2_grpc.py",
	"_gen.go", ".gen.go", "_generated.go", ".generated.go",
	".g.dart", ".freezed.dart", ".g.cs", ".designer.cs",
}

// IsGenerated reports whether a file name matches a common
// code-generation convention — protobuf stubs, *_gen.go, mocks,
// Kubernetes zz_generated deepcopy, Dart/C# generators, and friends.
// Edits to such files are overwritten by their generator, so callers
// (omission notes, retrieval ranking) treat them as second-class.
func IsGenerated(p string) bool {
	if p == "" {
		return false
	}
	base := strings.ToLower(path.Base(filepathToSlash(p)))
	for _, suf := range generatedSuffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	if strings.HasPrefix(base, "zz_generated") {
		return true
	}
	if strings.HasSuffix(base, ".go") &&
		(strings.HasPrefix(base, "mock_") || strings.HasSuffix(base, "_mock.go")) {
		return true
	}
	return false
}

// GeneratedPeerPaths returns the plausible hand-written peer file
// paths a generated file shadows — the "same-named implementation"
// gate the retrieval ranker uses before down-ranking a generated
// file. For foo.pb.go the peer is foo.go; for mock_user.go it is
// user.go; for user_pb2.py it is user.py.
//
// Returns nil when no clean peer name can be derived (e.g.
// zz_generated.deepcopy.go has no same-named hand-written twin). A
// nil result means "do not gate" — i.e. leave the generated file
// un-penalised, which is the safe default: a generated file that is
// the only definition should not be demoted into oblivion.
func GeneratedPeerPaths(p string) []string {
	if p == "" {
		return nil
	}
	norm := filepathToSlash(p)
	dir := path.Dir(norm)
	base := path.Base(norm)
	lower := strings.ToLower(base)

	join := func(name string) string {
		if dir == "." || dir == "" {
			return name
		}
		return dir + "/" + name
	}

	// Suffix markers: strip the generated marker, swap in the
	// hand-written extension. Ordered longest-first so _pb2_grpc.py
	// wins over _pb2.py and .designer.cs over .cs.
	suffixRules := []struct{ suf, ext string }{
		{"_pb2_grpc.py", ".py"},
		{"_pb2.py", ".py"},
		{".pb.go", ".go"},
		{"_generated.go", ".go"},
		{".generated.go", ".go"},
		{"_gen.go", ".go"},
		{".gen.go", ".go"},
		{"_mock.go", ".go"},
		{".freezed.dart", ".dart"},
		{".g.dart", ".dart"},
		{".designer.cs", ".cs"},
		{".g.cs", ".cs"},
	}
	for _, r := range suffixRules {
		if strings.HasSuffix(lower, r.suf) {
			stem := base[:len(base)-len(r.suf)]
			if stem == "" {
				return nil
			}
			return []string{join(stem + r.ext)}
		}
	}

	// Prefix marker: mock_user.go shadows user.go.
	if strings.HasPrefix(lower, "mock_") && strings.HasSuffix(lower, ".go") {
		rest := base[len("mock_"):]
		if rest != "" && rest != ".go" {
			return []string{join(rest)}
		}
	}
	return nil
}

// filepathToSlash normalises backslashes to forward slashes without
// pulling in path/filepath (which would make the leaf package
// OS-aware). Graph paths are always stored forward-slashed.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
