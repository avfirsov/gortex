package indexer

import (
	"iter"
	"sync/atomic"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"go.uber.org/zap"
)

type deferredScanCountingStore struct {
	*graph.Graph
	unresolvedScans atomic.Int64
}

func (s *deferredScanCountingStore) EdgesWithUnresolvedTarget() iter.Seq[*graph.Edge] {
	s.unresolvedScans.Add(1)
	return s.Graph.EdgesWithUnresolvedTarget()
}

func TestResolveDeferredMutationsSkipsCompleteEmptyReceiptWithoutScanning(t *testing.T) {
	store, edge := deferredReceiptFixture()
	mi := &MultiIndexer{graph: store, logger: zap.NewNop()}

	mode := mi.resolveDeferredMutations(&graph.MutationReceipt{Complete: true}, true, nil)
	if mode != deferredResolveSkipped {
		t.Fatalf("mode = %q, want %q", mode, deferredResolveSkipped)
	}
	if got := store.unresolvedScans.Load(); got != 0 {
		t.Fatalf("unresolved full scans = %d, want 0", got)
	}
	if edge.To != graph.UnresolvedMarker+"Target" {
		t.Fatalf("skip unexpectedly changed target to %q", edge.To)
	}
}

func TestResolveDeferredMutationsUsesExactDefinitionFrontier(t *testing.T) {
	store, edge := deferredReceiptFixture()
	mi := &MultiIndexer{graph: store, logger: zap.NewNop()}
	receipt := &graph.MutationReceipt{
		Complete:           true,
		ResolutionRelevant: true,
		DefinitionFiles:    []string{"b.go"},
		TargetNames:        []string{"Target"},
		TargetIDs:          []string{"b.go::Target"},
	}

	mode := mi.resolveDeferredMutations(receipt, true, nil)
	if mode != deferredResolveExact {
		t.Fatalf("mode = %q, want %q", mode, deferredResolveExact)
	}
	if got := store.unresolvedScans.Load(); got != 0 {
		t.Fatalf("exact resolution performed %d whole unresolved scans", got)
	}
	if edge.To != "b.go::Target" {
		t.Fatalf("edge target = %q, want b.go::Target", edge.To)
	}
}

func TestResolveDeferredMutationsUsesExactChangedFileFrontier(t *testing.T) {
	store, edge := deferredReceiptFixture()
	mi := &MultiIndexer{graph: store, logger: zap.NewNop()}
	receipt := &graph.MutationReceipt{
		Complete:           true,
		ResolutionRelevant: true,
		ChangedFiles:       []string{"a.go"},
		TargetNames:        []string{"Target"},
	}

	mode := mi.resolveDeferredMutations(receipt, false, nil)
	if mode != deferredResolveExact {
		t.Fatalf("mode = %q, want %q", mode, deferredResolveExact)
	}
	if got := store.unresolvedScans.Load(); got != 0 {
		t.Fatalf("exact resolution performed %d whole unresolved scans", got)
	}
	if edge.To != "b.go::Target" {
		t.Fatalf("edge target = %q, want b.go::Target", edge.To)
	}
}

func TestResolveDeferredMutationsIncompleteReceiptFallsBackToFullScan(t *testing.T) {
	store, edge := deferredReceiptFixture()
	mi := &MultiIndexer{graph: store, logger: zap.NewNop()}

	mode := mi.resolveDeferredMutations(&graph.MutationReceipt{Complete: false}, false, map[string]struct{}{"repo": {}})
	if mode != deferredResolveFallback {
		t.Fatalf("mode = %q, want %q", mode, deferredResolveFallback)
	}
	if got := store.unresolvedScans.Load(); got == 0 {
		t.Fatal("incomplete receipt did not use whole-graph unresolved scan")
	}
	if edge.To != "b.go::Target" {
		t.Fatalf("fallback edge target = %q, want b.go::Target", edge.To)
	}
}

func TestResolveDeferredMutationsMissingExactPathFailsClosed(t *testing.T) {
	store, _ := deferredReceiptFixture()
	mi := &MultiIndexer{graph: store, logger: zap.NewNop()}
	receipt := &graph.MutationReceipt{Complete: true, ResolutionRelevant: true, TargetNames: []string{"Target"}}

	mode := mi.resolveDeferredMutations(receipt, false, nil)
	if mode != deferredResolveFallback {
		t.Fatalf("mode = %q, want %q", mode, deferredResolveFallback)
	}
	if got := store.unresolvedScans.Load(); got == 0 {
		t.Fatal("missing exact path did not fail closed to whole scan")
	}
}

func deferredReceiptFixture() (*deferredScanCountingStore, *graph.Edge) {
	g := graph.New()
	edge := &graph.Edge{From: "a.go::Caller", To: graph.UnresolvedMarker + "Target", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 3}
	g.AddBatch([]*graph.Node{
		{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go", Language: "go"},
		{ID: "a.go::Caller", Kind: graph.KindFunction, Name: "Caller", FilePath: "a.go", Language: "go"},
		{ID: "b.go", Kind: graph.KindFile, Name: "b.go", FilePath: "b.go", Language: "go"},
		{ID: "b.go::Target", Kind: graph.KindFunction, Name: "Target", FilePath: "b.go", Language: "go"},
	}, []*graph.Edge{edge})
	return &deferredScanCountingStore{Graph: g}, edge
}
