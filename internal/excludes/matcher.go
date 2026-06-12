package excludes

import (
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"

	"github.com/zzet/gortex/internal/pathkey"
)

// Matcher tests whether a path should be excluded from indexing/watching.
// It is safe for concurrent reads after construction.
type Matcher struct {
	ign      *ignore.GitIgnore
	patterns []string
}

// New compiles the given patterns into a Matcher. A nil/empty list is
// valid and will match nothing.
//
// Patterns are folded to Unicode NFC so a pattern naming a non-ASCII
// directory matches paths regardless of which Unicode form the
// filesystem walk produced — MatchRel folds the candidate path to the
// same form before testing it.
func New(patterns []string) *Matcher {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		cleaned = append(cleaned, pathkey.Normalize(p))
	}
	return &Matcher{
		ign:      ignore.CompileIgnoreLines(cleaned...),
		patterns: cleaned,
	}
}

// Patterns returns the cleaned pattern list (empties and comments removed).
func (m *Matcher) Patterns() []string {
	if m == nil {
		return nil
	}
	out := make([]string, len(m.patterns))
	copy(out, m.patterns)
	return out
}

// MatchRel reports whether a repo-root-relative path is excluded.
// Path separators are normalised to forward slashes and the path is
// folded to Unicode NFC — matching how New normalised the patterns —
// before matching, so a non-ASCII path component compares equal to its
// pattern whether the OS supplied it decomposed (macOS NFD) or
// precomposed (Linux / git NFC).
func (m *Matcher) MatchRel(relPath string) bool {
	if m == nil || m.ign == nil {
		return false
	}
	rel := pathkey.Normalize(filepath.ToSlash(relPath))
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." {
		return false
	}
	return m.ign.MatchesPath(rel)
}

// MatchAbs reports whether an absolute path under root is excluded.
// Returns false if path is not under root.
func (m *Matcher) MatchAbs(absPath, root string) bool {
	return m.MatchAbsDir(absPath, root, false)
}

// MatchAbsDir reports whether an absolute path under root is excluded.
// When isDir is true the path is treated as a directory, so a pattern
// written with a trailing slash (e.g. "build/") matches the directory
// itself — letting the caller prune the whole subtree instead of
// descending it and re-testing every file. Returns false if path is
// not under root.
func (m *Matcher) MatchAbsDir(absPath, root string, isDir bool) bool {
	if m == nil || m.ign == nil {
		return false
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return false
	}
	if isDir {
		rel += "/"
	}
	return m.MatchRel(rel)
}
