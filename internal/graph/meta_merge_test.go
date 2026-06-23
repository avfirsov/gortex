package graph

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetaDelta_OnlyDifferingKeys proves the pure delta calculation:
// metaDelta returns exactly the keys whose value is absent or differs
// from the existing map, across the value shapes that ride on Node.Meta
// (string, []any tag slice, float64, nested map) using reflect.DeepEqual
// so slices/maps compare by contents, not identity. (AC1, pure half.)
func TestMetaDelta_OnlyDifferingKeys(t *testing.T) {
	existing := map[string]any{
		"ext_summary":    "old summary",
		"ext_tags":       []any{"a", "b"},
		"ext_complexity": 0.5,
		"structural":     "untouched",
	}
	kv := map[string]any{
		"ext_summary":    "new summary",          // changed
		"ext_tags":       []any{"a", "b"},        // equal (deep) -> omitted
		"ext_complexity": 0.5,                    // equal -> omitted
		"ext_domain":     "billing",              // new key
		"nested":         map[string]any{"x": 1}, // new nested map
	}

	delta := metaDelta(existing, kv)

	// Telemetry before asserts (LDD spirit).
	t.Logf("[delta] keys=%v", keysOf(delta))

	require.Len(t, delta, 3, "only changed/new keys should appear")
	assert.Equal(t, "new summary", delta["ext_summary"])
	assert.Equal(t, "billing", delta["ext_domain"])
	assert.Equal(t, map[string]any{"x": 1}, delta["nested"])
	_, hasTags := delta["ext_tags"]
	assert.False(t, hasTags, "deep-equal tag slice must be omitted")
	_, hasComplexity := delta["ext_complexity"]
	assert.False(t, hasComplexity, "equal complexity must be omitted")
}

// TestMetaDelta_NilAndEmpty covers the boundary inputs: a nil existing
// map returns every kv entry; an empty/nil kv returns nil.
func TestMetaDelta_NilAndEmpty(t *testing.T) {
	full := metaDelta(nil, map[string]any{"ext_summary": "s", "ext_domain": "d"})
	require.Len(t, full, 2, "nil existing -> every kv key is a delta")

	assert.Nil(t, metaDelta(map[string]any{"k": "v"}, nil), "nil kv -> nil delta")
	assert.Nil(t, metaDelta(nil, map[string]any{}), "empty kv -> nil delta")
}

// TestMergeNodeMeta_MergeOverwriteIdempotent exercises the full
// (changed, found) contract on the in-memory store: new keys merge,
// changed keys overwrite, and a re-apply of identical input is a no-op
// (idempotent). (AC1.)
func TestMergeNodeMeta_MergeOverwriteIdempotent(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "repo/a.go::Foo", Name: "Foo", Kind: KindFunction, FilePath: "repo/a.go"})

	// First merge: two new keys.
	changed, found := g.MergeNodeMeta("repo/a.go::Foo", map[string]any{
		"ext_summary": "computes foo",
		"ext_domain":  "core",
	})
	t.Logf("[merge#1] changed=%v found=%v", changed, found)
	require.True(t, found)
	require.True(t, changed, "first merge writes new keys")
	assert.Equal(t, "computes foo", g.GetNode("repo/a.go::Foo").Meta["ext_summary"])

	// Idempotent re-apply: identical input -> no change.
	changed, found = g.MergeNodeMeta("repo/a.go::Foo", map[string]any{
		"ext_summary": "computes foo",
		"ext_domain":  "core",
	})
	t.Logf("[merge#2 idempotent] changed=%v found=%v", changed, found)
	require.True(t, found)
	assert.False(t, changed, "re-applying identical input must be a no-op")

	// Overwrite: one key changes -> changed=true, only that key updated.
	changed, found = g.MergeNodeMeta("repo/a.go::Foo", map[string]any{
		"ext_summary": "computes foo, revised",
		"ext_domain":  "core", // unchanged
	})
	t.Logf("[merge#3 overwrite] changed=%v found=%v", changed, found)
	require.True(t, found)
	require.True(t, changed, "a differing value flips changed")
	assert.Equal(t, "computes foo, revised", g.GetNode("repo/a.go::Foo").Meta["ext_summary"])
	assert.Equal(t, "core", g.GetNode("repo/a.go::Foo").Meta["ext_domain"])
}

