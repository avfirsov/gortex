package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// connectivityReport is the JSON shape of an analyze
// kind=connectivity_health response.
type connectivityReport struct {
	NominalNodes   int     `json:"nominal_nodes"`
	EffectiveNodes int     `json:"effective_nodes"`
	EffectiveRatio float64 `json:"effective_ratio"`
	Isolated       int     `json:"isolated"`
	Leaf           int     `json:"leaf"`
	SourceOnly     int     `json:"source_only"`
	SinkOnly       int     `json:"sink_only"`
	ByKind         []struct {
		Kind     string `json:"kind"`
		Total    int    `json:"total"`
		Isolated int    `json:"isolated"`
		Leaf     int    `json:"leaf"`
	} `json:"by_kind"`
	DeadWeightByFile []struct {
		FilePath   string `json:"file_path"`
		Isolated   int    `json:"isolated"`
		Leaf       int    `json:"leaf"`
		DeadWeight int    `json:"dead_weight"`
	} `json:"dead_weight_by_file"`
	Note string `json:"note"`
}

// connectivityFixture wires a Server around a hand-built graph with a
// known mix of connected, leaf, and isolated nodes:
//
//   - good.go holds a call chain A->B->C: B is degree-2 (non-leaf),
//     A and C are degree-1 leaves.
//   - gap.go holds three functions with zero edges of any kind —
//     three extraction gaps.
func connectivityFixture(t *testing.T) *Server {
	t.Helper()
	srv := concurrencyServer(t)
	g := srv.graph
	addFn(g, "good.go::A", "A", "good.go")
	addFn(g, "good.go::B", "B", "good.go")
	addFn(g, "good.go::C", "C", "good.go")
	addEdge(g, "good.go::A", "good.go::B", graph.EdgeCalls, "good.go", 1)
	addEdge(g, "good.go::B", "good.go::C", graph.EdgeCalls, "good.go", 2)
	addFn(g, "gap.go::Lost1", "Lost1", "gap.go")
	addFn(g, "gap.go::Lost2", "Lost2", "gap.go")
	addFn(g, "gap.go::Lost3", "Lost3", "gap.go")
	return srv
}

// TestAnalyzeConnectivityHealth_StructuredResult asserts the
// connectivity_health kind returns the structured report with the
// expected isolated / leaf / effective-vs-nominal counts and the
// per-file dead-weight attribution.
func TestAnalyzeConnectivityHealth_StructuredResult(t *testing.T) {
	srv := connectivityFixture(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = map[string]any{"kind": "connectivity_health"}
	res, err := srv.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	var report connectivityReport
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &report))

	assert.Equal(t, 6, report.NominalNodes, "A,B,C + Lost1,Lost2,Lost3")
	assert.Equal(t, 3, report.EffectiveNodes, "the A->B->C chain is connected")
	assert.InDelta(t, 0.5, report.EffectiveRatio, 1e-9)
	assert.Equal(t, 3, report.Isolated, "the three gap.go functions have zero edges")
	assert.Equal(t, 2, report.Leaf, "chain ends A and C each have degree 1")
	assert.Equal(t, 1, report.SourceOnly, "A (only outgoing)")
	assert.Equal(t, 1, report.SinkOnly, "C (only incoming)")

	// The dead-weight ranking localises the gap to gap.go: 3 isolated
	// nodes outrank good.go's 2 leaves.
	require.Len(t, report.DeadWeightByFile, 2)
	assert.Equal(t, "gap.go", report.DeadWeightByFile[0].FilePath,
		"the file with the most isolated/leaf nodes ranks first")
	assert.Equal(t, 3, report.DeadWeightByFile[0].Isolated)
	assert.Equal(t, 3, report.DeadWeightByFile[0].DeadWeight)
	assert.Equal(t, "good.go", report.DeadWeightByFile[1].FilePath)
	assert.Equal(t, 2, report.DeadWeightByFile[1].Leaf)

	// The note must spell out the extraction-vs-dead-code distinction.
	assert.Contains(t, report.Note, "dead_code")
	assert.Contains(t, report.Note, "extract", "note must frame this as an extraction diagnostic")
}

// TestAnalyzeConnectivityHealth_DispatcherRoutes asserts the analyze
// switch accepts kind=connectivity_health — regression-protects the
// dispatcher wiring against a stray rename.
func TestAnalyzeConnectivityHealth_DispatcherRoutes(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = map[string]any{"kind": "connectivity_health"}
	res, err := srv.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError,
		"dispatcher must route kind=connectivity_health without error; got %v", res)
}

// TestAnalyzeConnectivityHealth_GCXEncodesRecord asserts the GCX1 wire
// output carries the connectivity_health header and its scalar
// fields, so wire-format clients can decode the report.
func TestAnalyzeConnectivityHealth_GCXEncodesRecord(t *testing.T) {
	srv := connectivityFixture(t)

	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = map[string]any{"kind": "connectivity_health", "format": "gcx"}
	res, err := srv.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	text := res.Content[0].(mcplib.TextContent).Text
	assert.Contains(t, text, "analyze.connectivity_health")
	assert.Contains(t, text, "isolated")
	assert.Contains(t, text, "effective_nodes")
	assert.Contains(t, text, "nominal_nodes")
}

// TestAnalyzeConnectivityHealth_LimitCapsFileRanking asserts the limit
// argument truncates the dead_weight_by_file ranking.
func TestAnalyzeConnectivityHealth_LimitCapsFileRanking(t *testing.T) {
	srv := concurrencyServer(t)
	g := srv.graph
	// Three isolated functions, one per file.
	addFn(g, "a.go::A", "A", "a.go")
	addFn(g, "b.go::B", "B", "b.go")
	addFn(g, "c.go::C", "C", "c.go")

	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = map[string]any{"kind": "connectivity_health", "limit": float64(2)}
	res, err := srv.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	var report connectivityReport
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &report))
	assert.Len(t, report.DeadWeightByFile, 2, "limit=2 caps the file ranking")
	assert.Equal(t, 3, report.Isolated, "the isolated count is not affected by the file-ranking cap")
}
