package analysis

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

// buildSyntheticGraph creates a graph with the given number of function nodes
// and approximately 2*nodeCount random-ish call edges for benchmarking.
func buildSyntheticGraph(b *testing.B, nodeCount int) *graph.Graph {
	b.Helper()
	g := graph.New()
	for i := 0; i < nodeCount; i++ {
		g.AddNode(&graph.Node{
			ID:        fmt.Sprintf("pkg/file%d.go::Func%d", i/100, i),
			Kind:      graph.KindFunction,
			Name:      fmt.Sprintf("Func%d", i),
			FilePath:  fmt.Sprintf("pkg/file%d.go", i/100),
			StartLine: (i % 100) + 1,
			EndLine:   (i % 100) + 10,
			Language:  "go",
		})
	}
	// Add ~2x edges with a deterministic pattern
	for i := 0; i < nodeCount; i++ {
		// Each node calls the next 2 nodes (wrapping)
		t1 := (i + 1) % nodeCount
		t2 := (i + 7) % nodeCount
		g.AddEdge(&graph.Edge{
			From: fmt.Sprintf("pkg/file%d.go::Func%d", i/100, i),
			To:   fmt.Sprintf("pkg/file%d.go::Func%d", t1/100, t1),
			Kind: graph.EdgeCalls,
		})
		g.AddEdge(&graph.Edge{
			From: fmt.Sprintf("pkg/file%d.go::Func%d", i/100, i),
			To:   fmt.Sprintf("pkg/file%d.go::Func%d", t2/100, t2),
			Kind: graph.EdgeCalls,
		})
	}
	return g
}

// buildCommunities creates a simple CommunityResult assigning nodes to communities
// based on their file grouping (every 100 nodes = 1 community).
func buildCommunities(g *graph.Graph) *CommunityResult {
	nodes := g.AllNodes()
	result := &CommunityResult{
		NodeToComm: make(map[string]string),
	}
	commMap := make(map[string][]string)
	for _, n := range nodes {
		commID := fmt.Sprintf("community-%s", n.FilePath)
		result.NodeToComm[n.ID] = commID
		commMap[commID] = append(commMap[commID], n.ID)
	}
	for id, members := range commMap {
		result.Communities = append(result.Communities, Community{
			ID:      id,
			Label:   id,
			Members: members,
			Size:    len(members),
		})
	}
	return result
}

// --- BenchmarkDetectCycles ---

func BenchmarkDetectCycles_1k(b *testing.B) {
	g := buildSyntheticGraph(b, 1000)
	comms := buildCommunities(g)
	b.ResetTimer()
	for b.Loop() {
		DetectCycles(g, comms, "")
	}
}

func BenchmarkDetectCycles_10k(b *testing.B) {
	g := buildSyntheticGraph(b, 10000)
	comms := buildCommunities(g)
	b.ResetTimer()
	for b.Loop() {
		DetectCycles(g, comms, "")
	}
}

func BenchmarkDetectCycles_100k(b *testing.B) {
	g := buildSyntheticGraph(b, 100000)
	comms := buildCommunities(g)
	b.ResetTimer()
	for b.Loop() {
		DetectCycles(g, comms, "")
	}
}

// --- BenchmarkFindDeadCode ---

func BenchmarkFindDeadCode_1k(b *testing.B) {
	g := buildSyntheticGraph(b, 1000)
	procs := &ProcessResult{NodeToProcs: make(map[string][]string)}
	b.ResetTimer()
	for b.Loop() {
		FindDeadCode(g, procs, nil)
	}
}

func BenchmarkFindDeadCode_10k(b *testing.B) {
	g := buildSyntheticGraph(b, 10000)
	procs := &ProcessResult{NodeToProcs: make(map[string][]string)}
	b.ResetTimer()
	for b.Loop() {
		FindDeadCode(g, procs, nil)
	}
}

func BenchmarkFindDeadCode_100k(b *testing.B) {
	g := buildSyntheticGraph(b, 100000)
	procs := &ProcessResult{NodeToProcs: make(map[string][]string)}
	b.ResetTimer()
	for b.Loop() {
		FindDeadCode(g, procs, nil)
	}
}

// --- BenchmarkFindHotspots ---

func BenchmarkFindHotspots_1k(b *testing.B) {
	g := buildSyntheticGraph(b, 1000)
	comms := buildCommunities(g)
	b.ResetTimer()
	for b.Loop() {
		FindHotspots(g, comms, 0)
	}
}

