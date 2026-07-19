package store_sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

// bulkFixture builds a deterministic node/edge set with distinct ids and
// qual_names (so the UNIQUE nodes_by_qual index never collides) and a mix of
// edge keys (a handful collide → exercise dedup). Input order is intentionally
// not key-sorted.
func bulkFixture(nNodes, nEdges int) ([]*graph.Node, []*graph.Edge) {
	nodes := make([]*graph.Node, 0, nNodes)
	for i := range nNodes {
		nodes = append(nodes, &graph.Node{
			ID:         fmt.Sprintf("pkg/f%d.go::Sym%d", i%64, i),
			Kind:       graph.KindFunction,
			Name:       fmt.Sprintf("Sym%d", i),
			QualName:   fmt.Sprintf("pkg.f%d.Sym%d", i%64, i),
			FilePath:   fmt.Sprintf("pkg/f%d.go", i%64),
			RepoPrefix: "gortex",
			Language:   "go",
		})
	}
	edges := make([]*graph.Edge, 0, nEdges)
	for i := range nEdges {
		from := nodes[i%nNodes]
		to := nodes[(i*7+1)%nNodes]
		edges = append(edges, &graph.Edge{
			From:       from.ID,
			To:         to.ID,
			Kind:       graph.EdgeCalls,
			FilePath:   from.FilePath,
			Line:       i % 500,
			Confidence: 1,
		})
	}
	return nodes, edges
}

func openTempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bulk.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

// indexNames returns the set of secondary index names present in the schema.
func indexNames(t *testing.T, q interface {
	Query(string, ...any) (*sql.Rows, error)
}) map[string]bool {
	t.Helper()
	rows, err := q.Query("SELECT name FROM sqlite_master WHERE type='index'")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		got[n] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return got
}

func pragmaIntDB(t *testing.T, db *sql.DB, pragma string) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow("PRAGMA " + pragma).Scan(&v); err != nil {
		t.Fatalf("pragma %s: %v", pragma, err)
	}
	return v
}

func integrityOK(t *testing.T, db *sql.DB) {
	t.Helper()
	var res string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&res); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if res != "ok" {
		t.Fatalf("integrity_check = %q, want \"ok\"", res)
	}
}

// TestBulkLoadDropsAndRebuildsIndexes is the core mechanism + restore proof:
// the fast path engages on an empty store, drops the droppable indexes,
// pins a synchronous=OFF connection, then on FlushBulk rebuilds every index,
// restores synchronous, releases the connection, and leaves the DB intact.
func TestBulkLoadDropsAndRebuildsIndexes(t *testing.T) {
	s, _ := openTempStore(t)
	ctx := context.Background()

	// Baseline: all droppable indexes present, synchronous=NORMAL(1).
	before := indexNames(t, s.db)
	for _, idx := range bulkDroppableIndexes {
		if !before[idx.name] {
			t.Fatalf("index %s missing before bulk load", idx.name)
		}
	}
	if got := pragmaIntDB(t, s.db, "synchronous"); got != 1 {
		t.Fatalf("synchronous before = %d, want 1 (NORMAL)", got)
	}

	// Engage the fast path on the empty store.
	s.BeginBulkLoad()
	if s.bulkConn == nil {
		t.Fatal("fast path did not engage on empty store")
	}
	// Read through the pinned connection (it may be the only one when
	// GOMAXPROCS/NumCPU is 1) to avoid blocking on connection acquisition.
	var sync int64
	if err := s.bulkConn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatalf("pinned synchronous: %v", err)
	}
	if sync != 0 {
		t.Fatalf("bulk synchronous = %d, want 0 (OFF)", sync)
	}
	var cache int64
	if err := s.bulkConn.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cache); err != nil {
		t.Fatalf("pinned cache_size: %v", err)
	}
	if cache != bulkCacheSizeKiB {
		t.Fatalf("bulk cache_size = %d, want %d", cache, bulkCacheSizeKiB)
	}
	// The droppable indexes are gone during the window.
	during := indexNames(t, &connQuerier{ctx: ctx, c: s.bulkConn})
	for _, idx := range bulkDroppableIndexes {
		if during[idx.name] {
			t.Fatalf("index %s still present during bulk window", idx.name)
		}
	}
	// nodes_by_qual (UNIQUE, not droppable) must remain live.
	if !during["nodes_by_qual"] {
		t.Fatal("nodes_by_qual (UNIQUE) must not be dropped")
	}

	nodes, edges := bulkFixture(2000, 4000)
	s.AddBatch(nodes, edges)

	if err := s.FlushBulk(); err != nil {
		t.Fatalf("FlushBulk: %v", err)
	}
	if s.bulkConn != nil {
		t.Fatal("bulkConn not released after FlushBulk")
	}

	// Every dropped index is back.
	after := indexNames(t, s.db)
	for _, idx := range bulkDroppableIndexes {
		if !after[idx.name] {
			t.Fatalf("index %s not rebuilt after FlushBulk", idx.name)
		}
	}
	// synchronous restored to NORMAL on the pool.
	if got := pragmaIntDB(t, s.db, "synchronous"); got != 1 {
		t.Fatalf("synchronous after = %d, want 1 (NORMAL)", got)
	}
	integrityOK(t, s.db)
}

