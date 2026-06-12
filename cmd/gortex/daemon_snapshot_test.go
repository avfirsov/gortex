package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// TestSnapshotRoundTrip proves that save + load preserves nodes and
// edges bit-for-bit. This is the guarantee the daemon's startup restore
// depends on; a silent corruption here would give warm-started daemons
// a stale graph that doesn't match any real source file.
func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "snap.gob.gz"))

	orig := graph.New()
	orig.AddNode(&graph.Node{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go"})
	orig.AddNode(&graph.Node{ID: "b.go::Bar", Name: "Bar", Kind: graph.KindMethod, FilePath: "b.go"})
	orig.AddEdge(&graph.Edge{From: "b.go::Bar", To: "a.go::Foo", Kind: graph.EdgeCalls, FilePath: "b.go", Line: 12})

	saveSnapshot(orig, nil, nil, snapshotVector{}, version, zap.NewNop())

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded, "loadSnapshot must succeed for a freshly-written file")

	assert.Equal(t, orig.NodeCount(), restored.NodeCount(),
		"node count must round-trip")
	assert.Equal(t, orig.EdgeCount(), restored.EdgeCount(),
		"edge count must round-trip")

	n := restored.GetNode("a.go::Foo")
	require.NotNil(t, n)
	assert.Equal(t, "Foo", n.Name)
}

// buildTestVectorIndex builds a small HNSW vector index and returns it
// serialized as a snapshotVector — the shape the daemon snapshot path
// persists.
func buildTestVectorIndex(t *testing.T) snapshotVector {
	t.Helper()
	const dims = 4
	vec := search.NewVector(dims)
	vec.Add("a.go::Foo", []float32{1, 0, 0, 0})
	vec.Add("b.go::Bar", []float32{0, 1, 0, 0})
	vec.Add("c.go::Baz", []float32{0, 0, 1, 0})

	var buf bytes.Buffer
	require.NoError(t, vec.Save(&buf))
	return snapshotVector{Index: buf.Bytes(), Dims: dims, Count: vec.Count()}
}

// TestSnapshotRoundTrip_VectorIndex proves the schema-v3 vector-index
// fields survive a save + load cycle. Without this the daemon would
// re-embed the whole graph on every restart.
func TestSnapshotRoundTrip_VectorIndex(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "snap.gob.gz"))

	orig := graph.New()
	orig.AddNode(&graph.Node{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go"})

	want := buildTestVectorIndex(t)
	saveSnapshot(orig, nil, nil, want, version, zap.NewNop())

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded)

	got := result.Vector
	assert.Equal(t, want.Dims, got.Dims, "vector dims must round-trip")
	assert.Equal(t, want.Count, got.Count, "vector count must round-trip")
	require.Equal(t, want.Index, got.Index, "serialized vector index bytes must round-trip")

	// The restored bytes must rehydrate into a usable HNSW index.
	rebuilt := search.NewVector(got.Dims)
	require.NoError(t, rebuilt.LoadFrom(bytes.NewReader(got.Index)))
	rebuilt.SetCount(got.Count)
	hits := rebuilt.Search([]float32{1, 0, 0, 0}, 1)
	require.Len(t, hits, 1)
	assert.Equal(t, "a.go::Foo", hits[0], "restored index must answer nearest-neighbour queries")
}

