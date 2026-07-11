package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// gitignoreStat is the cheap staleness key for a repo's root `.gitignore`.
// EffectiveExclude runs on a firehose of calls (every indexer walk and
// per-file reconcile); statting is a single syscall that lets the cache
// skip the open+scan+allocate work unless the file actually changed.
//
// mtime+size is the standard staleness heuristic: the only edit it cannot
// see is a same-second, same-byte-size, different-content rewrite, which is
// astronomically rare in practice and self-heals on the next mtime tick.
type gitignoreStat struct {
	exists  bool
	modTime time.Time
	size    int64
}

// equal compares two stat keys. time.Time is compared with Equal (by
// instant) rather than ==, which would also weigh monotonic-clock and
// location and can report unequal for two readings of the same timestamp.
func (s gitignoreStat) equal(o gitignoreStat) bool {
	return s.exists == o.exists && s.size == o.size && s.modTime.Equal(o.modTime)
}

// statGitignore returns the current staleness key for repoPath's root
// `.gitignore`. A missing or unstattable file yields the zero
// (exists=false) key, matching loadRepoGitignore's "absent → nil" contract.
func statGitignore(repoPath string) gitignoreStat {
	if repoPath == "" {
		return gitignoreStat{}
	}
	info, err := os.Stat(filepath.Join(repoPath, ".gitignore"))
	if err != nil {
		return gitignoreStat{}
	}
	return gitignoreStat{exists: true, modTime: info.ModTime(), size: info.Size()}
}

// gitignoreEntry caches the parsed patterns of one repo's `.gitignore`
// alongside the stat key they were parsed from.
type gitignoreEntry struct {
	stat     gitignoreStat
	patterns []string // shared, immutable — callers must not mutate
}

// mergedEntry caches the fully layered exclude list for one repoPrefix.
// The config pointers pin the inputs by identity: GlobalConfig and the
// per-repo workspace Config are only ever swapped (Reload / LoadWorkspace
// Config), never mutated in place, so an unchanged pointer means unchanged
// content and no rebuild is needed.
type mergedEntry struct {
	gc       *GlobalConfig
	ws       *Config
	repoPath string
	respect  bool
	giStat   gitignoreStat
	merged   []string // clipped, shared, immutable — callers must not mutate
}

// excludeCache memoizes the two per-call allocations ConfigManager.
// EffectiveExclude previously repeated on every invocation:
//
//   - the parsed root `.gitignore`, keyed by repo root path (the dominant
//     cost: a fresh 64 KiB scanner buffer plus a string per line, read on
//     every call);
//   - the fully layered exclude list, keyed by repoPrefix (a fresh merge
//     slice per call).
//
// A steady-state call does one os.Stat and returns shared, immutable
// slices; the underlying file read and slice merge happen only when a
// config pointer or the `.gitignore` stat actually changes.
type excludeCache struct {
	mu        sync.RWMutex
	gitignore map[string]*gitignoreEntry // repo root path → parsed .gitignore
	merged    map[string]*mergedEntry    // repoPrefix → layered excludes
	// readFn reads a repo's root `.gitignore`. nil selects loadRepoGitignore;
	// tests substitute a counting reader to assert cache hits never re-read.
	readFn func(string) []string
}

// excludeCacheCap bounds each map defensively. Keys are normally the
// configured repo set (one per tracked repo), so this is never reached in
// practice; it exists only so a caller passing unbounded arbitrary paths or
// prefixes can never grow the cache without limit. On breach the map is
// cleared and rewarms — simpler than an LRU and safe for a set this small.
const excludeCacheCap = 4096

func newExcludeCache() *excludeCache {
	return &excludeCache{
		gitignore: make(map[string]*gitignoreEntry),
		merged:    make(map[string]*mergedEntry),
	}
}

func (c *excludeCache) read(repoPath string) []string {
	if c.readFn != nil {
		return c.readFn(repoPath)
	}
	return loadRepoGitignore(repoPath)
}

// patterns returns the shared, immutable parsed patterns of repoPath's
// root `.gitignore` for the given (already-computed) stat key, re-reading
// only when the file changed. An absent file returns nil without opening
// anything.
func (c *excludeCache) patterns(repoPath string, st gitignoreStat) []string {
	if !st.exists {
		return nil
	}
	c.mu.RLock()
	e := c.gitignore[repoPath]
	c.mu.RUnlock()
	if e != nil && e.stat.equal(st) {
		return e.patterns
	}
	patterns := c.read(repoPath)
	c.mu.Lock()
	if len(c.gitignore) >= excludeCacheCap {
		c.gitignore = make(map[string]*gitignoreEntry)
	}
	c.gitignore[repoPath] = &gitignoreEntry{stat: st, patterns: patterns}
	c.mu.Unlock()
	return patterns
}

// lookupMerged returns the cached layered exclude list for repoPrefix when
// it is still valid for the given config pointers, respect flag, and
// `.gitignore` stat. The returned slice is shared — callers must not mutate.
func (c *excludeCache) lookupMerged(repoPrefix string, gc *GlobalConfig, ws *Config, repoPath string, respect bool, st gitignoreStat) ([]string, bool) {
	c.mu.RLock()
	e := c.merged[repoPrefix]
	c.mu.RUnlock()
	if e != nil && e.gc == gc && e.ws == ws && e.repoPath == repoPath && e.respect == respect && e.giStat.equal(st) {
		return e.merged, true
	}
	return nil, false
}

// storeMerged records the layered exclude list for repoPrefix. merged must
// be clipped (len == cap) by the caller so a consumer appending to it is
// forced to reallocate and can never write through the shared backing array.
func (c *excludeCache) storeMerged(repoPrefix string, gc *GlobalConfig, ws *Config, repoPath string, respect bool, st gitignoreStat, merged []string) {
	c.mu.Lock()
	if len(c.merged) >= excludeCacheCap {
		c.merged = make(map[string]*mergedEntry)
	}
	c.merged[repoPrefix] = &mergedEntry{
		gc:       gc,
		ws:       ws,
		repoPath: repoPath,
		respect:  respect,
		giStat:   st,
		merged:   merged,
	}
	c.mu.Unlock()
}
