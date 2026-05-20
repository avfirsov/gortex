// Package intern provides a process-wide string interning table.
//
// The knowledge graph stores a node's ID once on the node, but also
// once on every edge endpoint that references it, and a file path once
// per node defined in that file. Under a multi-repo warmup those
// references are minted as fresh `repoPrefix + "/" + id` concatenations
// — a heap profile attributed ~425 MB of resident memory to that
// concatenation alone. Interning collapses every duplicate of a string
// to a single shared backing array.
//
// The table is process-global and never shrinks: an interned string
// lives until process exit even if every repo referencing it is later
// evicted. This is a deliberate trade — node IDs and file paths are
// low-cardinality and heavily duplicated, so the bounded table costs
// far less than the duplication it removes.
package intern

import "sync"

// shardCount fans interning lookups across independent locks so the
// parallel warmup workers (one goroutine per repo) do not serialise on
// a single mutex. 64 keeps per-shard maps small without waste.
const shardCount = 64

type shard struct {
	mu sync.RWMutex
	m  map[string]string
}

var shards [shardCount]*shard

func init() {
	for i := range shards {
		shards[i] = &shard{m: make(map[string]string)}
	}
}

// shardFor picks a shard by FNV-1a hash of s — inlined rather than
// using hash/fnv to avoid a hash-object allocation on every call.
func shardFor(s string) *shard {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return shards[h%shardCount]
}

// String returns the canonical instance of s. Every call with an equal
// string returns the same backing array, so the caller may drop its
// own copy. The empty string is returned as-is without touching the
// table. Safe for concurrent use.
func String(s string) string {
	if s == "" {
		return ""
	}
	sh := shardFor(s)
	sh.mu.RLock()
	c, ok := sh.m[s]
	sh.mu.RUnlock()
	if ok {
		return c
	}
	sh.mu.Lock()
	if c, ok = sh.m[s]; !ok {
		sh.m[s] = s
		c = s
	}
	sh.mu.Unlock()
	return c
}

// Len reports the number of distinct strings currently interned.
// Intended for diagnostics and tests.
func Len() int {
	n := 0
	for _, sh := range shards {
		sh.mu.RLock()
		n += len(sh.m)
		sh.mu.RUnlock()
	}
	return n
}