// TestMigrateSnapshotV2toV3 proves the v2→v3 migration re-stamps the
// schema version and preserves every node / edge / repo / contract
// record. The record shapes are identical across the two versions, so
// a v2 stream encoded with the current types and SchemaVersion=2
// faithfully stands in for a real on-disk v2 snapshot.
func TestMigrateSnapshotV2toV3(t *testing.T) {
	var v2 bytes.Buffer
	enc := gob.NewEncoder(&v2)
	hdr := snapshotHeader{
		SchemaVersion: 2,
		Version:       version,
		NodeCount:     2,
		EdgeCount:     1,
		RepoCount:     1,
	}
	require.NoError(t, enc.Encode(hdr))
	require.NoError(t, enc.Encode(graph.Node{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go"}))
	require.NoError(t, enc.Encode(graph.Node{ID: "b.go::Bar", Name: "Bar", Kind: graph.KindMethod, FilePath: "b.go"}))
	require.NoError(t, enc.Encode(graph.Edge{From: "b.go::Bar", To: "a.go::Foo", Kind: graph.EdgeCalls}))
	require.NoError(t, enc.Encode(snapshotRepo{RepoPrefix: "repo", RootPath: "/tmp/repo"}))

	var v3 bytes.Buffer
	require.NoError(t, migrateSnapshotV2toV3(&v2, &v3))

	dec := gob.NewDecoder(&v3)
	var migrated snapshotHeader
	require.NoError(t, dec.Decode(&migrated))
	assert.Equal(t, 3, migrated.SchemaVersion, "migration must bump the schema version")
	assert.Equal(t, 2, migrated.NodeCount, "node count must survive")
	assert.Equal(t, 1, migrated.RepoCount, "repo count must survive")

	for i := 0; i < migrated.NodeCount; i++ {
		var n graph.Node
		require.NoError(t, dec.Decode(&n), "every node record must decode from the migrated stream")
		assert.NotEmpty(t, n.ID)
	}
	for i := 0; i < migrated.EdgeCount; i++ {
		var e graph.Edge
		require.NoError(t, dec.Decode(&e))
		assert.Equal(t, "b.go::Bar", e.From)
	}
	for i := 0; i < migrated.RepoCount; i++ {
		var r snapshotRepo
		require.NoError(t, dec.Decode(&r))
		assert.Equal(t, "repo", r.RepoPrefix)
	}
}

// TestSnapshotRoundTrip_NoVectorIndex asserts a snapshot written with
// no vector data loads with an empty snapshotVector — the embeddings-
// disabled path must not synthesise a phantom index.
func TestSnapshotRoundTrip_NoVectorIndex(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "snap.gob.gz"))

	orig := graph.New()
	orig.AddNode(&graph.Node{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go"})
	saveSnapshot(orig, nil, nil, snapshotVector{}, version, zap.NewNop())

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded)

	assert.Nil(t, result.Vector.Index, "no vector data written → none restored")
	assert.Zero(t, result.Vector.Count)
}

// TestLoadSnapshot_DropsStaleAbsPathNodes guards against the T0.2 symptom
// of duplicate symbols leaking across daemon sessions. Prior-version code
// paths occasionally stored nodes with absolute filesystem paths as their
// IDs; those nodes persisted in snapshots and were restored forever
// alongside the correctly-prefixed nodes produced by current indexing.
// loadSnapshot must detect the stale shape and drop it so re-indexing
// yields a single canonical node per symbol.
func TestLoadSnapshot_DropsStaleAbsPathNodes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "snap.gob.gz"))

	orig := graph.New()
	// Clean, current-shape node — should be restored.
	orig.AddNode(&graph.Node{
		ID:   "core-api/api/handler.go::Handler.CreateTuck",
		Name: "CreateTuck", Kind: graph.KindMethod,
		FilePath: "core-api/api/handler.go", RepoPrefix: "core-api",
	})
	// Stale abs-path node — a duplicate of the clean one, must be dropped.
	orig.AddNode(&graph.Node{
		ID:   "/Users/me/tuck/core-api/api/handler.go::Handler.CreateTuck",
		Name: "CreateTuck", Kind: graph.KindMethod,
		FilePath: "/Users/me/tuck/core-api/api/handler.go",
	})
	// Edge pointing at the stale node — must be dropped too so the
	// restored graph contains no dangling references.
	orig.AddEdge(&graph.Edge{
		From: "core-api/api/handler.go::Handler.RegisterRoutes",
		To:   "/Users/me/tuck/core-api/api/handler.go::Handler.CreateTuck",
		Kind: graph.EdgeCalls,
	})
	// Edge between two clean nodes — must survive.
	orig.AddNode(&graph.Node{
		ID:   "core-api/api/handler.go::Handler.RegisterRoutes",
		Name: "RegisterRoutes", Kind: graph.KindMethod,
		FilePath: "core-api/api/handler.go", RepoPrefix: "core-api",
	})
	orig.AddEdge(&graph.Edge{
		From: "core-api/api/handler.go::Handler.RegisterRoutes",
		To:   "core-api/api/handler.go::Handler.CreateTuck",
		Kind: graph.EdgeCalls,
	})

	saveSnapshot(orig, nil, nil, snapshotVector{}, version, zap.NewNop())

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded)

	assert.NotNil(t, restored.GetNode("core-api/api/handler.go::Handler.CreateTuck"),
		"clean prefixed node must be restored")
	assert.Nil(t, restored.GetNode("/Users/me/tuck/core-api/api/handler.go::Handler.CreateTuck"),
		"stale abs-path node must be dropped on load")

	for _, e := range restored.AllEdges() {
		assert.False(t, strings.HasPrefix(e.From, "/"),
			"edge From references a dropped stale node: %s → %s", e.From, e.To)
		assert.False(t, strings.HasPrefix(e.To, "/"),
			"edge To references a dropped stale node: %s → %s", e.From, e.To)
	}
}

