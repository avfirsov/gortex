package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
)

// copyingStore simulates persistent backends (sqlite): every read
// materialises fresh Edge values, so pointer identity never holds across
// reads. The chunked ResolveAll liveness gate compared pointers only —
// on such backends every computed resolution was judged stale and
// silently dropped, turning the daemon's whole master resolve pass into
// a no-op while the CLI's in-memory path kept working.
type copyingStore struct {
	graph.Store
}

func (c copyingStore) GetOutEdges(id string) []*graph.Edge {
	src := c.Store.GetOutEdges(id)
	out := make([]*graph.Edge, len(src))
	for i, e := range src {
		cp := *e
		out[i] = &cp
	}
	return out
}

func (c copyingStore) GetEdgeCandidates(
	endpoints []graph.EdgeEndpoint, sites []graph.EdgeSite,
) graph.EdgeCandidateSet {
	src := c.Store.GetEdgeCandidates(endpoints, sites)
	out := graph.NewEdgeCandidateSet()
	for _, site := range sites {
		for _, edge := range src.Site(site.From, site.Line, site.Kind) {
			cp := *edge
			out.AddSite(&cp)
		}
	}
	return out
}

func TestEdgeLivenessBatch_ValueIdentityOnCopyingStore(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.go::A", Kind: graph.KindFunction, Name: "A", FilePath: "a.go"})
	live := &graph.Edge{From: "a.go::A", To: "unresolved::B", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 7}
	g.AddEdge(live)

	cs := copyingStore{Store: g}
	copied := loadEdgeLiveness(cs, []*graph.Edge{live})
	assert.True(t, copied.containsEdge(live),
		"a live edge must be recognised through a store that returns copies")

	// Pointer identity still suffices on in-memory stores.
	inMemory := loadEdgeLiveness(g, []*graph.Edge{live})
	assert.True(t, inMemory.containsEdge(live))

	gone := *live
	gone.Line = 999
	missing := loadEdgeLiveness(cs, []*graph.Edge{&gone})
	assert.False(t, missing.containsEdge(&gone),
		"an edge that no longer exists at that call site must not count as live")
	assert.False(t, missing.containsEdge(nil))
}