// connQuerier adapts *sql.Conn to the Query signature indexNames expects.
type connQuerier struct {
	ctx context.Context
	c   *sql.Conn
}

func (q *connQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	return q.c.QueryContext(q.ctx, query, args...)
}

// TestBulkLoadMatchesNonBulkCounts proves the fast path persists exactly the
// same node/edge counts the plain AddBatch path does.
func TestBulkLoadMatchesNonBulkCounts(t *testing.T) {
	nodes, edges := bulkFixture(3000, 6000)

	plain, _ := openTempStore(t)
	plain.AddBatch(nodes, edges)
	wantNodes, wantEdges := plain.NodeCount(), plain.EdgeCount()

	bulk, _ := openTempStore(t)
	bulk.BeginBulkLoad()
	if bulk.bulkConn == nil {
		t.Fatal("fast path did not engage on empty store")
	}
	// Drain in two chunks to mirror the indexer's chunked persist.
	bulk.AddBatch(nodes[:1500], nil)
	bulk.AddBatch(nodes[1500:], nil)
	bulk.AddBatch(nil, edges[:3000])
	bulk.AddBatch(nil, edges[3000:])
	if err := bulk.FlushBulk(); err != nil {
		t.Fatalf("FlushBulk: %v", err)
	}

	if gotN, gotE := bulk.NodeCount(), bulk.EdgeCount(); gotN != wantNodes || gotE != wantEdges {
		t.Fatalf("bulk counts (%d nodes, %d edges) != non-bulk (%d, %d)", gotN, gotE, wantNodes, wantEdges)
	}
	integrityOK(t, bulk.db)
}

// TestBulkLoadGatedToPopulatedStore confirms the fast path is a safe no-op on
// a store that already holds rows — no indexes are dropped, durability stays.
func TestBulkLoadGatedToPopulatedStore(t *testing.T) {
	s, _ := openTempStore(t)
	// Populate first (the normal, non-bulk path).
	nodes, edges := bulkFixture(50, 100)
	s.AddBatch(nodes, edges)

	s.BeginBulkLoad()
	if s.bulkConn != nil {
		t.Fatal("fast path engaged on a populated store; must be a no-op")
	}
	// Indexes untouched, durability untouched.
	present := indexNames(t, s.db)
	for _, idx := range bulkDroppableIndexes {
		if !present[idx.name] {
			t.Fatalf("index %s dropped on a populated store", idx.name)
		}
	}
	if got := pragmaIntDB(t, s.db, "synchronous"); got != 1 {
		t.Fatalf("synchronous = %d on populated store, want 1 (NORMAL)", got)
	}
	if err := s.FlushBulk(); err != nil {
		t.Fatalf("FlushBulk no-op returned error: %v", err)
	}
}

