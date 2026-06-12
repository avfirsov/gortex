package store_sqlite

import (
	"path/filepath"
	"sync"

	"github.com/zzet/gortex/internal/graph"
)

// bundleCacheMaxEntries bounds how many per-node bundle entries the
// cache holds. When the cap is reached the cache is cleared wholesale
// rather than evicting individually — the entries are cheap to recompute
// (one batched fetch) and a wholesale clear keeps the bookkeeping O(1)
// and free of an LRU's per-entry overhead. The cap is generous: a
// half-million-symbol monorepo's hottest few thousand search hits fit
// comfortably under it.
const bundleCacheMaxEntries = 50000

// bundleCacheEntry is one node's cached bundle, tagged with the package
// it belongs to and the package fingerprint that was current when the
// bundle was computed. The entry is served only while
// fingerprints[pkgKey] still equals fp — any change to the package's
// content (a node or edge added / removed / reweighted, including a
// cross-file edge that lands on this node from elsewhere) moves the
// fingerprint and forces a recompute, so a cached bundle can never
// carry a stale edge.
type bundleCacheEntry struct {
	pkgKey string
	fp     uint64
	bundle graph.SymbolBundle
}

// bundleCache is a content-addressed, package-scoped cache over
// SearchSymbolBundles. It is keyed at the node level but validated at
// the package level: an entry is fresh exactly when the package's
// current fingerprint matches the fingerprint the entry was stored at.
//
// Correctness rests entirely on the fingerprint discipline: the daemon
// hands the cache an authoritative per-package fingerprint map after
// every analysis pass (which runs after every incremental reindex and
// every edit_file / fsnotify-driven graph mutation). The fingerprints
// are edge-aware — they fold every package's nodes AND the edges
// touching them — so any mutation that could change a cached bundle's
// in/out edges moves the relevant package fingerprint and invalidates
// the entry. A package whose fingerprint is unchanged is served from
// cache; a package the daemon has never reported a fingerprint for is
// always treated as a miss (conservative: never serve an unvalidated
// bundle).
type bundleCache struct {
	mu           sync.Mutex
	fingerprints map[string]uint64
	entries      map[string]*bundleCacheEntry
}

// SetBundleFingerprints installs the authoritative per-package
// fingerprint map and drops any cached entry whose package fingerprint
// has changed (or whose package is no longer reported). This is the
// invalidation entry point: the daemon calls it after each analysis
// pass with the fresh fingerprints derived from the live graph, so a
// reindex that altered a package's nodes or edges retires exactly the
// affected bundles while leaving untouched packages cached.
//
// fps is keyed by package key (the directory the package's files live
// in, repo-prefixed in multi-repo because the node file paths are).
func (s *Store) SetBundleFingerprints(fps map[string]uint64) {
	if s.bundles == nil {
		return
	}
	s.bundles.refresh(fps)
}

// refresh swaps in the new fingerprint map and prunes every entry whose
// package fingerprint no longer matches.
func (c *bundleCache) refresh(fps map[string]uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fps == nil {
		fps = map[string]uint64{}
	}
	c.fingerprints = fps
	for id, e := range c.entries {
		cur, ok := fps[e.pkgKey]
		if !ok || cur != e.fp {
			delete(c.entries, id)
		}
	}
}

// bundlePackageKey derives the package key for a node's file path. It
// mirrors the analysis layer's packageKey so the cache and the
// daemon-supplied fingerprint map agree on package identity: the
// directory the file lives in (repo-prefixed in multi-repo because the
// stored file paths are), or "" for a file at the repo root / a node
// with no path.
func bundlePackageKey(filePath string) string {
	if filePath == "" {
		return ""
	}
	dir := filepath.Dir(filepath.ToSlash(filePath))
	if dir == "." {
		return ""
	}
	return dir
}

// lookup returns the cached bundle for id when it is fresh — the entry
// exists and its package fingerprint still matches the current one. A
// node whose package has no reported fingerprint is never served (ok is
// false) so an unvalidated bundle can never escape the cache.
func (c *bundleCache) lookup(id string) (graph.SymbolBundle, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok {
		return graph.SymbolBundle{}, false
	}
	cur, ok := c.fingerprints[e.pkgKey]
	if !ok || cur != e.fp {
		// Stale or unvalidated — drop it so a later refresh doesn't
		// have to.
		delete(c.entries, id)
		return graph.SymbolBundle{}, false
	}
	return e.bundle, true
}

// store records a freshly computed bundle, tagged with its package's
// current fingerprint. A node whose package has no reported fingerprint
// is NOT cached (it could not be validated on read-back), keeping the
// cache conservative. When the cap is exceeded the cache is cleared
// wholesale before the insert.
func (c *bundleCache) store(b graph.SymbolBundle) {
	if b.Node == nil {
		return
	}
	pkgKey := bundlePackageKey(b.Node.FilePath)
	c.mu.Lock()
	defer c.mu.Unlock()
	fp, ok := c.fingerprints[pkgKey]
	if !ok {
		return
	}
	if len(c.entries) >= bundleCacheMaxEntries {
		c.entries = make(map[string]*bundleCacheEntry, bundleCacheMaxEntries)
	}
	c.entries[b.Node.ID] = &bundleCacheEntry{pkgKey: pkgKey, fp: fp, bundle: b}
}
