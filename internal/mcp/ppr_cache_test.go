package mcp

import "testing"

func TestPPRWalkCacheLRU(t *testing.T) {
	c := newPPRWalkCache()
	c.cap = 2

	c.put("a", map[string]float64{"x": 1})
	c.put("b", map[string]float64{"x": 2})
	if _, ok := c.get("a"); !ok {
		t.Fatal("a should be present")
	}
	// Touch a (now MRU), then insert c -> b is LRU and evicted.
	c.put("c", map[string]float64{"x": 3})
	if _, ok := c.get("b"); ok {
		t.Fatal("b should have been evicted (LRU)")
	}
	if _, ok := c.get("a"); !ok {
		t.Fatal("a should survive (was recently used)")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("c should be present")
	}
}

func TestPPRWalkCacheStats(t *testing.T) {
	c := newPPRWalkCache()
	c.put("k", map[string]float64{"x": 1})
	if _, ok := c.get("k"); !ok {
		t.Fatal("hit expected")
	}
	if _, ok := c.get("miss"); ok {
		t.Fatal("miss expected")
	}
	hits, misses, size, capacity, enabled := c.stats()
	if hits != 1 || misses != 1 {
		t.Fatalf("hits=%d misses=%d, want 1/1", hits, misses)
	}
	if size != 1 || capacity != 512 || !enabled {
		t.Fatalf("size=%d cap=%d enabled=%v", size, capacity, enabled)
	}
}

func TestPPRWalkCacheDisabled(t *testing.T) {
	t.Setenv("GORTEX_PPR_CACHE_DISABLE", "1")
	c := newPPRWalkCache()
	if c.enabled {
		t.Fatal("cache should be disabled")
	}
	c.put("k", map[string]float64{"x": 1})
	if _, ok := c.get("k"); ok {
		t.Fatal("disabled cache should never hit")
	}
}

func TestPPRWalkCacheEmptyKeyAndScores(t *testing.T) {
	c := newPPRWalkCache()
	c.put("", map[string]float64{"x": 1}) // empty key ignored
	if _, ok := c.get(""); ok {
		t.Fatal("empty key should never store")
	}
	c.put("k", nil) // empty scores ignored
	if _, ok := c.get("k"); ok {
		t.Fatal("empty scores should not be stored")
	}
}