func BenchmarkFindHotspots_10k(b *testing.B) {
	g := buildSyntheticGraph(b, 10000)
	comms := buildCommunities(g)
	b.ResetTimer()
	for b.Loop() {
		FindHotspots(g, comms, 0)
	}
}

func BenchmarkFindHotspots_100k(b *testing.B) {
	g := buildSyntheticGraph(b, 100000)
	comms := buildCommunities(g)
	b.ResetTimer()
	for b.Loop() {
		FindHotspots(g, comms, 0)
	}
}

// --- BenchmarkVerifyChanges ---

// buildGraphWithCallers creates a graph with one target function and N callers.
func buildGraphWithCallers(b *testing.B, callerCount int) (*graph.Graph, *query.Engine) {
	b.Helper()
	g := graph.New()
	targetID := "pkg/target.go::Target"
	g.AddNode(&graph.Node{
		ID:        targetID,
		Kind:      graph.KindFunction,
		Name:      "Target",
		FilePath:  "pkg/target.go",
		StartLine: 1,
		EndLine:   10,
		Language:  "go",
		Meta:      map[string]any{"signature": "func(ctx context.Context, id string) error"},
	})
	for i := 0; i < callerCount; i++ {
		callerID := fmt.Sprintf("pkg/caller%d.go::Caller%d", i, i)
		g.AddNode(&graph.Node{
			ID:        callerID,
			Kind:      graph.KindFunction,
			Name:      fmt.Sprintf("Caller%d", i),
			FilePath:  fmt.Sprintf("pkg/caller%d.go", i),
			StartLine: 1,
			EndLine:   5,
			Language:  "go",
		})
		g.AddEdge(&graph.Edge{
			From: callerID,
			To:   targetID,
			Kind: graph.EdgeCalls,
		})
	}
	eng := query.NewEngine(g)
	return g, eng
}

func BenchmarkVerifyChanges_10callers(b *testing.B) {
	g, eng := buildGraphWithCallers(b, 10)
	changes := []SignatureChange{{SymbolID: "pkg/target.go::Target", NewSignature: "func(ctx context.Context, id string, extra int) error"}}
	b.ResetTimer()
	for b.Loop() {
		VerifyChanges(g, eng, changes)
	}
}

func BenchmarkVerifyChanges_100callers(b *testing.B) {
	g, eng := buildGraphWithCallers(b, 100)
	changes := []SignatureChange{{SymbolID: "pkg/target.go::Target", NewSignature: "func(ctx context.Context, id string, extra int) error"}}
	b.ResetTimer()
	for b.Loop() {
		VerifyChanges(g, eng, changes)
	}
}

func BenchmarkVerifyChanges_1000callers(b *testing.B) {
	g, eng := buildGraphWithCallers(b, 1000)
	changes := []SignatureChange{{SymbolID: "pkg/target.go::Target", NewSignature: "func(ctx context.Context, id string, extra int) error"}}
	b.ResetTimer()
	for b.Loop() {
		VerifyChanges(g, eng, changes)
	}
}

// --- BenchmarkEnsureFresh ---
// ensureFresh is in the mcp package, so we benchmark the IsStale check pattern
// which is the core of the ensureFresh hot path.

func BenchmarkIsStaleCheck_100files(b *testing.B) {
	benchmarkIsStalePattern(b, 100)
}

func BenchmarkIsStaleCheck_1000files(b *testing.B) {
	benchmarkIsStalePattern(b, 1000)
}

func BenchmarkIsStaleCheck_10000files(b *testing.B) {
	benchmarkIsStalePattern(b, 10000)
}

// benchmarkIsStalePattern simulates the ensureFresh file-staleness check loop.
// It builds a map of file mtimes and iterates over N files checking staleness.
func benchmarkIsStalePattern(b *testing.B, fileCount int) {
	b.Helper()
	// Build a mtime map simulating the indexer's fileMtimes
	mtimes := make(map[string]int64, fileCount)
	files := make([]string, fileCount)
	for i := 0; i < fileCount; i++ {
		fp := fmt.Sprintf("pkg/dir%d/file%d.go", i/100, i)
		files[i] = fp
		mtimes[fp] = int64(i * 1000000) // fake mtime
	}

	b.ResetTimer()
	for b.Loop() {
		// Simulate ensureFresh: check up to 5 stale files
		limit := 5
		refreshed := 0
		for _, fp := range files {
			if refreshed >= limit {
				break
			}
			// Simulate IsStale check: lookup in map
			if _, ok := mtimes[fp]; ok {
				refreshed++
			}
		}
	}
}
