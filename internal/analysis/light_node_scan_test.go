package analysis

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// lightNodeOnlyStore fails loudly if a global analysis regresses to the full
// node scan. Its light projection delegates to the in-memory graph because no
// metadata decoding is involved there.
type lightNodeOnlyStore struct {
	graph.Store
	lightCalls int
}

func (s *lightNodeOnlyStore) AllNodes() []*graph.Node {
	panic("global analysis used the full node scan")
}

func (s *lightNodeOnlyStore) AllNodesLight() []*graph.Node {
	s.lightCalls++
	full := s.Store.AllNodes()
	out := make([]*graph.Node, 0, len(full))
	for _, n := range full {
		out = append(out, &graph.Node{
			ID: n.ID, Kind: n.Kind, Name: n.Name, QualName: n.QualName,
			FilePath: n.FilePath, StartLine: n.StartLine, EndLine: n.EndLine,
			StartColumn: n.StartColumn, EndColumn: n.EndColumn, Language: n.Language,
			RepoPrefix: n.RepoPrefix, WorkspaceID: n.WorkspaceID, ProjectID: n.ProjectID,
			Origin: n.Origin, Stub: n.Stub, FetchedAt: n.FetchedAt,
		})
	}
	return out
}

func TestDetectCommunitiesLeidenUsesLightNodesForBuildAndResult(t *testing.T) {
	base := graph.New()
	base.AddNode(&graph.Node{
		ID:       "repo/pkg/a.go::A",
		Kind:     graph.KindFunction,
		Name:     "A",
		FilePath: "pkg/a.go",
	})
	base.AddNode(&graph.Node{
		ID:       "repo/pkg/b.go::B",
		Kind:     graph.KindFunction,
		Name:     "B",
		FilePath: "pkg/b.go",
	})
	base.AddEdge(&graph.Edge{
		From: "repo/pkg/a.go::A",
		To:   "repo/pkg/b.go::B",
		Kind: graph.EdgeCalls,
	})

	store := &lightNodeOnlyStore{Store: base}
	result := DetectCommunitiesLeiden(store)
	if result == nil {
		t.Fatal("DetectCommunitiesLeiden returned nil")
	}
	if store.lightCalls < 2 {
		t.Fatalf("light node scan calls = %d, want at least 2 (graph build and result materialization)", store.lightCalls)
	}
}

func TestCentralityUsesLightNodeProjection(t *testing.T) {
	base := graph.New()
	base.AddNode(&graph.Node{ID: "repo/a.go::A", Kind: graph.KindFunction, Name: "A", FilePath: "a.go"})
	base.AddNode(&graph.Node{ID: "repo/b.go::B", Kind: graph.KindFunction, Name: "B", FilePath: "b.go"})
	base.AddEdge(&graph.Edge{From: "repo/a.go::A", To: "repo/b.go::B", Kind: graph.EdgeCalls})

	store := &lightNodeOnlyStore{Store: base}
	pageRank := ComputePageRank(store)
	if len(pageRank.Scores) != 2 {
		t.Fatalf("PageRank scores = %d, want 2", len(pageRank.Scores))
	}
	hits := ComputeHITS(store)
	if len(hits.Authorities) != 2 || len(hits.Hubs) != 2 {
		t.Fatalf("HITS authorities/hubs = %d/%d, want 2/2", len(hits.Authorities), len(hits.Hubs))
	}
	if store.lightCalls != 2 {
		t.Fatalf("centrality light node scan calls = %d, want 2", store.lightCalls)
	}
}
