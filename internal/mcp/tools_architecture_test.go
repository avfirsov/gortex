package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

func newArchitectureTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
	// A small architecture: handler.go calls service.go calls repo.go.
	// Plus a Python file in a different language for the language mix.
	g.AddNode(&graph.Node{ID: "p/handler.go::Handle", Name: "Handle", Kind: graph.KindFunction, FilePath: "p/handler.go", Language: "go", StartLine: 5})
	g.AddNode(&graph.Node{ID: "p/service.go::Process", Name: "Process", Kind: graph.KindFunction, FilePath: "p/service.go", Language: "go"})
	g.AddNode(&graph.Node{ID: "p/repo.go::Fetch", Name: "Fetch", Kind: graph.KindFunction, FilePath: "p/repo.go", Language: "go"})
	g.AddNode(&graph.Node{ID: "p/script.py::main", Name: "main", Kind: graph.KindFunction, FilePath: "p/script.py", Language: "python"})

	g.AddEdge(&graph.Edge{From: "p/handler.go::Handle", To: "p/service.go::Process", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "p/service.go::Process", To: "p/repo.go::Fetch", Kind: graph.EdgeCalls})

	return &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
}

func callArchitectureHandler(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleGetArchitecture(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError, "handler error: %+v", res.Content)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func TestArchitecture_SummaryReportsLanguageMix(t *testing.T) {
	s := newArchitectureTestServer(t)
	out := callArchitectureHandler(t, s, map[string]any{})

	summary := out["summary"].(map[string]any)
	assert.Equal(t, "go", summary["primary_language"], "go wins on node count")
	languages, _ := summary["languages"].([]any)
	require.NotEmpty(t, languages)

	// Find python entry.
	pythonSeen := false
	for _, l := range languages {
		m := l.(map[string]any)
		if m["name"] == "python" {
			pythonSeen = true
		}
	}
	assert.True(t, pythonSeen, "python should appear in the language mix")

	assert.EqualValues(t, 4, summary["total_nodes"].(float64))
	assert.EqualValues(t, 2, summary["total_edges"].(float64))
}

func TestArchitecture_EntryPointsAreUncalledFunctions(t *testing.T) {
	s := newArchitectureTestServer(t)
	out := callArchitectureHandler(t, s, map[string]any{})

	entries, _ := out["entry_points"].([]any)
	require.NotEmpty(t, entries, "Handle has zero in-edges and one out-edge — it must surface")
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.(map[string]any)["id"].(string)] = true
	}
	assert.True(t, ids["p/handler.go::Handle"], "Handle is the obvious entry point")
	assert.False(t, ids["p/service.go::Process"], "Process has a caller, not an entry point")
}

func TestArchitecture_CommunitiesIncludedWhenAnalysisCached(t *testing.T) {
	s := newArchitectureTestServer(t)
	s.analysisMu.Lock()
	s.communities = &analysis.CommunityResult{
		Communities: []analysis.Community{
			{
				ID:    "c-handler",
				Label: "handler-service-repo",
				Size:  3,
				Files: []string{"p/handler.go", "p/service.go", "p/repo.go"},
				Members: []string{
					"p/handler.go::Handle", "p/service.go::Process", "p/repo.go::Fetch",
				},
			},
		},
		Modularity: 0.5,
	}
	token := s.currentCommunityToken()
	s.communitiesToken = token
	s.adjacencyToken = token
	s.analysisEpoch = 1
	s.analysisMu.Unlock()

	out := callArchitectureHandler(t, s, map[string]any{})
	communities := out["communities"].(map[string]any)
	assert.EqualValues(t, 1, communities["count"].(float64))
	top := communities["top"].([]any)
	require.Len(t, top, 1)
	assert.Equal(t, "c-handler", top[0].(map[string]any)["id"])
}

