package reach

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

// joinIDs flattens a tier's IDs into a comma-separated string for
// quick equality assertions.
func joinIDs(entries []Entry) string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return strings.Join(ids, ",")
}

// newCallChain returns a graph with N functions wired in a linear
// caller→callee chain: fn-0 calls fn-1 calls fn-2 … calls fn-{N-1}.
// Useful for asserting per-depth tiering since reach_d{k} for fn-N is
// exactly {fn-{N-k}}.
func newCallChain(t *testing.T, n int) (*graph.Graph, []string) {
	t.Helper()
	g := graph.New()
	ids := make([]string, n)
	for i := range n {
		id := callerID(i)
		ids[i] = id
		g.AddNode(&graph.Node{
			ID:       id,
			Kind:     graph.KindFunction,
			Name:     id,
			FilePath: "main.go",
		})
	}
	for i := range n - 1 {
		g.AddEdge(&graph.Edge{
			From:       ids[i],
			To:         ids[i+1],
			Kind:       graph.EdgeCalls,
			Confidence: 1,
		})
	}
	return g, ids
}

func callerID(i int) string {
	// Inline a small base-10 conversion to dodge a strconv import in a
	// test fixture that already has a few small helpers.
	switch i {
	case 0:
		return "fn-0"
	case 1:
		return "fn-1"
	case 2:
		return "fn-2"
	case 3:
		return "fn-3"
	case 4:
		return "fn-4"
	}
	return "fn-" + iToA(i)
}

func iToA(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestBuildIndex_TiersMatchDepth asserts that fn-{N-1}'s reach_d1
// contains exactly fn-{N-2}, reach_d2 contains fn-{N-3}, etc.
func TestBuildIndex_TiersMatchDepth(t *testing.T) {
	g, ids := newCallChain(t, 5)
	stats := BuildIndex(g)
	if stats.NodesIndexed != 5 {
		t.Fatalf("indexed=%d want 5", stats.NodesIndexed)
	}
	// Seed = last fn in the chain (fn-4). Its callers are fn-3 (d1),
	// fn-2 (d2), fn-1 (d3). fn-0 sits at depth 4 so it must not appear.
	seed := ids[4]
	d1, d2, d3, hit := Lookup(g, seed)
	if !hit {
		t.Fatal("expected lookup hit for indexed seed")
	}
	if got := joinIDs(d1); got != "fn-3" {
		t.Errorf("d1=%q want fn-3", got)
	}
	if got := joinIDs(d2); got != "fn-2" {
		t.Errorf("d2=%q want fn-2", got)
	}
	if got := joinIDs(d3); got != "fn-1" {
		t.Errorf("d3=%q want fn-1", got)
	}
}

// TestBuildIndex_SourceNodeHasEmptyReach asserts that fn-0 (the chain
// root, which nothing calls) still gets a build stamp but has empty
// reach tiers — proving sinks distinguish from "not indexed yet".
func TestBuildIndex_SourceNodeHasEmptyReach(t *testing.T) {
	g, ids := newCallChain(t, 3)
	BuildIndex(g)
	d1, d2, d3, hit := Lookup(g, ids[0])
	if !hit {
		t.Fatal("expected build stamp on indexed leaf node")
	}
	if len(d1)+len(d2)+len(d3) != 0 {
		t.Errorf("sink node should have empty reach tiers; got d1=%v d2=%v d3=%v", d1, d2, d3)
	}
}

// TestBuildIndex_IgnoresStructuralEdges asserts that EdgeDefines and
// EdgeMemberOf are NOT followed during reach computation — they are
// structural-only signals AnalyzeImpact already discards.
func TestBuildIndex_IgnoresStructuralEdges(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "file.go", Kind: graph.KindFile, FilePath: "file.go"})
	g.AddNode(&graph.Node{ID: "T", Kind: graph.KindType, Name: "T", FilePath: "file.go"})
	g.AddNode(&graph.Node{ID: "T.Method", Kind: graph.KindMethod, Name: "Method", FilePath: "file.go"})
	g.AddNode(&graph.Node{ID: "caller", Kind: graph.KindFunction, Name: "caller", FilePath: "file.go"})

	// Structural edges — must NOT count as reach.
	g.AddEdge(&graph.Edge{From: "file.go", To: "T", Kind: graph.EdgeDefines})
	g.AddEdge(&graph.Edge{From: "T", To: "T.Method", Kind: graph.EdgeMemberOf})
	// Real edge — must count.
	g.AddEdge(&graph.Edge{From: "caller", To: "T.Method", Kind: graph.EdgeCalls, Confidence: 1})

	BuildIndex(g)
	d1, _, _, _ := Lookup(g, "T.Method")
	if len(d1) != 1 || d1[0].ID != "caller" {
		t.Errorf("d1 must contain only caller via EdgeCalls; got %v", d1)
	}
}