// TestSnapshotRoundTrip_NodeMetaWithShape guards against regressing on
// gob-registration of *contracts.Shape. The indexer attaches a Shape to
// contract-referenced type nodes via Meta["shape"]; without the Register
// call, saveSnapshot aborts on the first such node with "type not
// registered for interface: contracts.Shape" and the snapshot file is
// never written.
func TestSnapshotRoundTrip_NodeMetaWithShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	orig := graph.New()
	orig.AddNode(&graph.Node{
		ID:       "a.go::Req",
		Name:     "Req",
		Kind:     graph.KindType,
		FilePath: "a.go",
		Meta: map[string]any{
			"shape": &contracts.Shape{
				Kind: "struct",
				Fields: []contracts.ShapeField{
					{Name: "ID", Type: "string", Required: true},
				},
			},
		},
	})

	saveSnapshot(orig, nil, nil, snapshotVector{}, version, zap.NewNop())

	info, err := os.Stat(path)
	require.NoError(t, err, "snapshot file must exist — encode must not have aborted")
	require.Greater(t, info.Size(), int64(0), "snapshot must not be empty")

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded)

	n := restored.GetNode("a.go::Req")
	require.NotNil(t, n)
	require.NotNil(t, n.Meta["shape"], "shape must survive round-trip")

	shape, ok := n.Meta["shape"].(*contracts.Shape)
	require.True(t, ok, "decoded shape must keep its concrete *contracts.Shape type, got %T", n.Meta["shape"])
	assert.Equal(t, "struct", shape.Kind)
	require.Len(t, shape.Fields, 1)
	assert.Equal(t, "ID", shape.Fields[0].Name)
}

// TestSnapshotRoundTrip_NodeMetaWithGenericTypes guards the gob
// registration for the parser-side Meta values that aren't covered by
// the default map[string]any/[]any/[]string set: type_params (Go/TS/Rust
// generics, []map[string]string) and status_codes (HTTP contracts, []int).
// Without the registration, saveSnapshot aborts on the first such node
// with "type not registered for interface: []map[string]string" and the
// snapshot file is never written — a silent regression because
// saveSnapshot only logs.
func TestSnapshotRoundTrip_NodeMetaWithGenericTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	orig := graph.New()
	orig.AddNode(&graph.Node{
		ID:       "a.go::Generic",
		Name:     "Generic",
		Kind:     graph.KindFunction,
		FilePath: "a.go",
		Meta: map[string]any{
			"type_params": []map[string]string{
				{"name": "T", "bound": "comparable"},
				{"name": "U"},
			},
			"status_codes": []int{200, 404, 500},
		},
	})

	saveSnapshot(orig, nil, nil, snapshotVector{}, version, zap.NewNop())

	info, err := os.Stat(path)
	require.NoError(t, err, "snapshot file must exist — encode must not have aborted")
	require.Greater(t, info.Size(), int64(0), "snapshot must not be empty")

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded)

	n := restored.GetNode("a.go::Generic")
	require.NotNil(t, n)

	tp, ok := n.Meta["type_params"].([]map[string]string)
	require.True(t, ok, "type_params must keep its concrete []map[string]string type, got %T", n.Meta["type_params"])
	require.Len(t, tp, 2)
	assert.Equal(t, "T", tp[0]["name"])
	assert.Equal(t, "comparable", tp[0]["bound"])

	sc, ok := n.Meta["status_codes"].([]int)
	require.True(t, ok, "status_codes must keep its concrete []int type, got %T", n.Meta["status_codes"])
	assert.Equal(t, []int{200, 404, 500}, sc)
}

