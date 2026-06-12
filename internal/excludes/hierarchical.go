package excludes

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Hierarchical matches paths against per-directory ignore files
// discovered along the chain from a repo root down to each path's
// parent directory. Unlike the repo-root-only .gitignore handling, an
// ignore file placed in any directory is honored, with its patterns
// anchored at the directory that contains it — a pattern in
// <root>/sub/.gortexignore constrains only paths under <root>/sub.
//
// Each directory's ignore files are read and compiled once, on first
// request, and cached. A full index walk therefore pays one read per
// directory regardless of how deep the tree is. Hierarchical is safe
// for concurrent use.
type Hierarchical struct {
	root      string
	filenames []string

	mu    sync.RWMutex
	cache map[string]*Matcher // abs dir -> compiled matcher; nil value = directory has no ignore files
}

// NewHierarchical builds a per-directory ignore matcher rooted at root.
// filenames are the ignore-file basenames honored in every directory
// (e.g. ".gortexignore"). An empty filename list yields a matcher that
// excludes nothing.
func NewHierarchical(root string, filenames ...string) *Hierarchical {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Hierarchical{
		root:      filepath.Clean(root),
		filenames: filenames,
		cache:     make(map[string]*Matcher),
	}
}

// Match reports whether an absolute path is excluded by an ignore file
// in any ancestor directory between the root and the path's parent.
// When isDir is true the path is treated as a directory, so trailing-
// slash patterns prune the whole subtree. A path outside the root is
// never excluded.
func (h *Hierarchical) Match(absPath string, isDir bool) bool {
	if h == nil || len(h.filenames) == 0 {
		return false
	}
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(h.root, absPath)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}

	// Test the path against the root's ignore matcher and that of every
	// ancestor directory down to (but excluding) the path itself. Any
	// level that excludes the path wins; a file's own directory cannot
	// exclude the file from itself.
	dir := h.root
	if h.dirMatcher(dir).MatchAbsDir(absPath, dir, isDir) {
		return true
	}
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		dir = filepath.Join(dir, seg)
		if h.dirMatcher(dir).MatchAbsDir(absPath, dir, isDir) {
			return true
		}
	}
	return false
}

// dirMatcher returns the compiled ignore matcher for one directory,
// reading and parsing its ignore files on first request. A directory
// with no ignore files (or only empty ones) caches a nil matcher; the
// *Matcher methods are nil-safe so callers need no guard.
func (h *Hierarchical) dirMatcher(dir string) *Matcher {
	h.mu.RLock()
	m, ok := h.cache[dir]
	h.mu.RUnlock()
	if ok {
		return m
	}

	var patterns []string
	for _, name := range h.filenames {
		patterns = append(patterns, readIgnoreFile(filepath.Join(dir, name))...)
	}
	if len(patterns) > 0 {
		m = New(patterns)
	}

	h.mu.Lock()
	h.cache[dir] = m
	h.mu.Unlock()
	return m
}

// readIgnoreFile reads one ignore file and returns its non-blank,
// non-comment lines as gitignore-syntax patterns. A missing or
// unreadable file yields nil — honoring ignore files is a convenience,
// never a hard requirement, so a missing or permission-denied file
// silently no-ops.
func readIgnoreFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}