// TestBuildIndex_FilesAndImportsExcludedFromTiers asserts that
// file/import nodes encountered during traversal are walked through
// (for fan-out) but not surfaced in the returned tiers — the same
// behavior AnalyzeImpact's live walk has.
func TestBuildIndex_FilesAndImportsExcludedFromTiers(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "callee", Kind: graph.KindFunction, FilePath: "main.go"})
	g.AddNode(&graph.Node{ID: "f.go", Kind: graph.KindFile, FilePath: "f.go"})
	g.AddNode(&graph.Node{ID: "i.import", Kind: graph.KindImport, FilePath: "main.go"})
	// File + import both reference the callee — should still produce empty d1.
	g.AddEdge(&graph.Edge{From: "f.go", To: "callee", Kind: graph.EdgeReferences})
	g.AddEdge(&graph.Edge{From: "i.import", To: "callee", Kind: graph.EdgeReferences})

	BuildIndex(g)
	d1, _, _, hit := Lookup(g, "callee")
	if !hit {
		t.Fatal("expected build stamp")
	}
	if len(d1) != 0 {
		t.Errorf("file/import sources must be excluded from reach tier; got %v", d1)
	}
}

// TestLookup_LazyComputesOnFirstMiss asserts that Lookup is self-
// populating: a seed that has never been precomputed (no eager
// BuildIndex run) still returns a hit, having run the BFS on demand
// and stamped the result for subsequent lookups. The behaviour
// switched from "fall back to live walk via consumer fallback" to
// "compute and cache transparently inside Lookup" when the eager
// pass was retired from the cold-index hot path.
func TestLookup_LazyComputesOnFirstMiss(t *testing.T) {
	g, ids := newCallChain(t, 3)
	// Deliberately skip BuildIndex — lazy Lookup must still answer.
	seed := ids[2]
	d1, d2, d3, hit := Lookup(g, seed)
	if !hit {
		t.Fatal("lazy Lookup should return hit=true for a valid impact seed even without an eager BuildIndex")
	}
	// On a 3-node A→B→C chain, seed C has B at d1 and A at d2.
	if len(d1) != 1 || d1[0].ID != ids[1] {
		t.Errorf("expected d1=[%s], got %#v", ids[1], d1)
	}
	if len(d2) != 1 || d2[0].ID != ids[0] {
		t.Errorf("expected d2=[%s], got %#v", ids[0], d2)
	}
	if len(d3) != 0 {
		t.Errorf("expected d3=[], got %#v", d3)
	}
	// Lazy lookup is intentionally read-only. Persisting this result would
	// turn a safety check into an uncancellable store write; eager BuildIndex
	// remains the only reach-cache publication path.
	n := g.GetNode(seed)
	if n == nil {
		t.Fatalf("seed %s disappeared after lazy lookup", seed)
	}
	if _, ok := n.Meta[MetaReachBuild]; ok {
		t.Errorf("lazy Lookup must not stamp MetaReachBuild on %s", seed)
	}
}

