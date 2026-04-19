package graph

import (
	"fmt"
	"sync"
	"testing"
)

// TestReindexEdge_ConcurrentAddEdge reproduces the "concurrent map read
// and map write" crash seen in production when two MCP handlers both
// trigger ensureFresh → indexFile: one calls AddEdge on shard From, the
// other calls ReindexEdge which mutates From's outEdgeIdx without
// locking From. Run with `-race` for the sharpest detection; even
// without -race the bare runtime map guard panics reliably here.
func TestReindexEdge_ConcurrentAddEdge(t *testing.T) {
	g := New()

	const n = 200
	for i := range n {
		g.AddNode(&Node{ID: fmt.Sprintf("from%d::F", i), Name: "F", Kind: KindFunction, FilePath: "f"})
		g.AddNode(&Node{ID: fmt.Sprintf("to%d::T", i), Name: "T", Kind: KindFunction, FilePath: "t"})
		g.AddNode(&Node{ID: fmt.Sprintf("alt%d::A", i), Name: "A", Kind: KindFunction, FilePath: "a"})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer A: AddEdge against the From shards.
	go func() {
		defer wg.Done()
		for round := range 50 {
			for i := range n {
				g.AddEdge(&Edge{
					From:     fmt.Sprintf("from%d::F", i),
					To:       fmt.Sprintf("to%d::T", i),
					Kind:     EdgeCalls,
					FilePath: "f",
					Line:     round,
				})
			}
		}
	}()

	// Writer B: ReindexEdge, retargeting onto a different shard each
	// round — this is the resolver path that would collide with A.
	go func() {
		defer wg.Done()
		for round := range 50 {
			for i := range n {
				e := &Edge{
					From:     fmt.Sprintf("from%d::F", i),
					To:       fmt.Sprintf("to%d::T", i),
					Kind:     EdgeCalls,
					FilePath: "f",
					Line:     round + 1000,
				}
				g.AddEdge(e)
				oldTo := e.To
				e.To = fmt.Sprintf("alt%d::A", i)
				g.ReindexEdge(e, oldTo)
			}
		}
	}()

	wg.Wait()
}
