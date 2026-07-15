package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestBuildPassIndexesForPendingBoundsReachability(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go"},
		{ID: "a.go::Caller", Kind: graph.KindFunction, Name: "Caller", FilePath: "a.go"},
		{ID: "dep/dep.go", Kind: graph.KindFile, Name: "dep.go", FilePath: "dep/dep.go"},
		{ID: "other/other.go", Kind: graph.KindFile, Name: "other.go", FilePath: "other/other.go"},
	}, []*graph.Edge{
		{From: "a.go", To: "dep/dep.go", Kind: graph.EdgeImports, FilePath: "a.go", Line: 1},
	})

	r := New(g)
	pending := []*graph.Edge{{
		From: "a.go::Caller", To: graph.UnresolvedMarker + "Work",
		Kind: graph.EdgeCalls, FilePath: "a.go", Line: 3,
	}}
	clear := r.buildPassIndexesForPending(pending)
	defer clear()

	if got := len(r.reachableDirsByFile); got != 1 {
		t.Fatalf("frontier reachability files = %d, want 1", got)
	}
	reachable := r.reachableDirsByFile["a.go"]
	if _, ok := reachable["."]; !ok {
		t.Fatal("caller own directory missing from frontier reachability")
	}
	if _, ok := reachable["dep"]; !ok {
		t.Fatal("resolved import directory missing from frontier reachability")
	}
	if _, ok := r.reachableDirsByFile["other/other.go"]; ok {
		t.Fatal("unrelated file leaked into frontier reachability")
	}
	if r.dirIndex != nil || r.depModuleIndex != nil || r.providesForIdx != nil {
		t.Fatal("call-only frontier eagerly built a global resolver index")
	}
}

func TestBuildPassIndexesForPendingRecoversMissingEdgeFilePath(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go"},
		{ID: "a.go::Caller", Kind: graph.KindFunction, Name: "Caller", FilePath: "a.go"},
		{ID: "other/other.go", Kind: graph.KindFile, Name: "other.go", FilePath: "other/other.go"},
	}, nil)

	r := New(g)
	clear := r.buildPassIndexesForPending([]*graph.Edge{{
		From: "a.go::Caller", To: graph.UnresolvedMarker + "Work", Kind: graph.EdgeCalls,
	}})
	defer clear()

	if got := len(r.reachableDirsByFile); got != 1 {
		t.Fatalf("frontier reachability files = %d, want recovered caller only", got)
	}
	if _, ok := r.reachableDirsByFile["a.go"]; !ok {
		t.Fatal("caller path was not recovered from the From node")
	}
	if _, ok := r.reachableDirsByFile["other/other.go"]; ok {
		t.Fatal("missing edge FilePath triggered unrelated reachability work")
	}
}

func TestBuildPassIndexesForPendingUnknownCallerDoesNotBuildGlobalReachability(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "other/other.go", Kind: graph.KindFile, Name: "other.go", FilePath: "other/other.go"})
	r := New(g)

	clear := r.buildPassIndexesForPending([]*graph.Edge{{
		From: "missing::Caller", To: graph.UnresolvedMarker + "Work", Kind: graph.EdgeCalls,
	}})
	defer clear()

	if r.reachableDirsByFile == nil {
		t.Fatal("bounded empty reachability index was not installed")
	}
	if len(r.reachableDirsByFile) != 0 {
		t.Fatalf("unknown caller reachability = %#v, want empty fail-open frontier", r.reachableDirsByFile)
	}
}

func TestBoundImplsForBuildsProvidesIndexLazily(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "module", Kind: graph.KindType, Name: "Module", FilePath: "module.ts"},
		{ID: "repo::EmailNotifier", Kind: graph.KindType, Name: "EmailNotifier", FilePath: "email.ts"},
	}, []*graph.Edge{{
		From: "module", To: "repo::EmailNotifier", Kind: graph.EdgeProvides,
		Meta: map[string]any{"provides_for": "Notifier", "binding": "useClass"},
	}})

	r := New(g)
	if r.providesForIdx != nil {
		t.Fatal("provides index must start lazy")
	}
	bound := r.boundImplsFor("Notifier")
	if _, ok := bound["EmailNotifier"]; !ok {
		t.Fatalf("lazy provides lookup = %#v, want EmailNotifier", bound)
	}
	if r.providesForIdx == nil {
		t.Fatal("provides index was not cached after first lookup")
	}
}

func TestResolveFilesAndIncomingNoPendingBuildsNoIndexes(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go"})
	r := New(g)
	stats := r.ResolveFilesAndIncoming([]string{"a.go", "a.go"})
	if stats == nil || stats.Resolved != 0 || stats.Unresolved != 0 {
		t.Fatalf("no-pending stats = %#v, want zero", stats)
	}
	if r.dirIndex != nil || r.depModuleIndex != nil || r.providesForIdx != nil || r.reachableDirsByFile != nil {
		t.Fatal("no-pending batch left pass indexes allocated")
	}
}

func TestResolveFilesAndIncomingKeepsAttributionOnExactFiles(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go", Language: "go"},
		{ID: "a.go::Caller", Kind: graph.KindFunction, Name: "Caller", FilePath: "a.go", Language: "go"},
		{ID: "b.go", Kind: graph.KindFile, Name: "b.go", FilePath: "b.go", Language: "go"},
		{ID: "b.go::Caller", Kind: graph.KindFunction, Name: "Caller", FilePath: "b.go", Language: "go"},
	}, []*graph.Edge{
		{From: "a.go::Caller", To: graph.UnresolvedMarker + "Missing", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 3},
		{From: "b.go::Caller", To: graph.UnresolvedMarker + "len", Kind: graph.EdgeCalls, FilePath: "b.go", Line: 4},
	})

	New(g).ResolveFilesAndIncoming([]string{"a.go"})

	edges := g.GetOutEdges("b.go::Caller")
	if len(edges) != 1 || edges[0].To != graph.UnresolvedMarker+"len" {
		t.Fatalf("unrelated file attribution changed edge = %#v", edges)
	}
	if g.GetNode(graph.StubID("", graph.StubKindBuiltin, "go", "len")) != nil {
		t.Fatal("unrelated file caused whole-graph builtin attribution")
	}
}
