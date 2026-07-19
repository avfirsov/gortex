package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func openEdgeCandidateStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "edge-candidates.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedEdgeCandidate(t *testing.T, store *Store) {
	t.Helper()
	store.AddBatch([]*graph.Node{
		{ID: "a", Kind: graph.KindFunction, Name: "a"},
		{ID: "b", Kind: graph.KindFunction, Name: "b"},
	}, []*graph.Edge{{
		From: "a", To: "b", Kind: graph.EdgeCalls,
		FilePath: "a.go", Line: 10,
		Meta: map[string]any{"semantic": "candidate"},
	}})
}

func TestSQLiteEdgeCandidatesCanonicalizeAcrossLookupShapes(t *testing.T) {
	store := openEdgeCandidateStore(t)
	seedEdgeCandidate(t, store)

	candidates := store.GetEdgeCandidates(
		[]graph.EdgeEndpoint{{From: "a", To: "b"}, {From: "a", To: "b"}},
		[]graph.EdgeSite{
			{From: "a", Line: 10, Kind: graph.EdgeCalls},
			{From: "a", Line: 10},
			{From: "a", Line: 10, Kind: graph.EdgeCalls},
		},
	)

	endpoint := candidates.Endpoint("a", "b")
	exactSite := candidates.Site("a", 10, graph.EdgeCalls)
	anySite := candidates.Site("a", 10, "")
	if endpoint == nil {
		t.Fatal("endpoint candidate is missing")
	}
	if len(exactSite) != 1 {
		t.Fatalf("exact-site candidates = %d, want 1", len(exactSite))
	}
	if len(anySite) != 1 {
		t.Fatalf("any-kind site candidates = %d, want 1", len(anySite))
	}
	if endpoint != exactSite[0] || endpoint != anySite[0] {
		t.Fatalf("candidate pointers differ: endpoint=%p exact=%p any=%p", endpoint, exactSite[0], anySite[0])
	}

	endpoint.To = "reindexed"
	if exactSite[0].To != "reindexed" || anySite[0].To != "reindexed" {
		t.Fatal("site buckets did not observe mutation through the canonical edge pointer")
	}
}

func TestSQLiteEdgeCandidatesDoNotHideDecodeFailures(t *testing.T) {
	store := openEdgeCandidateStore(t)
	seedEdgeCandidate(t, store)
	if _, err := store.writerDB.Exec(`UPDATE edges SET meta = ?`, []byte(`{`)); err != nil {
		t.Fatalf("corrupt edge metadata: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("GetEdgeCandidates silently treated corrupt metadata as no candidates")
		}
	}()
	_ = store.GetEdgeCandidates([]graph.EdgeEndpoint{{From: "a", To: "b"}}, nil)
}

func TestSQLiteEdgeCandidatesDoNotHideQueryFailures(t *testing.T) {
	store := openEdgeCandidateStore(t)
	seedEdgeCandidate(t, store)
	if _, err := store.writerDB.Exec(`DROP TABLE edges`); err != nil {
		t.Fatalf("drop edges table: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("GetEdgeCandidates silently treated a failed SQL query as no candidates")
		}
	}()
	_ = store.GetEdgeCandidates([]graph.EdgeEndpoint{{From: "a", To: "b"}}, nil)
}

func TestSQLiteEdgeCandidatesClosedStoreReturnsEmpty(t *testing.T) {
	store := openEdgeCandidateStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	candidates := store.GetEdgeCandidates(
		[]graph.EdgeEndpoint{{From: "a", To: "b"}},
		[]graph.EdgeSite{{From: "a", Line: 10}},
	)
	if got := candidates.Endpoint("a", "b"); got != nil {
		t.Fatalf("closed-store endpoint candidate = %#v, want nil", got)
	}
	if got := candidates.Site("a", 10, ""); len(got) != 0 {
		t.Fatalf("closed-store site candidates = %#v, want empty", got)
	}
}
