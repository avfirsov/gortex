package analysis

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

// TestLeidenVsLouvainOnLiveGraph loads the cached gortex graph and
// runs both algorithms on it so we can see real numbers. Skipped
// unless GORTEX_BENCH_COMMUNITIES=1 is set so it doesn't run in
// normal test invocations.
//
// Run:  GORTEX_BENCH_COMMUNITIES=1 go test ./internal/analysis/ -run TestLeidenVsLouvainOnLiveGraph -v
func TestLeidenVsLouvainOnLiveGraph(t *testing.T) {
	if os.Getenv("GORTEX_BENCH_COMMUNITIES") != "1" {
		t.Skip("set GORTEX_BENCH_COMMUNITIES=1 to enable")
	}
	g, err := loadCachedGraph()
	if err != nil {
		t.Skipf("no cached graph available: %v", err)
	}
	t.Logf("graph: %d nodes, %d edges", len(g.AllNodes()), len(g.AllEdges()))

	report := func(name string, r *CommunityResult, dur time.Duration) {
		sizes := make([]int, len(r.Communities))
		for i, c := range r.Communities {
			sizes[i] = c.Size
		}
		sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
		topFive := sizes
		if len(topFive) > 5 {
			topFive = topFive[:5]
		}
		// Sibling-group spread: how many distinct parent_ids and
		// what's the largest one?
		parentCount := make(map[string]int)
		for _, c := range r.Communities {
			if c.ParentID != "" {
				parentCount[c.ParentID]++
			}
		}
		maxParent, maxN := "", 0
		for pid, n := range parentCount {
			if n > maxN {
				maxParent = pid
				maxN = n
			}
		}
		t.Logf("%s: %d communities · modularity %.3f · max-size %d · top-5 sizes %v · %d parent groups (biggest: %s with %d children) · %v",
			name, len(r.Communities), r.Modularity, sizes[0], topFive, len(parentCount), maxParent, maxN, dur)
	}

	t0 := time.Now()
	louvain := DetectCommunitiesLouvain(g)
	report("Louvain", louvain, time.Since(t0))

	t0 = time.Now()
	leiden := DetectCommunitiesLeiden(g)
	report("Leiden ", leiden, time.Since(t0))
}

// loadCachedGraph pulls the graph from a running daemon via the
// /v1/graph endpoint and reconstructs a *graph.Graph from it. The
// daemon URL is read from $GORTEX_BENCH_URL (default 4747).
func loadCachedGraph() (*graph.Graph, error) {
	url := os.Getenv("GORTEX_BENCH_URL")
	if url == "" {
		url = "http://localhost:4747/v1/graph"
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d from %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Nodes []struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			FilePath  string `json:"file_path"`
			StartLine int    `json:"start_line"`
			Language  string `json:"language"`
		} `json:"nodes"`
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
			Kind string `json:"kind"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	g := graph.New()
	for _, n := range payload.Nodes {
		g.AddNode(&graph.Node{
			ID:        n.ID,
			Kind:      graph.NodeKind(n.Kind),
			Name:      n.Name,
			FilePath:  n.FilePath,
			StartLine: n.StartLine,
			Language:  n.Language,
		})
	}
	for _, e := range payload.Edges {
		g.AddEdge(&graph.Edge{
			From: e.From, To: e.To,
			Kind: graph.EdgeKind(e.Kind),
		})
	}
	return g, nil
}

// Silence the unused-import lint in case `strings` ends up not being
// needed after edits.
var _ = strings.HasPrefix
