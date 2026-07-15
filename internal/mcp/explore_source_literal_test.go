package mcp

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/trigram"
)

func newExploreSourceLiteralServer(t testing.TB, nodes []*graph.Node) *Server {
	t.Helper()
	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	store.AddBatch(nodes, nil)
	return &Server{graph: store}
}

func sourceLiteralNode(id, name, path string, kind graph.NodeKind, start, end int) *graph.Node {
	return &graph.Node{
		ID: id, Name: name, QualName: id, Kind: kind,
		FilePath: path, RepoPrefix: "demo", Language: "csharp",
		StartLine: start, EndLine: end,
	}
}

func TestMapExploreSourceLiteralMatchesFindsCSharpConstructor(t *testing.T) {
	path := "demo/src/FormatterRegistry.cs"
	class := sourceLiteralNode("demo/FormatterRegistry.cs::FormatterRegistry#type", "FormatterRegistry", path, graph.KindType, 8, 42)
	constructor := sourceLiteralNode("demo/FormatterRegistry.cs::FormatterRegistry", "FormatterRegistry", path, graph.KindMethod, 20, 29)
	server := newExploreSourceLiteralServer(t, []*graph.Node{class, constructor})

	recall := server.mapExploreSourceLiteralMatches("ku", []trigram.Match{{
		Path: path, Line: 24, Text: `Register("ku", new CentralKurdishFormatter());`,
	}}, query.QueryOptions{RepoAllow: map[string]bool{"demo": true}})

	require.Equal(t, []exploreSourceLiteralHit{{nodeID: constructor.ID, rank: 0}}, recall.hits)
	require.False(t, recall.ambiguous)
}

func TestMapExploreSourceLiteralMatchesChoosesSmallestEnclosingSymbol(t *testing.T) {
	path := "demo/src/Registry.cs"
	typeNode := sourceLiteralNode("demo/Registry.cs::Registry#type", "Registry", path, graph.KindType, 1, 80)
	method := sourceLiteralNode("demo/Registry.cs::Registry.RegisterDefaults", "RegisterDefaults", path, graph.KindMethod, 16, 35)
	closure := sourceLiteralNode("demo/Registry.cs::Registry.RegisterDefaults#closure", "closure", path, graph.KindClosure, 22, 25)
	server := newExploreSourceLiteralServer(t, []*graph.Node{typeNode, method, closure})

	recall := server.mapExploreSourceLiteralMatches("ku", []trigram.Match{{
		Path: path, Line: 23, Text: `register("ku")`,
	}}, query.QueryOptions{RepoAllow: map[string]bool{"demo": true}})

	require.Equal(t, []exploreSourceLiteralHit{{nodeID: closure.ID, rank: 0}}, recall.hits)
}

func TestMapExploreSourceLiteralMatchesKeepsCommonLiteralNonTerminal(t *testing.T) {
	left := sourceLiteralNode("demo/left.cs::Register", "Register", "demo/left.cs", graph.KindMethod, 1, 10)
	right := sourceLiteralNode("demo/right.cs::Register", "Register", "demo/right.cs", graph.KindMethod, 1, 10)
	server := newExploreSourceLiteralServer(t, []*graph.Node{left, right})

	recall := server.mapExploreSourceLiteralMatches("shared", []trigram.Match{
		{Path: left.FilePath, Line: 4, Text: `Register("shared")`},
		{Path: right.FilePath, Line: 5, Text: `Register("shared")`},
	}, query.QueryOptions{RepoAllow: map[string]bool{"demo": true}})

	require.Len(t, recall.hits, 2)
	require.True(t, recall.ambiguous)
	targets := []exploreTarget{
		{node: left, exactContent: true, exactContentAmbiguous: true},
		{node: right, exactContent: true, exactContentAmbiguous: true},
	}
	require.False(t, exploreAnswerReady(`find registration for "shared"`, targets))
}

func TestMapExploreSourceLiteralMatchesHardCapsRecall(t *testing.T) {
	const total = exploreSourceLiteralRecallMaxHits + 7
	nodes := make([]*graph.Node, 0, total)
	matches := make([]trigram.Match, 0, total)
	for i := 0; i < total; i++ {
		path := fmt.Sprintf("demo/registry_%02d.cs", i)
		nodes = append(nodes, sourceLiteralNode(
			fmt.Sprintf("%s::Register", path), "Register", path, graph.KindMethod, 1, 5,
		))
		matches = append(matches, trigram.Match{Path: path, Line: 3, Text: `Register("needle")`})
	}
	server := newExploreSourceLiteralServer(t, nodes)

	recall := server.mapExploreSourceLiteralMatches(
		"needle", matches, query.QueryOptions{RepoAllow: map[string]bool{"demo": true}},
	)

	require.Len(t, recall.hits, exploreSourceLiteralRecallMaxHits)
	require.Equal(t, exploreSourceLiteralRecallMaxHits-1, recall.hits[len(recall.hits)-1].rank)
	require.True(t, recall.ambiguous)
}

func TestExploreHighestInformationQuotedLiteral(t *testing.T) {
	require.Equal(t, "registration-key", exploreHighestInformationQuotedLiteral([]string{"ku", "日本", "registration-key"}))
	require.Empty(t, exploreHighestInformationQuotedLiteral(nil))
}

func BenchmarkMapExploreSourceLiteralMatches(b *testing.B) {
	path := "demo/src/FormatterRegistry.cs"
	nodes := []*graph.Node{
		sourceLiteralNode("demo/FormatterRegistry.cs::FormatterRegistry#type", "FormatterRegistry", path, graph.KindType, 8, 42),
		sourceLiteralNode("demo/FormatterRegistry.cs::FormatterRegistry", "FormatterRegistry", path, graph.KindMethod, 20, 29),
	}
	server := newExploreSourceLiteralServer(b, nodes)
	matches := []trigram.Match{{Path: path, Line: 24, Text: `Register("ku")`}}
	scope := query.QueryOptions{RepoAllow: map[string]bool{"demo": true}}
	require.Len(b, server.mapExploreSourceLiteralMatches("ku", matches, scope).hits, 1)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		server.mapExploreSourceLiteralMatches("ku", matches, scope)
	}
}