func TestCoordinatedBulkLoadDefersNestedRepoFlushes(t *testing.T) {
	s, _ := openTempStore(t)
	if !s.BeginCoordinatedBulkLoad() {
		t.Fatal("coordinated fast path did not engage on empty store")
	}
	if !s.coordinatedBulkLoad || s.bulkConn == nil {
		t.Fatal("coordinated bulk state not retained")
	}

	assertIndexesDropped := func(stage string) {
		t.Helper()
		present := indexNames(t, &connQuerier{ctx: context.Background(), c: s.bulkConn})
		for _, idx := range bulkDroppableIndexes {
			if present[idx.name] {
				t.Fatalf("index %s rebuilt during %s", idx.name, stage)
			}
		}
	}
	assertIndexesDropped("outer begin")

	// Mirror two concurrent-repository shadow drains. Their ordinary bulk
	// boundaries must not close the outer window after repository one.
	s.BeginBulkLoad()
	s.AddBatch([]*graph.Node{{
		ID: "repo-a/a.go::A", Kind: graph.KindFunction, Name: "A",
		FilePath: "repo-a/a.go", RepoPrefix: "repo-a", Language: "go",
	}}, nil)
	if err := s.FlushBulk(); err != nil {
		t.Fatalf("nested repo-a FlushBulk: %v", err)
	}
	assertIndexesDropped("repo-a flush")

	s.BeginBulkLoad()
	s.AddBatch([]*graph.Node{{
		ID: "repo-b/b.go::B", Kind: graph.KindFunction, Name: "B",
		FilePath: "repo-b/b.go", RepoPrefix: "repo-b", Language: "go",
	}}, nil)
	if err := s.FlushBulk(); err != nil {
		t.Fatalf("nested repo-b FlushBulk: %v", err)
	}
	assertIndexesDropped("repo-b flush")

	if err := s.EndCoordinatedBulkLoad(); err != nil {
		t.Fatalf("EndCoordinatedBulkLoad: %v", err)
	}
	if s.coordinatedBulkLoad || s.bulkConn != nil {
		t.Fatal("coordinated bulk state not released")
	}
	rebuilt := indexNames(t, s.db)
	for _, idx := range bulkDroppableIndexes {
		if !rebuilt[idx.name] {
			t.Fatalf("index %s not rebuilt at outer finalize", idx.name)
		}
	}
	if got := s.NodeCount(); got != 2 {
		t.Fatalf("node count = %d, want 2", got)
	}
	integrityOK(t, s.db)
	if s.BeginCoordinatedBulkLoad() {
		t.Fatal("coordinated fast path engaged on populated warm store")
	}
}

func TestCoordinatedBulkLoadRoutesSingleRowSidecarsOnPinnedConnection(t *testing.T) {
	s, _ := openTempStore(t)
	s.db.SetMaxOpenConns(1)
	if !s.BeginCoordinatedBulkLoad() {
		t.Fatal("coordinated fast path did not engage")
	}

	done := make(chan error, 1)
	go func() {
		if err := s.SetRepoIndexState(graph.RepoIndexState{RepoPrefix: "repo", IndexedSHA: "sha"}); err != nil {
			done <- err
			return
		}
		if err := s.SetFileMtime("repo", "a.go", 1); err != nil {
			done <- err
			return
		}
		if err := s.SetEnrichmentState(graph.EnrichmentState{RepoPrefix: "repo", Provider: "test"}); err != nil {
			done <- err
			return
		}
		done <- s.UpsertEmbedding("repo/a.go::A", []float32{1})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sidecar write: %v", err)
		}
	case <-time.After(time.Second):
		// Let a legacy pool-routed writer drain so test cleanup cannot hang.
		s.db.SetMaxOpenConns(2)
		<-done
		t.Fatal("single-row sidecar writer waited for a second pooled connection")
	}
	if err := s.EndCoordinatedBulkLoad(); err != nil {
		t.Fatalf("EndCoordinatedBulkLoad: %v", err)
	}
}

