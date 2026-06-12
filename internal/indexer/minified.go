package indexer

import "bytes"

// Tuning constants for build-artifact detection. The thresholds are
// deliberately conservative — a false positive silently drops a real
// source file, which is worse than indexing the odd bundle.
const (
	// minifiedMinBytes is the floor below which a file is never
	// classified as an artifact — a tiny file is cheap to index and
	// too small to be a meaningful bundle.
	minifiedMinBytes = 2048
	// minifiedAvgLineLen is the average line length above which a
	// JS/TS/CSS file is treated as minified. Hand-written code averages
	// well under 150 chars/line even when dense; a minified bundle
	// averages thousands.
	minifiedAvgLineLen = 500
)

// minifiableLang gates the line-length and bundle-marker heuristics to
// the languages minification actually targets. Sourcemap detection is
// language-agnostic and runs regardless.
var minifiableLang = map[string]bool{
	"javascript": true,
	"typescript": true,
	"css":        true,
}

// minifiedArtifactReason classifies src as a build artifact that
// should not be indexed as source — a sourcemap, a minified bundle, or
// generated code carrying a sourcemap link. It returns a short reason
// ("sourcemap" / "minified" / "bundled") or "" when src is genuine
// source. Detection is high-precision by design: every heuristic keys
// on a signal that does not occur in hand-written code.
func minifiedArtifactReason(lang string, src []byte) string {
	if len(src) < minifiedMinBytes {
		return ""
	}
	if looksLikeSourceMap(src) {
		return "sourcemap"
	}
	if !minifiableLang[lang] {
		return ""
	}
	// A sourceMappingURL annotation is emitted only by build tooling —
	// a hand-written source file never carries one.
	if bytes.Contains(src, []byte("//# sourceMappingURL=")) ||
		bytes.Contains(src, []byte("//@ sourceMappingURL=")) {
		return "bundled"
	}
	if avgLineLength(src) > minifiedAvgLineLen {
		return "minified"
	}
	return ""
}

// looksLikeSourceMap reports whether src is a Source Map v3 document —
// a JSON object carrying "version":3 plus a "mappings" or "sources"
// member. Only the head of the file is inspected.
func looksLikeSourceMap(src []byte) bool {
	head := bytes.TrimLeft(src, " \t\r\n")
	if len(head) == 0 || head[0] != '{' {
		return false
	}
	if len(head) > 4096 {
		head = head[:4096]
	}
	hasVersion := bytes.Contains(head, []byte(`"version":3`)) ||
		bytes.Contains(head, []byte(`"version": 3`))
	hasMap := bytes.Contains(head, []byte(`"mappings"`)) ||
		bytes.Contains(head, []byte(`"sources"`))
	return hasVersion && hasMap
}

// avgLineLength returns the mean number of bytes per line in src.
func avgLineLength(src []byte) int {
	lines := bytes.Count(src, []byte{'\n'}) + 1
	return len(src) / lines
}