// TestLookup_ConcurrentBuildNeverReturnsPartial exercises the real publication
// path while eager rebuilds and hot cache reads overlap. Every generation has
// identical graph topology, so a reader may observe either adjacent complete
// generation, but it must never observe an empty or truncated tier.
func TestLookup_ConcurrentBuildNeverReturnsPartial(t *testing.T) {
	g, ids := newCallChain(t, 5)
	seed := ids[4]
	BuildIndex(g)

	const (
		builds  = 40
		readers = 8
		reads   = 400
	)
	start := make(chan struct{})
	errs := make(chan string, readers*reads)
	var wg sync.WaitGroup
	wg.Add(1 + readers)
	go func() {
		defer wg.Done()
		<-start
		for range builds {
			BuildIndex(g)
		}
	}()
	for range readers {
		go func() {
			defer wg.Done()
			<-start
			for range reads {
				d1, d2, d3, hit := Lookup(g, seed)
				if !hit || joinIDs(d1) != ids[3] || joinIDs(d2) != ids[2] || joinIDs(d3) != ids[1] {
					errs <- "lookup observed an incomplete reach generation"
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	if err := <-errs; err != "" {
		t.Fatal(err)
	}
}

// TestLookup_ConcurrentUnrelatedMetaMutation proves the cache fast path shares
// ResolveMutex with graph-wide passes that still update non-reach Node.Meta
// fields in place. The race detector used to report readCached racing the
// writer below even though both reach writers themselves were copy-on-write.
func TestLookup_ConcurrentUnrelatedMetaMutation(t *testing.T) {
	g, ids := newCallChain(t, 5)
	seed := ids[4]
	BuildIndex(g)

	const iterations = 1000
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := range iterations {
			mu := g.ResolveMutex()
			mu.Lock()
			n := g.GetNode(seed)
			n.Meta["unrelated_test_generation"] = i
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			d1, d2, d3, hit := Lookup(g, seed)
			if !hit || joinIDs(d1) != ids[3] || joinIDs(d2) != ids[2] || joinIDs(d3) != ids[1] {
				t.Errorf("lookup changed while unrelated metadata was updated")
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}

// TestTopologyMutationPublishesBeforeLookup ensures a cached zero-impact
// answer cannot escape while the watcher is replacing topology. Readers wait
// for the mutation's invalidation, then compute from the complete new graph.
func TestTopologyMutationPublishesBeforeLookup(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "sink", Kind: graph.KindFunction, Name: "sink", FilePath: "sink.go"})
	if d1, _, _, hit := Lookup(g, "sink"); !hit || len(d1) != 0 {
		t.Fatalf("fixture must begin with a cached empty reach record; hit=%v d1=%v", hit, d1)
	}

	finishMutation := BeginTopologyMutation(g)
	type lookupResult struct {
		d1  []Entry
		hit bool
	}
	resultCh := make(chan lookupResult, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		d1, _, _, hit := Lookup(g, "sink")
		resultCh <- lookupResult{d1: d1, hit: hit}
	}()
	<-started

	select {
	case result := <-resultCh:
		t.Fatalf("lookup escaped an active topology mutation: %+v", result)
	case <-time.After(10 * time.Millisecond):
	}

	g.AddNode(&graph.Node{ID: "caller", Kind: graph.KindFunction, Name: "caller", FilePath: "caller.go"})
	g.AddEdge(&graph.Edge{From: "caller", To: "sink", Kind: graph.EdgeCalls, Confidence: 1})
	finishMutation(true)

	select {
	case result := <-resultCh:
		if !result.hit || len(result.d1) != 1 || result.d1[0].ID != "caller" {
			t.Fatalf("lookup did not observe the fully published topology: hit=%v d1=%v", result.hit, result.d1)
		}
	case <-time.After(time.Second):
		t.Fatal("lookup remained blocked after topology publication")
	}
}

type countingBatchStore struct {
	graph.Store
	mu      sync.Mutex
	batches int
	maxRows int
}

func (s *countingBatchStore) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	s.mu.Lock()
	s.batches++
	if len(nodes) > s.maxRows {
		s.maxRows = len(nodes)
	}
	s.mu.Unlock()
	s.Store.AddBatch(nodes, edges)
}

func (s *countingBatchStore) reset() {
	s.mu.Lock()
	s.batches = 0
	s.maxRows = 0
	s.mu.Unlock()
}

func (s *countingBatchStore) counts() (batches, maxRows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batches, s.maxRows
}

// TestReachMaintenancePublishesBoundedBatches prevents eager maintenance from
// retaining a full-graph duplicate of Node + Meta records before publication.
func TestReachMaintenancePublishesBoundedBatches(t *testing.T) {
	g, _ := newCallChain(t, reachPublishBatchSize+17)
	store := &countingBatchStore{Store: g}

	BuildIndex(store)
	if batches, maxRows := store.counts(); batches < 2 || maxRows > reachPublishBatchSize {
		t.Fatalf("BuildIndex batches=%d max_rows=%d; want multiple batches capped at %d", batches, maxRows, reachPublishBatchSize)
	}

	store.reset()
	ClearIndex(store)
	if batches, maxRows := store.counts(); batches < 2 || maxRows > reachPublishBatchSize {
		t.Fatalf("ClearIndex batches=%d max_rows=%d; want multiple batches capped at %d", batches, maxRows, reachPublishBatchSize)
	}
}

// TestLookup_NonSeedKindStaysFalse asserts that Lookup never tries to
// lazy-compute for a node whose kind is not an impact seed (file,
// import, param, …). Consumers that pass a non-seed ID get hit=false
// so the live-BFS fallback in AnalyzeImpact handles the edge case
// (impact analysis from a file node walks file-import edges, not
// callers — semantics outside reach's mandate).
func TestLookup_NonSeedKindStaysFalse(t *testing.T) {
	g, _ := newCallChain(t, 3)
	fileNode := &graph.Node{ID: "test.go", Kind: graph.KindFile, FilePath: "test.go"}
	g.AddNode(fileNode)
	if _, _, _, hit := Lookup(g, "test.go"); hit {
		t.Error("Lookup must return hit=false for a non-impact-seed kind")
	}
}

// TestClearIndex_RemovesStampsAndBumpsCounter asserts that ClearIndex
// strips reach_d* / reach_build and advances the generation tag so
// any in-flight reader sees a fresh build number.
func TestClearIndex_RemovesStampsAndBumpsCounter(t *testing.T) {
	g, _ := newCallChain(t, 3)
	BuildIndex(g)
	before := BuildCounter()
	ClearIndex(g)
	if BuildCounter() <= before {
		t.Errorf("ClearIndex must bump the generation counter; before=%d after=%d", before, BuildCounter())
	}
	for _, n := range g.AllNodes() {
		if n.Meta == nil {
			continue
		}
		for _, k := range []string{MetaReachD1, MetaReachD2, MetaReachD3, MetaReachBuild, MetaReachComplete} {
			if _, ok := n.Meta[k]; ok {
				t.Errorf("node %s still has key %q after ClearIndex", n.ID, k)
			}
		}
	}
}

// TestBuildIndex_Idempotent asserts that running BuildIndex twice
// produces identical tier slices — important for snapshot stability
// and for the watcher path that rebuilds after every patch.
func TestBuildIndex_Idempotent(t *testing.T) {
	g, ids := newCallChain(t, 4)
	BuildIndex(g)
	d1a, d2a, d3a, _ := Lookup(g, ids[3])
	BuildIndex(g)
	d1b, d2b, d3b, _ := Lookup(g, ids[3])
	if joinIDs(d1a) != joinIDs(d1b) ||
		joinIDs(d2a) != joinIDs(d2b) ||
		joinIDs(d3a) != joinIDs(d3b) {
		t.Errorf("repeated BuildIndex produced different tiers: before d1=%v d2=%v d3=%v; after d1=%v d2=%v d3=%v",
			d1a, d2a, d3a, d1b, d2b, d3b)
	}
}

// TestBuildIndex_NonImpactSeedsSkipped asserts that imports and files
// don't receive build stamps — the index is only for symbols a
// developer would actually change.
func TestBuildIndex_NonImpactSeedsSkipped(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "main.go", Kind: graph.KindFile, FilePath: "main.go"})
	g.AddNode(&graph.Node{ID: "fmt.import", Kind: graph.KindImport, FilePath: "main.go"})
	g.AddNode(&graph.Node{ID: "fn", Kind: graph.KindFunction, FilePath: "main.go"})
	g.AddEdge(&graph.Edge{From: "fn", To: "fn", Kind: graph.EdgeCalls, Confidence: 1})

	BuildIndex(g)
	if _, _, _, hit := Lookup(g, "main.go"); hit {
		t.Error("file nodes must not receive a reach build stamp")
	}
	if _, _, _, hit := Lookup(g, "fmt.import"); hit {
		t.Error("import nodes must not receive a reach build stamp")
	}
	if _, _, _, hit := Lookup(g, "fn"); !hit {
		t.Error("function nodes must receive a reach build stamp")
	}
}
