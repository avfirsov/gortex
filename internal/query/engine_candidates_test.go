package query

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
	"github.com/zzet/gortex/internal/search/rerank"
)

type candidateChannelBackend struct {
	text   []search.SearchResult
	vector []string
}

func (b *candidateChannelBackend) Add(string, ...string) {}
func (b *candidateChannelBackend) Remove(string)         {}
func (b *candidateChannelBackend) Search(string, int) []search.SearchResult {
	return append([]search.SearchResult(nil), b.text...)
}
func (b *candidateChannelBackend) SearchChannels(string, int) ([]search.SearchResult, []string) {
	return append([]search.SearchResult(nil), b.text...), append([]string(nil), b.vector...)
}
func (b *candidateChannelBackend) Count() int { return len(b.text) + len(b.vector) }
func (b *candidateChannelBackend) Close()     {}

func TestGatherSymbolCandidatesPreservesHybridRanksAndScope(t *testing.T) {
	g := graph.New()
	for _, node := range []*graph.Node{
		{ID: "text", Name: "text", Kind: graph.KindFunction, WorkspaceID: "wanted"},
		{ID: "vector", Name: "vector", Kind: graph.KindFunction, WorkspaceID: "wanted"},
		{ID: "outside", Name: "outside", Kind: graph.KindFunction, WorkspaceID: "other"},
	} {
		g.AddNode(node)
	}

	backend := &candidateChannelBackend{
		text:   []search.SearchResult{{ID: "text", Score: 1}},
		vector: []string{"vector", "outside"},
	}
	engine := NewEngine(g)
	engine.SetSearch(backend)
	opts := QueryOptions{WorkspaceID: "wanted", SkipInnerRerank: true}

	gathered := engine.GatherSymbolCandidates("concept", 1, opts, nil)
	if len(gathered) != 2 {
		t.Fatalf("gathered %d candidates, want text and vector-only hits: %#v", len(gathered), gathered)
	}
	byID := make(map[string]*rerank.Candidate, len(gathered))
	for _, candidate := range gathered {
		byID[candidate.Node.ID] = candidate
	}
	if got := byID["text"]; got == nil || got.TextRank != 0 || got.VectorRank != -1 {
		t.Fatalf("text rank not preserved: %#v", got)
	}
	if got := byID["vector"]; got == nil || got.TextRank != -1 || got.VectorRank != 0 {
		t.Fatalf("vector-only rank not preserved: %#v", got)
	}
	if byID["outside"] != nil {
		t.Fatalf("workspace scope leaked unrelated candidate: %#v", byID["outside"])
	}

	ranked := engine.SearchSymbolsRanked("concept", 1, opts, nil)
	if len(ranked) != 1 || ranked[0].Node.ID != "text" {
		t.Fatalf("ranked compatibility no longer truncates to the requested limit: %#v", ranked)
	}
}