func TestArchitecture_CrossRepoRollup(t *testing.T) {
	s := newArchitectureTestServer(t)
	// Add a second repo's node and a cross-repo call edge.
	s.graph.AddNode(&graph.Node{ID: "repoB/lib.go::Util", Name: "Util", Kind: graph.KindFunction, FilePath: "repoB/lib.go", Language: "go", RepoPrefix: "repoB"})
	for n := range []string{"p/handler.go::Handle", "p/service.go::Process", "p/repo.go::Fetch", "p/script.py::main"} {
		_ = n
	}
	// Mark our existing handler node with a RepoPrefix so the
	// cross-repo edge has named endpoints.
	hn := s.graph.GetNode("p/handler.go::Handle")
	hn.RepoPrefix = "repoA"
	s.graph.AddEdge(&graph.Edge{
		From: "p/handler.go::Handle",
		To:   "repoB/lib.go::Util",
		Kind: graph.EdgeCrossRepoCalls,
	})

	out := callArchitectureHandler(t, s, map[string]any{})
	cross, _ := out["cross_repo"].([]any)
	require.Len(t, cross, 1)
	row := cross[0].(map[string]any)
	assert.Equal(t, "cross_repo_calls", row["kind"])
	assert.Equal(t, "repoA", row["from_repo"])
	assert.Equal(t, "repoB", row["to_repo"])
	assert.EqualValues(t, 1, row["count"].(float64))
}

func TestArchitecture_ContractsRollup(t *testing.T) {
	s := newArchitectureTestServer(t)
	reg := contracts.NewRegistry()
	reg.Add(contracts.Contract{
		ID: "http:GET:/api/users", Type: contracts.ContractType("http"), Role: contracts.RoleProvider,
		SymbolID: "p/handler.go::Handle", FilePath: "p/handler.go", RepoPrefix: "repoA",
	})
	reg.Add(contracts.Contract{
		ID: "http:GET:/api/users", Type: contracts.ContractType("http"), Role: contracts.RoleConsumer,
		SymbolID: "p/client.go::Call", FilePath: "p/client.go", RepoPrefix: "repoB",
	})
	s.contractRegistry = reg

	out := callArchitectureHandler(t, s, map[string]any{})
	contractsSection := out["contracts"].(map[string]any)
	assert.EqualValues(t, 2, contractsSection["total"].(float64))
	byType := contractsSection["by_type"].(map[string]any)
	assert.EqualValues(t, 2, byType["http"].(float64))
	byRole := contractsSection["by_role"].(map[string]any)
	assert.EqualValues(t, 1, byRole["provider"].(float64))
	assert.EqualValues(t, 1, byRole["consumer"].(float64))
}

func TestArchitecture_PathPrefixFilter(t *testing.T) {
	s := newArchitectureTestServer(t)
	// Add an extra file outside p/.
	s.graph.AddNode(&graph.Node{ID: "vendor/x.go::X", Name: "X", Kind: graph.KindFunction, FilePath: "vendor/x.go", Language: "go"})
	s.graph.AddEdge(&graph.Edge{From: "vendor/x.go::X", To: "p/repo.go::Fetch", Kind: graph.EdgeCalls})

	out := callArchitectureHandler(t, s, map[string]any{"path_prefix": "p/"})
	summary := out["summary"].(map[string]any)
	// vendor/x.go is excluded; only the 4 original p/ nodes.
	assert.EqualValues(t, 4, summary["total_nodes"].(float64))
}

func TestArchitecture_EmptyGraphIsSafe(t *testing.T) {
	s := &Server{
		graph:      graph.New(),
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	out := callArchitectureHandler(t, s, map[string]any{})

	// Every section should be present and empty/zero.
	for _, key := range []string{"summary", "communities", "hotspots", "entry_points", "processes", "cross_repo", "contracts"} {
		_, ok := out[key]
		assert.True(t, ok, "section %q must be present even on empty graph", key)
	}
}

func TestArchitecture_TopCommunitiesCap(t *testing.T) {
	s := newArchitectureTestServer(t)
	comms := []analysis.Community{}
	for i := range 10 {
		comms = append(comms, analysis.Community{
			ID:      "c" + string(rune('A'+i)),
			Label:   "c" + string(rune('A'+i)),
			Size:    10 - i,
			Members: []string{"p/handler.go::Handle"},
			Files:   []string{"p/handler.go"},
		})
	}
	s.analysisMu.Lock()
	s.communities = &analysis.CommunityResult{Communities: comms}
	token := s.currentCommunityToken()
	s.communitiesToken = token
	s.adjacencyToken = token
	s.analysisEpoch = 1
	s.analysisMu.Unlock()

	out := callArchitectureHandler(t, s, map[string]any{"top_communities": 3})
	top := out["communities"].(map[string]any)["top"].([]any)
	assert.Len(t, top, 3, "cap honored")
}
