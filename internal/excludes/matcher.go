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

// HasNegatedDescendant reports whether any re-include ("!") pattern in
// the matcher could match a path strictly beneath relDir.
//
// The index walk prunes an excluded directory with filepath.SkipDir so
// it never descends a subtree it would only throw away. But go-gitignore
// treats "*" as matching across "/", so a blanket like "a/b/*" reports
// the directory "a/b" itself as excluded — pruning it would skip a later
// "!a/b/keep/" re-include before the walk ever reaches the child. This
// lets the walk ask "could a negation resurrect something under here?"
// and keep descending when the answer is yes, mirroring git, which never
// prunes a directory a negation could re-include a child from.
//
// relDir is a repo-root-relative, forward-slash directory path (a
// trailing slash and a leading "./" are tolerated). The check is
// deliberately conservative: an unanchored or wildcard-leading negation
// can match at varying depths, so it is treated as "could be under
// anything" and the directory is kept rather than pruned.
func (m *Matcher) HasNegatedDescendant(relDir string) bool {
	if m == nil {
		return false
	}
	relDir = pathkey.Normalize(filepath.ToSlash(relDir))
	relDir = strings.TrimPrefix(relDir, "./")
	relDir = strings.TrimSuffix(relDir, "/")
	if relDir == "." {
		relDir = ""
	}
	for _, p := range m.patterns {
		if !strings.HasPrefix(p, "!") {
			continue
		}
		np := strings.TrimSpace(p[1:])
		np = strings.TrimPrefix(np, "/")
		np = strings.TrimSuffix(np, "/")
		if np == "" {
			continue
		}
		// A negation with no internal slash is unanchored: gitignore
		// matches it at any depth, so it can re-include something under
		// any directory. Keep descending.
		if !strings.Contains(np, "/") {
			return true
		}
		anchor := literalAnchor(np)
		if anchor == "" {
			// First segment is itself a wildcard ("*/...", "**/..."): it
			// can match at varying depths, so stay conservative.
			return true
		}
		// At the root, every anchored negation lives somewhere beneath us.
		if relDir == "" {
			return true
		}
		// The negation's match-set intersects relDir's subtree when its
		// literal anchor sits at or under relDir, or relDir sits under the
		// anchor (a wildcard tail can then still reach into relDir).
		if anchor == relDir ||
			strings.HasPrefix(anchor, relDir+"/") ||
			strings.HasPrefix(relDir, anchor+"/") {
			return true
		}
	}
	return false
}

// literalAnchor returns the leading path segments of a slash-bearing
// gitignore pattern up to (but excluding) the first segment that holds a
// wildcard meta-character. It returns "" when the first segment is
// itself a wildcard ("*", "**", "?foo", ...).
func literalAnchor(pattern string) string {
	segs := strings.Split(pattern, "/")
	lit := make([]string, 0, len(segs))
	for _, s := range segs {
		if strings.ContainsAny(s, "*?[") {
			break
		}
		lit = append(lit, s)
	}
	return strings.Join(lit, "/")
}
