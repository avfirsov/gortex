package mcp

import (
	"container/list"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// pprWalkCache is a bounded LRU of seeded random-walk (Personalized
// PageRank) results, keyed by the content-addressed walk key derived
// from sorted seeds + restart + per-package Merkle roots (see
// analysis.AdjacencySnapshot.WalkCacheKey).
//
// It is the incremental-RWR cache: because the key embeds the per-
// package content roots, invalidation is implicit. When a package the
// walk depends on changes, the next analysis pass produces a different
// root → a different key → a miss → recompute; unchanged-package walks
// reproduce the same key and hit, even across a snapshot rebuild or a
// daemon restart of the in-memory graph. Stale entries for changed
// packages become unreachable and age out via LRU eviction.
//
// Cached score maps are treated as read-only by every consumer (the
// rerank pipeline rescales into a fresh map; context_closure only reads
// values), so sharing one map across calls is safe without copying.
type pprWalkCache struct {
	mu      sync.Mutex
	ll      *list.List // front = most-recently-used
	m       map[string]*list.Element
	cap     int
	enabled bool

	hits   atomic.Int64
	misses atomic.Int64
}

type pprCacheEntry struct {
	key    string
	scores map[string]float64
}

// newPPRWalkCache constructs the cache from the environment:
//   - GORTEX_PPR_CACHE_DISABLE=1  turn the cache off (always recompute)
//   - GORTEX_PPR_CACHE_SIZE=<n>   max distinct walks retained (default 512)
func newPPRWalkCache() *pprWalkCache {
	c := &pprWalkCache{
		ll:      list.New(),
		m:       make(map[string]*list.Element),
		cap:     512,
		enabled: true,
	}
	if isTruthyEnv(os.Getenv("GORTEX_PPR_CACHE_DISABLE")) {
		c.enabled = false
	}
	if v := strings.TrimSpace(os.Getenv("GORTEX_PPR_CACHE_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.cap = n
		}
	}
	return c
}

// get returns the cached scores for key, promoting it to most-recently-
// used. The second return is false on a miss.
func (c *pprWalkCache) get(key string) (map[string]float64, bool) {
	if c == nil || !c.enabled || key == "" {
		return nil, false
	}
	c.mu.Lock()
	el, ok := c.m[key]
	if ok {
		c.ll.MoveToFront(el)
	}
	c.mu.Unlock()
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return el.Value.(*pprCacheEntry).scores, true
}

// put stores scores under key, evicting the least-recently-used entry
// when the cache is over capacity.
func (c *pprWalkCache) put(key string, scores map[string]float64) {
	if c == nil || !c.enabled || key == "" || len(scores) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[key]; ok {
		el.Value.(*pprCacheEntry).scores = scores
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&pprCacheEntry{key: key, scores: scores})
	c.m[key] = el
	for c.ll.Len() > c.cap {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		delete(c.m, back.Value.(*pprCacheEntry).key)
	}
}

// stats returns a snapshot of cache performance for diagnostics.
func (c *pprWalkCache) stats() (hits, misses int64, size, capacity int, enabled bool) {
	if c == nil {
		return 0, 0, 0, 0, false
	}
	c.mu.Lock()
	size = c.ll.Len()
	c.mu.Unlock()
	return c.hits.Load(), c.misses.Load(), size, c.cap, c.enabled
}