// TestSnapshotRoundTrip_ContractMetaWithSliceOfMaps guards the gob
// registration of []map[string]any. The HTTP schema enricher writes a
// response_envelope value of that shape into Contract.Meta; without the
// Register call, saveSnapshot aborts on the first such contract with
// "type not registered for interface: []map[string]interface {}" and the
// snapshot file never lands.
func TestSnapshotRoundTrip_ContractMetaWithSliceOfMaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	orig := graph.New()
	snapContracts := []snapshotContract{{
		ID:         "repo/api.go::POST /tasks",
		Type:       "http",
		Role:       "provider",
		SymbolID:   "repo/api.go::CreateTask",
		FilePath:   "repo/api.go",
		Line:       42,
		RepoPrefix: "repo",
		Meta: map[string]any{
			"path": "/tasks",
			"response_envelope": []map[string]any{
				{"name": "id", "type": "string"},
				{"name": "items", "type": "Task", "repeated": true},
			},
		},
	}}

	saveSnapshot(orig, nil, snapContracts, snapshotVector{}, version, zap.NewNop())

	info, err := os.Stat(path)
	require.NoError(t, err, "snapshot file must exist — encode must not have aborted on []map[string]any")
	require.Greater(t, info.Size(), int64(0))

	restored := graph.New()
	result, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, result.Loaded)

	cs, ok := result.Contracts["repo"]
	require.True(t, ok, "contracts for repo prefix must round-trip")
	require.Len(t, cs, 1)
	env, ok := cs[0].Meta["response_envelope"].([]map[string]any)
	require.True(t, ok, "response_envelope must keep its []map[string]any type, got %T", cs[0].Meta["response_envelope"])
	require.Len(t, env, 2)
	assert.Equal(t, "id", env[0]["name"])
	assert.Equal(t, true, env[1]["repeated"])
}

func TestLoadSnapshot_MissingFile_NotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "nope.gob.gz"))

	g := graph.New()
	result, err := loadSnapshot(g, zap.NewNop())
	require.NoError(t, err, "missing snapshot must not surface as an error — first-run path")
	assert.False(t, result.Loaded, "no snapshot means loaded=false")
	assert.Equal(t, 0, g.NodeCount())
}

// Regression: a snapshot written by an older binary must not be loaded
// silently. Earlier symptoms — methods like (*Node).Inner showing zero
// callers even though the source has 19 call sites — traced back to the
// resolver having improved between daemon versions while the persisted
// edges kept their older (wrong) targets forever. The version field on
// the snapshot header is what protects us; this test pins that gate.
func TestLoadSnapshot_BinaryVersionMismatch_Discards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "older.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	// Write a snapshot stamped with a different binary version.
	src := graph.New()
	src.AddNode(&graph.Node{ID: "a.go::Foo", Kind: graph.KindFunction, Name: "Foo", FilePath: "a.go", Language: "go"})
	require.NoError(t, saveSnapshotTo(src, nil, nil, snapshotVector{}, "0.0.0-from-an-older-daemon", path, zap.NewNop()))

	g := graph.New()
	result, err := loadSnapshot(g, zap.NewNop())
	require.NoError(t, err, "version mismatch is not an error — daemon falls back to full re-index")
	assert.False(t, result.Loaded, "older-binary snapshot must not be loaded into a newer daemon")
	assert.Equal(t, 0, g.NodeCount(), "graph must remain empty so the warmup path re-indexes from source")
}

