package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search"
	"github.com/zzet/gortex/internal/search/rerank"
	"github.com/zzet/gortex/internal/search/trigram"
)

type exploreSourceLiteralCountingStore struct {
	graph.Store
	allNodesCalls int
}

func (s *exploreSourceLiteralCountingStore) AllNodes() []*graph.Node {
	s.allNodesCalls++
	return s.Store.AllNodes()
}

type exploreSourceLiteralBlockingStore struct {
	graph.Store
	started chan struct{}
}

func (s *exploreSourceLiteralBlockingStore) GetFileNodesContext(ctx context.Context, _ string) []*graph.Node {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil
}

type exploreSourceLiteralOrderedStore struct {
	graph.Store
	nodesByPath map[string][]*graph.Node
	blockPath   string
	calls       []string
}

func (s *exploreSourceLiteralOrderedStore) GetFileNodesContext(ctx context.Context, path string) []*graph.Node {
	s.calls = append(s.calls, path)
	if path == s.blockPath {
		<-ctx.Done()
		return nil
	}
	return s.nodesByPath[path]
}

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

func TestMapExploreSourceLiteralMatchesFallsBackToSingleRepoUnprefixedPath(t *testing.T) {
	graphPath := "src/Humanizer/Configuration/FormatterRegistry.cs"
	matchPath := "humanizer-1059/" + graphPath
	class := sourceLiteralNode("src/FormatterRegistry.cs::FormatterRegistry#type", "FormatterRegistry", graphPath, graph.KindType, 5, 66)
	constructor := sourceLiteralNode("src/FormatterRegistry.cs::FormatterRegistry", "FormatterRegistry", graphPath, graph.KindMethod, 7, 55)
	class.RepoPrefix = ""
	constructor.RepoPrefix = ""
	server := newExploreSourceLiteralServer(t, []*graph.Node{class, constructor})

	recall := server.mapExploreSourceLiteralMatches("ku", []trigram.Match{{
		Path: matchPath, Line: 24, Text: `RegisterDefaultFormatter("ku");`,
	}}, query.QueryOptions{RepoAllow: map[string]bool{"humanizer-1059": true}})

	require.Equal(t, []exploreSourceLiteralHit{{nodeID: constructor.ID, rank: 0}}, recall.hits)
	require.False(t, recall.ambiguous)
}

func TestMapExploreSourceLiteralMatchesPrefersExactPathOverAlias(t *testing.T) {
	graphPath := "src/Humanizer/Configuration/FormatterRegistry.cs"
	matchPath := "humanizer-1059/" + graphPath
	alias := sourceLiteralNode("alias::FormatterRegistry", "AliasFormatterRegistry", graphPath, graph.KindMethod, 7, 55)
	alias.RepoPrefix = ""
	exact := sourceLiteralNode("exact::FormatterRegistry", "ExactFormatterRegistry", matchPath, graph.KindMethod, 7, 55)
	exact.RepoPrefix = "humanizer-1059"
	server := newExploreSourceLiteralServer(t, []*graph.Node{alias, exact})

	recall := server.mapExploreSourceLiteralMatches("ku", []trigram.Match{{
		Path: matchPath, Line: 24, Text: `RegisterDefaultFormatter("ku");`,
	}}, query.QueryOptions{RepoAllow: map[string]bool{"humanizer-1059": true}})

	require.Equal(t, []exploreSourceLiteralHit{{nodeID: exact.ID, rank: 0}}, recall.hits)
}

