// Package pathkey canonicalises filesystem paths to a single Unicode
// form so that every subsystem that keys, compares, or deduplicates a
// path agrees on its byte representation regardless of where the path
// came from.
//
// The problem this solves is platform-dependent Unicode normalisation.
// A file named "café.go" or "日本語.go" has no single byte encoding:
//
//   - macOS APFS / HFS+ decompose filenames to NFD ("e" + combining
//     acute) — so filepath.WalkDir and the FSEvents watcher hand back
//     decomposed bytes.
//   - Linux filesystems preserve the bytes as written, which for a
//     file created from git or a Linux editor is almost always NFC
//     (precomposed "é").
//   - git itself never normalises: `git diff` emits paths exactly as
//     the bytes were committed, i.e. typically NFC, even on macOS.
//
// So the *same* file can present as two different byte sequences
// within one process: the filesystem walk stores one form, the git
// watcher reports another. A graph node keyed under one form is then
// invisible to a lookup made with the other — lost symbols, missed
// watcher events, snapshot-key misses that trigger a full re-index.
//
// The fix is to fold every path to one canonical form at every keying
// boundary. NFC is chosen as the target: it is what git stores, what
// the W3C and IETF recommend for interchange, and the form Linux
// repositories already carry, so normalising costs nothing on the
// common path and only repairs macOS's decomposed form.
package pathkey

import "golang.org/x/text/unicode/norm"

// Normalize returns p folded to Unicode NFC. An all-ASCII path — the
// overwhelmingly common case — is returned unchanged without invoking
// the normaliser, so the helper is free on hot indexing paths. A path
// already in NFC is likewise returned as-is by norm.NFC.String, which
// allocates nothing when no rune needs recomposing.
//
// Normalize only touches Unicode composition; it does not clean,
// resolve, or change the separator style of the path. Callers that
// also want filepath.Clean / ToSlash semantics apply those separately
// — the two concerns are deliberately kept independent.
func Normalize(p string) string {
	if isASCII(p) {
		return p
	}
	return norm.NFC.String(p)
}

// Equal reports whether two paths denote the same path once both are
// folded to NFC. Use this instead of a raw == when either operand may
// have come from a different platform or from git rather than from a
// filesystem walk.
func Equal(a, b string) bool {
	if a == b {
		return true
	}
	return Normalize(a) == Normalize(b)
}

// isASCII reports whether s contains only bytes < 0x80. Such a string
// is identical in every Unicode normal form, so it can skip the
// normaliser entirely. Inlined as a tight byte loop rather than
// ranging runes to avoid UTF-8 decoding on the hot path.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