func TestCloseFinalizesActiveCoordinatedBulkLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close-active-bulk.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !s.BeginCoordinatedBulkLoad() {
		t.Fatal("coordinated fast path did not engage")
	}
	s.AddBatch([]*graph.Node{{
		ID: "repo/a.go::A", Kind: graph.KindFunction, Name: "A",
		FilePath: "repo/a.go", RepoPrefix: "repo", Language: "go",
	}}, nil)
	if err := s.Close(); err != nil {
		t.Fatalf("Close with active coordinated load: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if got := reopened.NodeCount(); got != 1 {
		t.Fatalf("node count after close/reopen = %d, want 1", got)
	}
	for _, idx := range bulkDroppableIndexes {
		if !indexNames(t, reopened.db)[idx.name] {
			t.Fatalf("index %s not rebuilt by Close", idx.name)
		}
	}
	integrityOK(t, reopened.db)
}

func TestCoordinatedBulkLoadDefersFTSOptimizeUntilOuterEnd(t *testing.T) {
	s, _ := openTempStore(t)
	if !s.BeginCoordinatedBulkLoad() {
		t.Fatal("coordinated fast path did not engage")
	}
	if err := s.BulkUpsertSymbolFTS("repo", []graph.SymbolFTSItem{{NodeID: "repo/a.go::Alpha", Tokens: "alpha symbol"}}); err != nil {
		t.Fatalf("BulkUpsertSymbolFTS: %v", err)
	}
	if err := s.BuildSymbolIndex(); err != nil {
		t.Fatalf("BuildSymbolIndex: %v", err)
	}
	if err := s.AppendContent("repo", []graph.ContentFTSItem{{
		NodeID: "repo/docs.md::section", FilePath: "repo/docs.md", Body: "cold content marker",
	}}); err != nil {
		t.Fatalf("AppendContent: %v", err)
	}
	if err := s.BuildContentIndex(); err != nil {
		t.Fatalf("BuildContentIndex: %v", err)
	}
	if !s.deferredFTSOptimize {
		t.Fatal("per-repository symbol FTS optimize was not deferred")
	}
	if !s.deferredContentFTS {
		t.Fatal("per-repository content FTS optimize was not deferred")
	}
	if err := s.EndCoordinatedBulkLoad(); err != nil {
		t.Fatalf("EndCoordinatedBulkLoad: %v", err)
	}
	if s.deferredFTSOptimize {
		t.Fatal("deferred symbol FTS optimize was not consumed")
	}
	if s.deferredContentFTS {
		t.Fatal("deferred content FTS optimize was not consumed")
	}
	hits, err := s.SearchSymbols("alpha", 10)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(hits) != 1 || hits[0].NodeID != "repo/a.go::Alpha" {
		t.Fatalf("unexpected FTS hits after outer finalize: %#v", hits)
	}
	contentHits, err := s.SearchContent("marker", "repo", 10)
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(contentHits) != 1 || contentHits[0].NodeID != "repo/docs.md::section" {
		t.Fatalf("unexpected content FTS hits after outer finalize: %#v", contentHits)
	}
}

// TestBulkLoadInMemoryIsNoOp confirms in-memory stores never engage the fast
// path (no WAL / on-disk B-tree to optimise).
func TestBulkLoadInMemoryIsNoOp(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.BeginBulkLoad()
	if s.bulkConn != nil {
		t.Fatal("fast path engaged on an in-memory store")
	}
	if err := s.FlushBulk(); err != nil {
		t.Fatalf("FlushBulk: %v", err)
	}
}