// Round-trip with the current binary's version stamp must succeed —
// version-gating mustn't break the normal "same-binary" path.
func TestLoadSnapshot_SameBinaryVersion_Loads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "same.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	src := graph.New()
	src.AddNode(&graph.Node{ID: "a.go::Foo", Kind: graph.KindFunction, Name: "Foo", FilePath: "a.go", Language: "go"})
	require.NoError(t, saveSnapshotTo(src, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	g := graph.New()
	result, err := loadSnapshot(g, zap.NewNop())
	require.NoError(t, err)
	assert.True(t, result.Loaded)
	assert.Equal(t, 1, g.NodeCount())
}

// Regression: when a snapshot's persisted resolution state is corrupt
// (lots of edges point at node IDs that no longer exist), the loader
// must discard it entirely instead of returning a half-graph that the
// daemon then layers incremental indexing on top of. We saw this in
// the wild — `(*Node).Type` and `WriteIfNotExists` ended up with zero
// callers in the daemon's graph despite obviously having dozens in
// source, because consecutive restarts kept loading and re-saving an
// increasingly degraded snapshot.
func TestLoadSnapshot_CorruptionDetected_ForcesFreshIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	// Build a snapshot with 200 nodes and 200 edges pointing at IDs
	// that don't exist. >5% drop ratio + EdgeCount > 100 → corrupt path.
	orig := graph.New()
	for i := 0; i < 200; i++ {
		orig.AddNode(&graph.Node{
			ID: fmt.Sprintf("repo/a.go::Sym%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("Sym%d", i), FilePath: "repo/a.go", Language: "go",
			RepoPrefix: "repo", // real graphs always set this; tests must too
		})
		orig.AddEdge(&graph.Edge{
			From: fmt.Sprintf("repo/a.go::Sym%d", i),
			To:   fmt.Sprintf("ghost.go::Ghost%d", i), // target node never exists
			Kind: graph.EdgeCalls,
		})
	}
	require.NoError(t, saveSnapshotTo(orig, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	g := graph.New()
	result, err := loadSnapshot(g, zap.NewNop())
	require.NoError(t, err)
	assert.False(t, result.Loaded, "corruption-detected snapshot must NOT be reported as loaded")
	assert.Equal(t, 0, g.NodeCount(), "graph must be empty so warmup re-indexes from source")

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr),
		"corrupt snapshot file must be deleted so a daemon restart doesn't loop on it")
}

// Regression guard for the abs-path cleanup case: a small number of
// stale-edge drops accompanied by a node-drop wave (the intentional
// pre-v1 abs-path cleanup) must NOT be flagged as corruption.
func TestLoadSnapshot_SmallStaleEdgeDrops_StaysLoaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absclean.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	// 200 healthy entries plus 5 abs-path stale ones (under 5% drop).
	orig := graph.New()
	for i := 0; i < 200; i++ {
		orig.AddNode(&graph.Node{
			ID: fmt.Sprintf("a.go::S%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("S%d", i), FilePath: "a.go", Language: "go",
		})
	}
	for i := 0; i < 5; i++ {
		orig.AddNode(&graph.Node{
			ID:   fmt.Sprintf("/abs/a.go::S%d", i), // stale abs-path: dropped on load
			Kind: graph.KindFunction, Name: fmt.Sprintf("S%d", i),
			FilePath: "/abs/a.go",
		})
		orig.AddEdge(&graph.Edge{
			From: "a.go::S0", To: fmt.Sprintf("/abs/a.go::S%d", i), Kind: graph.EdgeCalls,
		})
	}
	require.NoError(t, saveSnapshotTo(orig, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	g := graph.New()
	result, err := loadSnapshot(g, zap.NewNop())
	require.NoError(t, err)
	assert.True(t, result.Loaded, "small drop ratio must still load (abs-path cleanup is normal)")
	assert.GreaterOrEqual(t, g.NodeCount(), 200)
}

func TestLoadSnapshot_CorruptFile_ReportsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)
	require.NoError(t, os.WriteFile(path, []byte("not a gzip stream"), 0o600))

	g := graph.New()
	result, err := loadSnapshot(g, zap.NewNop())
	assert.Error(t, err, "corrupt snapshot must not be silently swallowed")
	assert.False(t, result.Loaded)
	assert.Equal(t, 0, g.NodeCount())
}

// TestSaveSnapshot_ShrinkGuardBlocksCollapse proves the shrink-guard
// refuses to overwrite a healthy snapshot with one whose node/edge
// counts have collapsed — the signature of a partial or failed index.
// The prior good snapshot must survive untouched on disk.
func TestSaveSnapshot_ShrinkGuardBlocksCollapse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob.gz")

	// A healthy baseline snapshot: 200 nodes, 199 edges.
	healthy := graph.New()
	for i := 0; i < 200; i++ {
		healthy.AddNode(&graph.Node{
			ID: fmt.Sprintf("repo/a.go::Sym%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("Sym%d", i), FilePath: "repo/a.go", Language: "go",
			RepoPrefix: "repo",
		})
		if i > 0 {
			healthy.AddEdge(&graph.Edge{
				From: fmt.Sprintf("repo/a.go::Sym%d", i),
				To:   "repo/a.go::Sym0", Kind: graph.EdgeCalls,
			})
		}
	}
	require.NoError(t, saveSnapshotTo(healthy, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	// A collapsed graph — 10 nodes, ~5% of the baseline. Attempting to
	// persist it must be refused.
	collapsed := graph.New()
	for i := 0; i < 10; i++ {
		collapsed.AddNode(&graph.Node{
			ID: fmt.Sprintf("repo/a.go::Sym%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("Sym%d", i), FilePath: "repo/a.go", Language: "go",
			RepoPrefix: "repo",
		})
	}
	// The guard's refusal is not surfaced as an error — keeping the
	// good snapshot is a success outcome.
	require.NoError(t, saveSnapshotTo(collapsed, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	// The on-disk snapshot must still be the healthy 200-node one.
	hdr, ok := readSnapshotHeader(path)
	require.True(t, ok, "the prior good snapshot must still be readable")
	assert.Equal(t, 200, hdr.NodeCount, "shrink-guard must have preserved the healthy snapshot")
	assert.Equal(t, 199, hdr.EdgeCount, "shrink-guard must have preserved the healthy snapshot")

	// And no leftover .tmp file from the refused write.
	_, statErr := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "the refused write's tmp file must be cleaned up")
}

// TestSaveSnapshot_ShrinkGuardAllowsModestShrink proves a genuine,
// moderate shrink (files deleted) still overwrites the prior snapshot —
// the guard blocks only a suspicious collapse, not normal churn.
func TestSaveSnapshot_ShrinkGuardAllowsModestShrink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob.gz")

	big := graph.New()
	for i := 0; i < 200; i++ {
		big.AddNode(&graph.Node{
			ID: fmt.Sprintf("repo/a.go::Sym%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("Sym%d", i), FilePath: "repo/a.go", Language: "go",
			RepoPrefix: "repo",
		})
	}
	require.NoError(t, saveSnapshotTo(big, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	// 150 nodes — 75% retained, well above the 50% floor. A legitimate
	// shrink that must be allowed through.
	smaller := graph.New()
	for i := 0; i < 150; i++ {
		smaller.AddNode(&graph.Node{
			ID: fmt.Sprintf("repo/a.go::Sym%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("Sym%d", i), FilePath: "repo/a.go", Language: "go",
			RepoPrefix: "repo",
		})
	}
	require.NoError(t, saveSnapshotTo(smaller, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	hdr, ok := readSnapshotHeader(path)
	require.True(t, ok)
	assert.Equal(t, 150, hdr.NodeCount, "a modest shrink must be allowed to overwrite the snapshot")
}

// TestSaveSnapshot_ShrinkGuardAllowsFirstWrite proves the guard never
// blocks the very first snapshot — with no prior file there is no good
// snapshot to protect.
func TestSaveSnapshot_ShrinkGuardAllowsFirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.gob.gz")

	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.go::Foo", Kind: graph.KindFunction, Name: "Foo", FilePath: "a.go", Language: "go"})
	require.NoError(t, saveSnapshotTo(g, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	hdr, ok := readSnapshotHeader(path)
	require.True(t, ok, "the first snapshot must be written even though it is small")
	assert.Equal(t, 1, hdr.NodeCount)
}

// TestSnapshotWouldCollapse_EdgeOnlyCollapseIsBlocked proves the guard
// fires when edges collapse even though nodes are retained — a resolver
// failure can shed the call graph while leaving symbol nodes intact.
func TestSnapshotWouldCollapse_EdgeOnlyCollapseIsBlocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob.gz")

	healthy := graph.New()
	for i := 0; i < 100; i++ {
		healthy.AddNode(&graph.Node{
			ID: fmt.Sprintf("repo/a.go::Sym%d", i), Kind: graph.KindFunction,
			Name: fmt.Sprintf("Sym%d", i), FilePath: "repo/a.go", Language: "go",
			RepoPrefix: "repo",
		})
		if i > 0 {
			healthy.AddEdge(&graph.Edge{
				From: fmt.Sprintf("repo/a.go::Sym%d", i),
				To:   "repo/a.go::Sym0", Kind: graph.EdgeCalls,
			})
		}
	}
	require.NoError(t, saveSnapshotTo(healthy, nil, nil, snapshotVector{}, version, path, zap.NewNop()))

	// All 100 nodes retained, but only 5 of 99 edges — the call graph
	// has collapsed. The guard must catch the edge-side collapse.
	if !snapshotWouldCollapse(path, 100, 5, zap.NewNop()) {
		t.Error("a collapse confined to edges must still be blocked")
	}
	// Both counts healthy → allowed.
	if snapshotWouldCollapse(path, 100, 99, zap.NewNop()) {
		t.Error("an unchanged graph must not be flagged as a collapse")
	}
}

// TestStartPeriodicSnapshots_WritesOnTick verifies the 10-minute ticker
// (configurable interval in tests) actually fires and writes to disk.
// The daemon relies on this for crash-recovery — on a `kill -9` it
// would otherwise lose everything since the last shutdown snapshot.
func TestStartPeriodicSnapshots_WritesOnTick(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "periodic.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)

	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.go::X", Name: "X", Kind: graph.KindFunction, FilePath: "a.go"})

	// 30ms interval — fast enough to observe two or three ticks within
	// a reasonable test budget, slow enough to survive scheduler jitter.
	// nil isReady → always-ready (test wants ticks to fire immediately).
	stop := startPeriodicSnapshots(g, nil, "t", 30*time.Millisecond, nil, zap.NewNop())
	t.Cleanup(stop)

	require.Eventually(t, func() bool {
		info, err := os.Stat(path)
		return err == nil && info.Size() > 0
	}, 2*time.Second, 20*time.Millisecond,
		"periodic snapshot should land on disk within the budget")

	// Prove a second tick also happens — modify mtime check after capture.
	info1, err := os.Stat(path)
	require.NoError(t, err)

	// Add another node to force a different encoded payload on the
	// next tick; this way a no-op snapshot won't silently pass by
	// just checking mtime equality.
	g.AddNode(&graph.Node{ID: "b.go::Y", Name: "Y", Kind: graph.KindFunction, FilePath: "b.go"})

	require.Eventually(t, func() bool {
		info2, err := os.Stat(path)
		if err != nil {
			return false
		}
		return info2.ModTime().After(info1.ModTime())
	}, 2*time.Second, 20*time.Millisecond,
		"second periodic tick should rewrite the snapshot")
}