// TestMergeNodeMeta_UnknownIDNoPanic proves an unknown id reports
// found=false (changed=false) and never panics. (AC1, MUST NOT: unknown
// ids must not crash the batch.)
func TestMergeNodeMeta_UnknownIDNoPanic(t *testing.T) {
	g := New()
	changed, found := g.MergeNodeMeta("does/not/exist::Nope", map[string]any{"ext_summary": "x"})
	t.Logf("[unknown] changed=%v found=%v", changed, found)
	assert.False(t, found, "unknown id -> found=false")
	assert.False(t, changed, "unknown id -> changed=false")
}

// TestMergeNodeMeta_LazyInitsNilMeta proves a node created without a
// Meta map gets one lazily on first merge — no nil-map write panic.
func TestMergeNodeMeta_LazyInitsNilMeta(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "repo/b.go::Bar", Name: "Bar", Kind: KindFunction, FilePath: "repo/b.go"})
	require.Nil(t, g.GetNode("repo/b.go::Bar").Meta, "precondition: nil Meta")

	changed, found := g.MergeNodeMeta("repo/b.go::Bar", map[string]any{"ext_summary": "lazy"})
	require.True(t, found)
	require.True(t, changed)
	assert.Equal(t, "lazy", g.GetNode("repo/b.go::Bar").Meta["ext_summary"])
}

// TestMergeNodeMeta_StructuralUntouched proves the merge never alters
// structural node fields. (AC3 / MUST NOT.)
func TestMergeNodeMeta_StructuralUntouched(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "repo/c.go::Baz", Name: "Baz", Kind: KindFunction, FilePath: "repo/c.go", StartLine: 10, EndLine: 20})

	g.MergeNodeMeta("repo/c.go::Baz", map[string]any{"ext_summary": "s", "ext_tags": []any{"t"}})

	n := g.GetNode("repo/c.go::Baz")
	assert.Equal(t, "Baz", n.Name)
	assert.Equal(t, KindFunction, n.Kind)
	assert.Equal(t, "repo/c.go", n.FilePath)
	assert.Equal(t, 10, n.StartLine)
	assert.Equal(t, 20, n.EndLine)
	assert.Equal(t, "s", n.Meta["ext_summary"])
}

// TestMergeNodeMeta_ConcurrentNoRace fans out many MergeNodeMeta and
// AddEdge goroutines against a shared graph. Run under `-race` it proves
// the merge takes the shard write lock correctly: no concurrent-map
// panic, no data race. This is the first mutating tool, so this is the
// load-bearing safety test. (AC2.)
func TestMergeNodeMeta_ConcurrentNoRace(t *testing.T) {
	g := New()
	const nNodes = 32
	for i := 0; i < nNodes; i++ {
		g.AddNode(&Node{
			ID:       fmt.Sprintf("repo/x.go::N%d", i),
			Name:     fmt.Sprintf("N%d", i),
			Kind:     KindFunction,
			FilePath: "repo/x.go",
		})
	}

	const workers = 16
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for it := 0; it < iters; it++ {
				id := fmt.Sprintf("repo/x.go::N%d", (w+it)%nNodes)
				// Concurrent Meta merge (shard-locked) ...
				g.MergeNodeMeta(id, map[string]any{
					"ext_summary": fmt.Sprintf("w%d-it%d", w, it),
					"ext_tags":    []any{"t", fmt.Sprintf("%d", it)},
				})
				// ... interleaved with idempotent edge adds (also
				// shard-locked) to maximise cross-shard lock contention.
				other := fmt.Sprintf("repo/x.go::N%d", (w+it+1)%nNodes)
				g.AddEdge(&Edge{
					From:   id,
					To:     other,
					Kind:   EdgeSemanticallyRelated,
					Origin: "ext_annotated",
				})
			}
		}(w)
	}
	wg.Wait()

	t.Logf("[concurrency] nodes=%d edges=%d", g.NodeCount(), g.EdgeCount())
	// Every node should carry a ext_summary from the last writer that
	// touched it; the exact value is non-deterministic, presence is not.
	for i := 0; i < nNodes; i++ {
		n := g.GetNode(fmt.Sprintf("repo/x.go::N%d", i))
		require.NotNil(t, n)
		assert.Contains(t, n.Meta, "ext_summary")
	}
}

// keysOf is a tiny test helper for stable delta-key logging.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