// TestBulkLoadWarmRestartLoadsClean bulk-loads, closes, reopens the same file,
// and asserts the persisted graph round-trips: identical counts, indexes
// present, integrity ok.
func TestBulkLoadWarmRestartLoadsClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "warm.sqlite")
	nodes, edges := bulkFixture(2500, 5000)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.BeginBulkLoad()
	if s.bulkConn == nil {
		t.Fatal("fast path did not engage on empty store")
	}
	s.AddBatch(nodes, edges)
	if err := s.FlushBulk(); err != nil {
		t.Fatalf("FlushBulk: %v", err)
	}
	wantNodes, wantEdges := s.NodeCount(), s.EdgeCount()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if gotN, gotE := reopened.NodeCount(), reopened.EdgeCount(); gotN != wantNodes || gotE != wantEdges {
		t.Fatalf("warm restart counts (%d, %d) != pre-close (%d, %d)", gotN, gotE, wantNodes, wantEdges)
	}
	present := indexNames(t, reopened.db)
	for _, idx := range bulkDroppableIndexes {
		if !present[idx.name] {
			t.Fatalf("index %s missing after warm restart", idx.name)
		}
	}
	integrityOK(t, reopened.db)
	// A populated store on reopen must NOT engage the fast path.
	reopened.BeginBulkLoad()
	if reopened.bulkConn != nil {
		t.Fatal("fast path engaged on warm restart (populated store)")
	}
	_ = reopened.FlushBulk()
}

// TestBulkLoadPersistSpeed is the persist-speed evidence: it times the plain
// path vs the fast path on the same fixture and logs both. It asserts
// correctness and that the fast path is not pathologically slower; a strict
// speedup ratio is gated behind GORTEX_BULK_PERF_ASSERT so the default run
// stays deterministic on noisy CI.
func TestBulkLoadPersistSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping persist-speed timing in -short")
	}
	n, e := 8000, 16000
	nodes, edges := bulkFixture(n, e)

	plain, _ := openTempStore(t)
	t0 := time.Now()
	plain.AddBatch(nodes, edges)
	plainDur := time.Since(t0)

	bulk, _ := openTempStore(t)
	t1 := time.Now()
	bulk.BeginBulkLoad()
	bulk.AddBatch(nodes, edges)
	if err := bulk.FlushBulk(); err != nil {
		t.Fatalf("FlushBulk: %v", err)
	}
	bulkDur := time.Since(t1)

	if bulk.NodeCount() != plain.NodeCount() || bulk.EdgeCount() != plain.EdgeCount() {
		t.Fatalf("count mismatch: bulk(%d,%d) plain(%d,%d)",
			bulk.NodeCount(), bulk.EdgeCount(), plain.NodeCount(), plain.EdgeCount())
	}
	integrityOK(t, bulk.db)

	ratio := float64(plainDur) / float64(bulkDur)
	t.Logf("persist %d nodes / %d edges: plain=%s bulk=%s speedup=%.2fx",
		n, e, plainDur, bulkDur, ratio)

	// Sanity floor: the fast path must never be dramatically slower.
	if bulkDur > plainDur*5 {
		t.Fatalf("fast path far slower: plain=%s bulk=%s", plainDur, bulkDur)
	}
	if os.Getenv("GORTEX_BULK_PERF_ASSERT") != "" && ratio < 2.0 {
		t.Fatalf("fast path speedup %.2fx below 2x target", ratio)
	}
}

// BenchmarkPersistFixture is reproducible persist-speed evidence: run with
//
//	go test -run=^$ -bench=BenchmarkPersistFixture ./internal/graph/store_sqlite/
//
// to compare the plain AddBatch path against the bulk-load fast path.
func BenchmarkPersistFixture(b *testing.B) {
	nodes, edges := bulkFixture(50000, 100000)

	run := func(b *testing.B, bulk bool) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			path := filepath.Join(b.TempDir(), fmt.Sprintf("p%d.sqlite", i))
			s, err := Open(path)
			if err != nil {
				b.Fatalf("open: %v", err)
			}
			b.StartTimer()
			if bulk {
				s.BeginBulkLoad()
				s.AddBatch(nodes, edges)
				if err := s.FlushBulk(); err != nil {
					b.Fatalf("FlushBulk: %v", err)
				}
			} else {
				s.AddBatch(nodes, edges)
			}
			b.StopTimer()
			_ = s.Close()
		}
	}

	b.Run("nonbulk", func(b *testing.B) { run(b, false) })
	b.Run("bulk", func(b *testing.B) { run(b, true) })
}
