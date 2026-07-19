package scip

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

func TestSCIPContextHelper(t *testing.T) {
	if os.Getenv("GORTEX_SCIP_CONTEXT_HELPER") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
}

func TestEnrichRepoContextCancelsIndexerProcess(t *testing.T) {
	t.Setenv("GORTEX_SCIP_CONTEXT_HELPER", "1")
	p := NewProvider(os.Args[0], []string{"-test.run=^TestSCIPContextHelper$", "--"}, []string{"go"}, 30, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, err := p.EnrichRepoContext(ctx, graph.New(), "", t.TempDir(), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Partial)
	assert.Contains(t, result.AbortReason, "deadline exceeded")
	assert.Less(t, time.Since(start), 2*time.Second, "cancelled SCIP child must not outlive the manager deadline")
}

func TestSCIPProtobufRoundTrip(t *testing.T) {
	index := &SCIPIndex{
		Documents: []SCIPDocument{
			{
				RelativePath: "main.go",
				Occurrences: []SCIPOccurrence{
					{
						Range:       []int32{9, 5, 9, 9}, // line 10 (0-indexed=9), col 5-9
						Symbol:      "scip-go gomod example.com v1.0.0 main.Foo().",
						SymbolRoles: 1, // Definition
					},
					{
						Range:       []int32{14, 2, 14, 5},
						Symbol:      "scip-go gomod example.com v1.0.0 main.Bar().",
						SymbolRoles: 0, // Reference
					},
				},
				Symbols: []SCIPSymbolInfo{
					{
						Symbol:        "scip-go gomod example.com v1.0.0 main.Foo().",
						Documentation: []string{"func Foo() error"},
					},
				},
			},
		},
	}

	// Encode to protobuf.
	data := encodeSCIPForTesting(index)
	require.NotEmpty(t, data)

	// Decode back.
	decoded, err := decodeSCIPProtobuf(data)
	require.NoError(t, err)
	require.Len(t, decoded.Documents, 1)

	doc := decoded.Documents[0]
	assert.Equal(t, "main.go", doc.RelativePath)
	require.Len(t, doc.Occurrences, 2)

	// Check definition.
	assert.Equal(t, int32(9), doc.Occurrences[0].Range[0])
	assert.True(t, doc.Occurrences[0].IsDefinition())
	assert.Equal(t, 10, doc.Occurrences[0].StartLine()) // 0-indexed → 1-indexed

	// Check reference.
	assert.False(t, doc.Occurrences[1].IsDefinition())

	// Check symbol info.
	require.Len(t, doc.Symbols, 1)
	assert.Contains(t, doc.Symbols[0].Documentation[0], "func Foo()")
}

func TestSCIPJSONParsing(t *testing.T) {
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "index.scip")

	jsonData := `{
		"documents": [
			{
				"relative_path": "pkg/handler.go",
				"occurrences": [
					{"range": [4, 5, 4, 11], "symbol": "pkg.Handle()", "symbol_roles": 1},
					{"range": [10, 2, 10, 8], "symbol": "pkg.Handle()", "symbol_roles": 0}
				],
				"symbols": [
					{
						"symbol": "pkg.Handle()",
						"documentation": ["func Handle(w http.ResponseWriter, r *http.Request)"],
						"relationships": [
							{"symbol": "http.Handler", "is_implementation": true}
						]
					}
				]
			}
		]
	}`

	require.NoError(t, os.WriteFile(jsonFile, []byte(jsonData), 0644))

	index, err := ParseSCIPFile(jsonFile)
	require.NoError(t, err)
	require.Len(t, index.Documents, 1)

	doc := index.Documents[0]
	assert.Equal(t, "pkg/handler.go", doc.RelativePath)
	require.Len(t, doc.Occurrences, 2)
	require.Len(t, doc.Symbols, 1)
	require.Len(t, doc.Symbols[0].Relationships, 1)
	assert.True(t, doc.Symbols[0].Relationships[0].IsImplementation)
}

func TestEnrichFromIndex(t *testing.T) {
	logger := zap.NewNop()
	p := NewProvider("scip-go", nil, []string{"go"}, 120, logger)

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "main.go::Foo", Kind: graph.KindFunction, Name: "Foo",
		FilePath: "main.go", StartLine: 10, EndLine: 20, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::Bar", Kind: graph.KindFunction, Name: "Bar",
		FilePath: "main.go", StartLine: 22, EndLine: 30, Language: "go",
	})

	// Add an INFERRED edge.
	g.AddEdge(&graph.Edge{
		From: "main.go::Bar", To: "main.go::Foo", Kind: graph.EdgeCalls,
		Confidence: 0.7, ConfidenceLabel: "INFERRED",
	})

	// Create a SCIP index that confirms the call.
	index := &SCIPIndex{
		Documents: []SCIPDocument{
			{
				RelativePath: "main.go",
				Occurrences: []SCIPOccurrence{
					{
						Range:       []int32{9, 5, 9, 8}, // line 10 (Foo definition)
						Symbol:      "main.Foo()",
						SymbolRoles: 1, // Definition
					},
					{
						Range:       []int32{21, 5, 21, 8}, // line 22 (Bar definition)
						Symbol:      "main.Bar()",
						SymbolRoles: 1, // Definition
					},
					{
						Range:       []int32{24, 2, 24, 5}, // line 25 (call to Foo from Bar)
						Symbol:      "main.Foo()",
						SymbolRoles: 0, // Reference
					},
				},
				Symbols: []SCIPSymbolInfo{
					{
						Symbol:        "main.Foo()",
						Documentation: []string{"func Foo() error"},
					},
				},
			},
		},
	}

	result := p.enrichFromIndex(g, index, "/tmp/repo")

	assert.Greater(t, result.SymbolsCovered, 0)
	assert.Greater(t, result.EdgesConfirmed, 0)

	// Verify the edge was confirmed.
	edges := g.GetOutEdges("main.go::Bar")
	require.NotEmpty(t, edges)
	for _, e := range edges {
		if e.To == "main.go::Foo" && e.Kind == graph.EdgeCalls {
			assert.Equal(t, 1.0, e.Confidence)
			assert.Equal(t, "EXTRACTED", e.ConfidenceLabel)
		}
	}
}

