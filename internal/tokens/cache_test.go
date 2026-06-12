package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("func foo() int { return bar() }\n", 300)
	want := Count(content)

	c := NewDiskCache(dir)
	if got := c.Count(content); got != want { // miss: compute + store
		t.Fatalf("first Count = %d, want %d", got, want)
	}

	// A fresh cache over the same directory must read the persisted
	// entry rather than recompute — the cache survives a restart.
	c2 := NewDiskCache(dir)
	if got := c2.Count(content); got != want {
		t.Errorf("persisted Count = %d, want %d", got, want)
	}
}

func TestDiskCache_HitReturnsStored(t *testing.T) {
	// A hit returns the stored value verbatim: pre-seed a deliberately
	// wrong count and confirm Count trusts the entry over recomputing.
	dir := t.TempDir()
	c := NewDiskCache(dir)
	content := "content the cache will answer for"
	c.write(c.key(content), 999999)
	if got := c.Count(content); got != 999999 {
		t.Errorf("Count = %d, want the stored 999999 (cache not consulted)", got)
	}
}

func TestDiskCache_RevisionIsolation(t *testing.T) {
	dir := t.TempDir()
	content := "content shared across tokenizer revisions"

	old := &DiskCache{dir: dir, revision: "0/old"}
	old.write(old.key(content), 4242)

	// A cache on a different revision must not see the old entry —
	// counts from a different tokenizer are never trusted.
	cur := &DiskCache{dir: dir, revision: "1/cl100k_base"}
	if _, ok := cur.read(cur.key(content)); ok {
		t.Error("revision change must make old entries unreachable")
	}
	// The same revision still hits.
	if n, ok := old.read(old.key(content)); !ok || n != 4242 {
		t.Errorf("same-revision read = (%d,%v), want (4242,true)", n, ok)
	}
}

func TestCachedCount(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)

	small := strings.Repeat("x", minCacheBytes-1)
	large := strings.Repeat("token stream ", 600)

	// CachedCount is value-identical to Count regardless of size.
	if got, want := CachedCount(small), Count(small); got != want {
		t.Errorf("CachedCount(small) = %d, want %d", got, want)
	}
	if got, want := CachedCount(large), Count(large); got != want {
		t.Errorf("CachedCount(large) = %d, want %d", got, want)
	}

	// Only the large input crossed the threshold and was persisted.
	cacheRoot := filepath.Join(xdg, "gortex", "token-counts")
	entries := countCacheEntries(t, cacheRoot)
	if entries == 0 {
		t.Error("a large input should have produced a cache entry")
	}
}

// countCacheEntries counts regular files under root (zero when root is
// absent), used to assert the small-input fast path skips the disk.
func countCacheEntries(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && info.Mode().IsRegular() {
			n++
		}
		return nil
	})
	return n
}