func TestMapExploreSourceLiteralMatchesQueriesExactPathsBeforeAliases(t *testing.T) {
	exactA := sourceLiteralNode("demo/src/a.cs::Register", "RegisterA", "demo/src/a.cs", graph.KindMethod, 1, 5)
	exactB := sourceLiteralNode("demo/src/b.cs::Register", "RegisterB", "demo/src/b.cs", graph.KindMethod, 1, 5)
	store := &exploreSourceLiteralOrderedStore{
		nodesByPath: map[string][]*graph.Node{
			exactA.FilePath: {exactA},
			exactB.FilePath: {exactB},
		},
		blockPath: "src/a.cs",
	}
	server := &Server{graph: store}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	recall := server.mapExploreSourceLiteralMatchesContext(ctx, "ku", []trigram.Match{
		{Path: exactB.FilePath, Line: 3, Text: `Register("ku")`},
		{Path: exactA.FilePath, Line: 3, Text: `Register("ku")`},
	}, query.QueryOptions{RepoAllow: map[string]bool{"demo": true}})

	require.Equal(t, []string{"demo/src/a.cs", "demo/src/b.cs", "src/a.cs"}, store.calls)
	require.Equal(t, []exploreSourceLiteralHit{
		{nodeID: exactB.ID, rank: 0},
		{nodeID: exactA.ID, rank: 1},
	}, recall.hits)
	require.True(t, recall.ambiguous)
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

func TestMapDiscoveredExploreSourceLiteralMatchesPreservesHitsAfterDiscoveryDeadline(t *testing.T) {
	path := "demo/src/FormatterRegistry.cs"
	constructor := sourceLiteralNode(
		"demo/src/FormatterRegistry.cs::FormatterRegistry", "FormatterRegistry.<init>",
		path, graph.KindMethod, 2, 4,
	)
	server := newExploreSourceLiteralServer(t, []*graph.Node{constructor})
	search := exploreSourceLiteralSearch{
		matches: []trigram.Match{{
			Path: path, Line: 3, Text: `RegisterDefaultFormatter("ku");`,
		}},
		incomplete: true,
	}

	recall, mappingErr := server.mapDiscoveredExploreSourceLiteralMatches(
		context.Background(), "ku", search,
		query.QueryOptions{RepoAllow: map[string]bool{"demo": true}},
		context.DeadlineExceeded,
	)

	require.NoError(t, mappingErr)
	require.Equal(t, []exploreSourceLiteralHit{{nodeID: constructor.ID, rank: 0}}, recall.hits)
	require.True(t, recall.ambiguous, "deadline-truncated discovery must remain non-terminal")
}

func TestExploreHighestInformationQuotedLiteral(t *testing.T) {
	require.Equal(t, "registration-key", exploreHighestInformationQuotedLiteral([]string{"ku", "日本", "registration-key"}))
	require.Empty(t, exploreHighestInformationQuotedLiteral(nil))
}

func TestGatherExploreQuotedContentCandidatesMergesSourceLiteralWithExactContentNode(t *testing.T) {
	root := t.TempDir()
	rel := "src/FormatterRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("public sealed class FormatterRegistry {\n    public FormatterRegistry() {\n        Register(\"ku\", new CentralKurdishFormatter());\n    }\n}\n"), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	document := &graph.Node{
		ID: "demo/docs/locales.md::content", Name: "locales", QualName: "docs.locales",
		Kind: graph.KindFile, FilePath: "docs/locales.md", RepoPrefix: "demo",
	}
	typeNode := sourceLiteralNode("demo/src/FormatterRegistry.cs::FormatterRegistry#type", "FormatterRegistry", rel, graph.KindType, 1, 5)
	constructor := sourceLiteralNode("demo/src/FormatterRegistry.cs::FormatterRegistry", "FormatterRegistry", rel, graph.KindMethod, 2, 4)
	store.AddBatch([]*graph.Node{document, typeNode, constructor}, nil)
	counting := &quotedRecallCountingStore{
		Store: store,
		hits: map[string][]graph.ContentHit{
			"ku": {{NodeID: document.ID, FilePath: document.FilePath, Snippet: `locale "ku" reference`}},
		},
	}
	idx := indexer.New(counting, parser.NewRegistry(), config.IndexConfig{}, zap.NewNop())
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)
	idx.SetFileMtimes(map[string]int64{rel: 1})
	store.AddBatch([]*graph.Node{document, typeNode, constructor}, nil)
	server := &Server{graph: counting, indexer: idx}

	candidates := server.gatherExploreQuotedContentCandidates(
		context.Background(), `find registration for "ku"`, nil, 20,
		query.QueryOptions{RepoAllow: map[string]bool{"demo": true}},
	)

	require.NotNil(t, candidateByID(candidates, document.ID), "exact content evidence must be retained")
	require.NotNil(t, candidateByID(candidates, constructor.ID), "an exact non-source content hit must not suppress bounded source recall")
}

func TestGatherExploreQuotedContentCandidatesSkipsSourceScanForExactOrdinaryCandidate(t *testing.T) {
	root := t.TempDir()
	rel := "src/RawRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("public sealed class RawRegistry {\n    public void Configure() { Register(\"KnownFormatter\"); }\n}\n"), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	idx := indexer.New(store, parser.NewRegistry(), config.IndexConfig{}, zap.NewNop())
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)
	idx.SetFileMtimes(map[string]int64{rel: 1})

	exact := sourceLiteralNode("demo/src/KnownFormatter.cs::KnownFormatter", "KnownFormatter", "src/KnownFormatter.cs", graph.KindType, 1, 2)
	raw := sourceLiteralNode("demo/src/RawRegistry.cs::RawRegistry.Configure", "RawRegistry.Configure", rel, graph.KindMethod, 1, 3)
	store.AddBatch([]*graph.Node{exact, raw}, nil)
	core, logs := observer.New(zap.DebugLevel)
	server := &Server{graph: store, indexer: idx, logger: zap.New(core)}

	candidates := server.gatherExploreQuotedContentCandidates(
		context.Background(), `find symbol "KnownFormatter"`,
		[]*rerank.Candidate{{Node: exact, TextRank: 0, VectorRank: -1}}, 20,
		query.QueryOptions{RepoAllow: map[string]bool{"demo": true}},
	)

	require.Nil(t, candidateByID(candidates, raw.ID), "a distinct raw-source match must not be admitted after an exact ordinary hit")
	require.Zero(t,
		logs.FilterMessage("mcp: explore source literal recall").Len()+
			logs.FilterMessage("mcp: explore source literal recall incomplete").Len(),
		"the miss-only gate must avoid opening source files when ordinary retrieval is already exact",
	)
}

func TestExplorePreferredRefinementSymbolPrefersSourceLiteral(t *testing.T) {
	ordinary := &graph.Node{ID: "repo/ordinary.go::ordinary"}
	source := &graph.Node{ID: "repo/source.go::source"}
	targets := []exploreTarget{{node: ordinary}, {node: source, sourceLiteral: true}}

	require.Equal(t, source.ID, explorePreferredRefinementSymbol(targets))
	require.Equal(t, ordinary.ID, explorePreferredRefinementSymbol(targets[:1]))
}

func TestGatherExploreSourceLiteralRecallMapsParsedCSharpConstructor(t *testing.T) {
	root := t.TempDir()
	rel := "src/Humanizer/Configuration/FormatterRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("public sealed class FormatterRegistry {\n    public FormatterRegistry() {\n        RegisterDefaultFormatter(\"ku\");\n    }\n}\n"), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	registry := parser.NewRegistry()
	languages.RegisterAll(registry)
	idx := indexer.New(store, registry, config.IndexConfig{}, zap.NewNop())
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)
	counting := &exploreSourceLiteralCountingStore{Store: store}
	server := &Server{graph: counting, indexer: idx, logger: zap.NewNop()}

	recall := server.gatherExploreSourceLiteralRecall(
		context.Background(), []string{"ku"}, "", query.QueryOptions{},
	)

	found := false
	for _, hit := range recall.hits {
		node := store.GetNode(hit.nodeID)
		if node != nil && node.FilePath == rel && node.Name == "FormatterRegistry.<init>" {
			found = true
		}
	}
	require.True(t, found, "literal line must map to the parsed enclosing constructor")

	task := `CultureNotFoundException for "ku" culture - code adds/uses "ku" (Kurdish) CultureInfo which is not supported on Xamarin.Android, causing crash. Find where "ku" culture is registered/used in fallback resolution logic.`
	require.NotEqual(t, rerank.QueryClassConcept, rerank.ClassifyQuery(shapeExploreQuery(task)), "regression requires the non-concept retrieval branch")
	engine := query.NewEngine(counting)
	engine.SetSearchProvider(idx.Search)
	fullServer := NewServer(engine, counting, idx, nil, zap.NewNop(), nil)
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"task": task, "localize": true, "max_symbols": 10,
	}
	result, err := fullServer.handleExplore(context.Background(), req)
	require.NoError(t, err)
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(mcpgo.TextContent)
	require.True(t, ok)
	var envelope localizationExploreEnvelope
	require.NoError(t, json.Unmarshal([]byte(text.Text), &envelope))
	require.NotEmpty(t, envelope.Evidence)
	require.Equal(t, "FormatterRegistry.<init>", envelope.Evidence[0].Name, "source evidence must lead the final localization envelope")
	if envelope.Completion.State == localizationStateNeedsRefinement {
		require.Contains(t, envelope.Completion.RequiredAction, envelope.Evidence[0].ID)
	}
	require.Zero(t, counting.allNodesCalls, "literal mapping must stay bounded to matched files")
}

func TestGatherExploreSourceLiteralRecallBoundsMappingByRequestDeadline(t *testing.T) {
	root := t.TempDir()
	rel := "src/Registry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("Register(\"needle\")\n"), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	idx := indexer.New(store, parser.NewRegistry(), config.IndexConfig{}, zap.NewNop())
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)
	idx.SetFileMtimes(map[string]int64{rel: 1})
	started := make(chan struct{}, 1)
	server := &Server{
		graph:   &exploreSourceLiteralBlockingStore{Store: store, started: started},
		indexer: idx,
		logger:  zap.NewNop(),
	}

	began := time.Now()
	recall := server.gatherExploreSourceLiteralRecall(
		context.Background(), []string{"needle"}, "", query.QueryOptions{},
	)
	elapsed := time.Since(began)

	select {
	case <-started:
	default:
		t.Fatal("mapping did not use the context-aware file-node reader")
	}
	require.Less(t, elapsed, 500*time.Millisecond, "mapping must not outlive the bounded recall budget")
	require.Empty(t, recall.hits)
	require.True(t, recall.ambiguous, "deadline-truncated mapping must remain non-terminal")
}

func TestSearchExploreSourceLiteralFallsBackWhenMultiIndexerDoesNotOwnRepo(t *testing.T) {
	root := t.TempDir()
	rel := "src/FormatterRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("Register(\"ku\")\n"), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	idx := indexer.New(store, parser.NewRegistry(), config.IndexConfig{}, zap.NewNop())
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)
	idx.SetFileMtimes(map[string]int64{rel: 1})
	multi := indexer.NewMultiIndexer(store, parser.NewRegistry(), nil, nil, zap.NewNop())
	server := &Server{graph: store, indexer: idx, multiIndexer: multi}

	search := server.searchExploreSourceLiteral(
		context.Background(), "ku", "", query.QueryOptions{},
	)

	require.Len(t, search.matches, 1)
	require.Equal(t, rel, search.matches[0].Path)
	require.False(t, search.incomplete)
	require.Equal(t, "direct", search.backend)
	require.True(t, search.owned)
}

func TestSearchExploreSourceLiteralDoesNotCrossConfiguredRepository(t *testing.T) {
	directRoot := t.TempDir()
	rel := "src/FormatterRegistry.cs"
	path := filepath.Join(directRoot, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("Register(\"ku\")\n"), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	registry := parser.NewRegistry()
	direct := indexer.New(store, registry, config.IndexConfig{}, zap.NewNop())
	_, err = direct.IndexCtx(context.Background(), directRoot)
	require.NoError(t, err)
	direct.SetFileMtimes(map[string]int64{rel: 1})

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	manager, err := config.NewConfigManager(configPath)
	require.NoError(t, err)
	multi := indexer.NewMultiIndexer(store, registry, search.NewAuto(), manager, zap.NewNop())
	otherRoot := filepath.Join(t.TempDir(), "repo-a")
	require.NoError(t, os.MkdirAll(otherRoot, 0o755))
	_, err = multi.TrackRepoCtx(context.Background(), config.RepoEntry{Path: otherRoot, Force: true})
	require.NoError(t, err)
	server := &Server{graph: store, indexer: direct, multiIndexer: multi}

	search := server.searchExploreSourceLiteral(
		context.Background(), "ku", "repo-b", query.QueryOptions{},
	)

	require.Empty(t, search.matches, "unresolved multi-repo scope must not scan the direct backend")
	require.False(t, search.incomplete)
	require.Equal(t, "multi-unresolved", search.backend)
	require.False(t, search.owned)
}

func TestGatherExploreSourceLiteralRecallDoesNotLogRawLiteral(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	server := &Server{logger: zap.New(core)}
	const secret = "customer-secret-日本"

	server.gatherExploreSourceLiteralRecall(
		context.Background(), []string{secret}, "demo", query.QueryOptions{},
	)

	entries := logs.FilterMessage("mcp: explore source literal recall incomplete").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.NotContains(t, fields, "term")
	require.EqualValues(t, 18, fields["term_runes"])
	require.NotContains(t, entries[0].Message, secret)
	require.NotContains(t, fmt.Sprint(fields), secret)
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