func TestEnrichFromIndexScopedUsesPrefixedMultiRepoPaths(t *testing.T) {
	p := NewProvider("scip-go", nil, []string{"go"}, 120, zap.NewNop())
	g := graph.New()
	for _, repo := range []string{"repo-a", "repo-b"} {
		g.AddBatch([]*graph.Node{
			{ID: repo + "/main.go::Foo", Kind: graph.KindFunction, Name: "Foo", FilePath: repo + "/main.go", StartLine: 10, EndLine: 20, Language: "go", RepoPrefix: repo},
			{ID: repo + "/main.go::Bar", Kind: graph.KindFunction, Name: "Bar", FilePath: repo + "/main.go", StartLine: 22, EndLine: 30, Language: "go", RepoPrefix: repo},
		}, []*graph.Edge{{
			From: repo + "/main.go::Bar", To: repo + "/main.go::Foo", Kind: graph.EdgeCalls,
			FilePath: repo + "/main.go", Line: 25, Confidence: 0.7, ConfidenceLabel: "INFERRED",
		}})
	}
	index := &SCIPIndex{Documents: []SCIPDocument{{
		RelativePath: "main.go",
		Occurrences: []SCIPOccurrence{
			{Range: []int32{9, 5, 9, 8}, Symbol: "main.Foo()", SymbolRoles: 1},
			{Range: []int32{21, 5, 21, 8}, Symbol: "main.Bar()", SymbolRoles: 1},
			{Range: []int32{24, 2, 24, 5}, Symbol: "main.Foo()"},
		},
	}}}

	result := p.enrichFromIndexScoped(g, index, "repo-a", "/tmp/repo-a")

	assert.Equal(t, 2, result.SymbolsTotal)
	assert.Equal(t, 1, result.EdgesConfirmed)
	repoAEdges := g.GetOutEdges("repo-a/main.go::Bar")
	require.Len(t, repoAEdges, 1)
	assert.Equal(t, 1.0, repoAEdges[0].Confidence)
	repoBEdges := g.GetOutEdges("repo-b/main.go::Bar")
	require.Len(t, repoBEdges, 1)
	assert.Equal(t, 0.7, repoBEdges[0].Confidence, "sibling repo with the same relative path must remain untouched")
}

// TestEnrichFromIndex_DefinitionsOnly verifies the C# / .NET coverage
// helper fast path: definitions are mapped (coverage counted) but the
// reference-edge pass is skipped, so no edge is confirmed or added.
func TestEnrichFromIndex_DefinitionsOnly(t *testing.T) {
	p := NewProvider("scip-dotnet", nil, []string{"csharp"}, 120, zap.NewNop()).
		WithDefinitionsOnly()

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "M.cs::Foo", Kind: graph.KindMethod, Name: "Foo",
		FilePath: "M.cs", StartLine: 10, EndLine: 20, Language: "csharp",
	})
	g.AddNode(&graph.Node{
		ID: "M.cs::Bar", Kind: graph.KindMethod, Name: "Bar",
		FilePath: "M.cs", StartLine: 22, EndLine: 30, Language: "csharp",
	})
	g.AddEdge(&graph.Edge{
		From: "M.cs::Bar", To: "M.cs::Foo", Kind: graph.EdgeCalls,
		Confidence: 0.7, ConfidenceLabel: "INFERRED",
	})

	index := &SCIPIndex{Documents: []SCIPDocument{{
		RelativePath: "M.cs",
		Occurrences: []SCIPOccurrence{
			{Range: []int32{9, 5, 9, 8}, Symbol: "M.Foo()", SymbolRoles: 1},
			{Range: []int32{21, 5, 21, 8}, Symbol: "M.Bar()", SymbolRoles: 1},
			{Range: []int32{24, 2, 24, 5}, Symbol: "M.Foo()", SymbolRoles: 0}, // reference
		},
	}}}

	result := p.enrichFromIndex(g, index, "/tmp/repo")
	assert.Greater(t, result.SymbolsCovered, 0, "definitions must still be mapped")
	assert.Equal(t, 0, result.EdgesConfirmed, "reference pass must be skipped")
	assert.Equal(t, 0, result.EdgesAdded, "reference pass must be skipped")

	edges := g.GetOutEdges("M.cs::Bar")
	require.NotEmpty(t, edges)
	assert.Equal(t, 0.7, edges[0].Confidence, "INFERRED edge must stay unconfirmed")
}

func TestExtractSymbolName(t *testing.T) {
	tests := []struct {
		symbol string
		want   string
	}{
		{"scip-go gomod github.com/foo/bar v1.0.0 pkg.Foo().", "Foo"},
		{"scip-go gomod github.com/foo/bar v1.0.0 internal/graph/Graph.AddNode().", "AddNode"},
		{"scip-go gomod github.com/foo/bar v1.0.0 internal/graph/Node#Kind.", "Kind"},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractSymbolName(tt.symbol)
		assert.Equal(t, tt.want, got, "for symbol %q", tt.symbol)
	}
}
