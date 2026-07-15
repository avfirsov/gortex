package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/rerank"
)

func TestExploreQuotedRecallTermsPreservesShortSignalAndDropsPatterns(t *testing.T) {
	task := "unsupported culture \"ku\" while locale \"日本\" is active; regex \"e.x|ex\" fails; inspect `tenant-code` initialization"
	got := exploreQuotedRecallTerms(task)
	want := []string{"ku", "日本", "tenant-code"}
	if len(got) != len(want) {
		t.Fatalf("quoted recall terms = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("quoted recall term %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRerankExploreConceptCoveragePromotesConjunctiveImplementation(t *testing.T) {
	generic := &rerank.Candidate{
		Node:     &graph.Node{ID: "generic", Name: "validate", Kind: graph.KindVariable, FilePath: "config.go"},
		TextRank: 0, VectorRank: -1, Score: 4,
	}
	implementation := &rerank.Candidate{
		Node: &graph.Node{
			ID: "implementation", Name: "dispatchRequest", QualName: "Parser.dispatchRequest",
			Kind: graph.KindMethod, FilePath: "parser/dispatch.go",
			Meta: map[string]any{"doc": "Parse and route each request through the configured dispatch pipeline."},
		},
		TextRank: 9, VectorRank: -1, Score: 2,
	}
	got := rerankExploreConceptCoverage("validate request parsing and routing in the dispatch pipeline", []*rerank.Candidate{generic, implementation})
	if got[0] != implementation {
		t.Fatalf("multi-term implementation must outrank one rare identifier: got %s", got[0].Node.ID)
	}
}

func TestRerankExploreConceptCoveragePromotesBoundedBodyLiteral(t *testing.T) {
	metadataHit := &rerank.Candidate{
		Node:     &graph.Node{ID: "api", Name: "convert", Kind: graph.KindMethod, FilePath: "api.go"},
		TextRank: 0, VectorRank: -1, Score: 4,
	}
	initializer := &rerank.Candidate{
		Node:     &graph.Node{ID: "initializer", Name: "registerFormats", Kind: graph.KindFunction, FilePath: "registry.go"},
		TextRank: 7, VectorRank: -1, Score: 1,
		Signals: map[string]float64{
			exploreContentRecallRankSignal: 1,
			exploreContentRecallTermSignal: 1,
		},
	}
	got := rerankExploreConceptCoverage("Why does conversion fail for an unsupported quoted locale during static registration?", []*rerank.Candidate{metadataHit, initializer})
	if got[0] != initializer {
		t.Fatalf("exact body-literal evidence must beat nearby API metadata: got %s", got[0].Node.ID)
	}
}

func TestExploreConceptDoesNotInferExactReadFromOverlap(t *testing.T) {
	api := &graph.Node{ID: "api", Name: "Convert", QualName: "Formatter.Convert", Kind: graph.KindMethod, FilePath: "formatter.go"}
	registry := &graph.Node{ID: "registry", Name: "registerLocale", Kind: graph.KindFunction, FilePath: "registry.go"}
	targets := []exploreTarget{{node: api}, {node: registry}}

	concept := "Failure while calling Convert with a specific locale; find the static locale registration"
	if got := exploreLocalizationExplicitTarget(concept, targets); got != "" {
		t.Fatalf("concept-only overlap prescribed exact read %q", got)
	}
	anchored := "Formatter.Convert() fails for a specific locale"
	if got := exploreLocalizationExplicitTarget(anchored, targets); got != api.ID {
		t.Fatalf("explicit call anchor exact target = %q, want %q", got, api.ID)
	}
}

func TestMergeExploreCandidatesRetainsContentEvidence(t *testing.T) {
	node := &graph.Node{ID: "same", Name: "register", Kind: graph.KindFunction, FilePath: "registry.go"}
	primary := &rerank.Candidate{Node: node, TextRank: 2, VectorRank: -1}
	content := &rerank.Candidate{
		Node: node, TextRank: 0, VectorRank: -1,
		Signals: map[string]float64{exploreContentRecallRankSignal: 1, exploreContentRecallTermSignal: 2},
	}
	merged := mergeExploreCandidates([]*rerank.Candidate{primary}, []*rerank.Candidate{content}, 0)
	if len(merged) != 1 || merged[0].Signals[exploreContentRecallRankSignal] != 1 || merged[0].Signals[exploreContentRecallTermSignal] != 2 {
		t.Fatalf("content annotations lost during dedup: %+v", merged)
	}
}

func TestExploreTextHasExactLiteralUsesUnicodeBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		literal string
		want    bool
	}{
		{name: "short exact", text: `case "ku": register()`, literal: "ku", want: true},
		{name: "prefix is not exact", text: `enable kubernetes registration`, literal: "ku", want: false},
		{name: "unicode exact", text: `supported["日本"] = formatter`, literal: "日本", want: true},
		{name: "unicode prefix is not exact", text: `supported["日本語"] = formatter`, literal: "日本", want: false},
		{name: "punctuated identifier", text: `tenant-code initialization`, literal: "tenant-code", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, exploreTextHasExactLiteral(test.text, test.literal))
		})
	}
}

func TestRerankExploreConceptCoveragePreservesVectorForPrefixOnlyContentHit(t *testing.T) {
	semantic := &rerank.Candidate{
		Node:     &graph.Node{ID: "semantic", Name: "localePipeline", Kind: graph.KindFunction, FilePath: "pipeline.go"},
		TextRank: 8, VectorRank: 0, Score: 5,
	}
	prefixOnly := &rerank.Candidate{
		Node:     &graph.Node{ID: "prefix", Name: "registerFormats", Kind: graph.KindFunction, FilePath: "registry.go"},
		TextRank: 0, VectorRank: -1, Score: 1,
		Signals: map[string]float64{
			exploreContentRecallRankSignal: 1,
			exploreContentRecallTermSignal: 1,
		},
	}
	got := rerankExploreConceptCoverage("find locale registration pipeline", []*rerank.Candidate{semantic, prefixOnly})
	require.Same(t, semantic, got[0])
}

func TestRerankExploreConceptCoveragePromotesVerifiedLiteralAcrossVector(t *testing.T) {
	semantic := &rerank.Candidate{
		Node:     &graph.Node{ID: "semantic", Name: "localePipeline", Kind: graph.KindFunction, FilePath: "pipeline.go"},
		TextRank: 0, VectorRank: 0, Score: 5,
	}
	exact := &rerank.Candidate{
		Node:     &graph.Node{ID: "exact", Name: "registerFormats", Kind: graph.KindFunction, FilePath: "registry.go"},
		TextRank: 7, VectorRank: -1, Score: 1,
		Signals: map[string]float64{
			exploreContentRecallRankSignal:  1,
			exploreContentRecallTermSignal:  1,
			exploreContentRecallExactSignal: 1,
		},
	}
	got := rerankExploreConceptCoverage("find locale registration pipeline", []*rerank.Candidate{semantic, exact})
	require.Same(t, exact, got[0])
}

func newExploreQualityServer(t *testing.T) *Server {
	t.Helper()
	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	store.AddBatch([]*graph.Node{
		{
			ID: "demo/api.go::Formatter.Convert", Name: "Convert", QualName: "Formatter.Convert",
			Kind: graph.KindMethod, FilePath: "demo/api.go", RepoPrefix: "demo", Language: "go",
			Meta: map[string]any{"doc": "Convert values through the locale pipeline."},
		},
		{
			ID: "demo/registry.go::registerFormats", Name: "registerFormats", QualName: "registry.registerFormats",
			Kind: graph.KindFunction, FilePath: "demo/registry.go", RepoPrefix: "demo", Language: "go",
			Meta: map[string]any{"doc": "Install supported formats during static registration."},
		},
		{
			ID: "demo/validate.go::validateRequest", Name: "validateRequest", QualName: "request.validateRequest",
			Kind: graph.KindFunction, FilePath: "demo/validate.go", RepoPrefix: "demo", Language: "go",
		},
		{
			ID: "demo/route.go::routeMessage", Name: "routeMessage", QualName: "message.routeMessage",
			Kind: graph.KindFunction, FilePath: "demo/route.go", RepoPrefix: "demo", Language: "go",
		},
	}, nil)
	require.NoError(t, store.AppendContent("demo", []graph.ContentFTSItem{{
		NodeID: "demo/registry.go::registerFormats", FilePath: "demo/registry.go", Ordinal: 0,
		Body: `func registerFormats() { supported["ku"] = formatter }`,
	}}))
	require.NoError(t, store.BuildContentIndex())
	return NewServer(query.NewEngine(store), store, nil, nil, zap.NewNop(), nil)
}

func callExploreQuality(t *testing.T, server *Server, task string, localize bool) (*mcpgo.CallToolResult, string) {
	t.Helper()
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"task": task, "localize": localize, "max_symbols": 5, "repo": "demo",
	}
	result, err := server.handleExplore(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	text := ""
	if len(result.Content) > 0 {
		if content, ok := result.Content[0].(mcpgo.TextContent); ok {
			text = content.Text
		}
	}
	return result, text
}

func TestHandleExploreRecallsExactQuotedLiteralFromSQLiteContentFTS(t *testing.T) {
	server := newExploreQualityServer(t)
	result, text := callExploreQuality(t, server, `why does conversion fail for unsupported locale "ku" during static registration`, false)
	require.False(t, result.IsError, text)
	require.Contains(t, text, "demo/registry.go::registerFormats")
	registry := strings.Index(text, "demo/registry.go::registerFormats")
	api := strings.Index(text, "demo/api.go::Formatter.Convert")
	require.True(t, api < 0 || registry < api, "exact body-literal candidate must lead ordinary metadata: %s", text)
}

func TestHandleExploreLocalizeKeepsEmptyResultNonTerminal(t *testing.T) {
	server := newExploreQualityServer(t)
	result, text := callExploreQuality(t, server, "find request validation and message routing behavior", true)
	require.False(t, result.IsError, text)
	require.Contains(t, text, `"state":""`)
	require.Contains(t, text, `"required_action":"continue"`)
	require.Contains(t, text, `"evidence":[]`)
	require.Nil(t, server.localization.block("search", "symbols", map[string]any{"query": "better anchor"}))
}

func TestHandleExploreLocalizeReturnsBoundedEvidenceWhenConfidenceIsLow(t *testing.T) {
	server := newExploreQualityServer(t)
	store, ok := server.graph.(*store_sqlite.Store)
	require.True(t, ok)
	require.NoError(t, store.AppendContent("demo", []graph.ContentFTSItem{{
		NodeID: "demo/route.go::routeMessage", FilePath: "demo/route.go", Ordinal: 0,
		Body: `func routeMessage() { supported["ku"] = route }`,
	}}))
	require.NoError(t, store.BuildContentIndex())

	result, text := callExploreQuality(t, server, `find "ku" behavior`, true)

	require.False(t, result.IsError, text)
	var envelope localizationExploreEnvelope
	require.NoError(t, json.Unmarshal([]byte(text), &envelope))
	require.Equal(t, localizationStateNeedsRefinement, envelope.Completion.State)
	require.Equal(t, localizationRefinementRequiredAction, envelope.Completion.RequiredAction)
	require.Equal(t, 1, envelope.Completion.AllowedToolCalls)
	require.NotEmpty(t, envelope.Evidence)
	require.NotEmpty(t, envelope.Symbols)
	for _, evidence := range envelope.Evidence {
		require.Empty(t, evidence.Source, "refinement evidence must not duplicate the authorized source read")
	}

	terminal := server.localizationFor(context.Background())
	terminal.mu.Lock()
	authorized := append([]string(nil), terminal.refinementSymbols...)
	terminal.mu.Unlock()
	require.Equal(t, envelope.Symbols, authorized, "only serialized evidence IDs may be refined")
	require.NotNil(t, terminal.block("search", "symbols", map[string]any{"query": "locale formatter"}))
	candidateRead := map[string]any{"target": map[string]any{"symbol": envelope.Symbols[0]}}
	blocked, reserved := terminal.authorize("read", "source", candidateRead)
	require.Nil(t, blocked)
	require.True(t, reserved)
	terminal.finishReservedRead(true)
	require.NotNil(t, terminal.block("search", "symbols", map[string]any{"query": "another search"}))
}

func TestHandleExploreLocalizeAcceptsVerifiedLiteralAndExplicitSymbol(t *testing.T) {
	t.Run("verified literal", func(t *testing.T) {
		server := newExploreQualityServer(t)
		result, text := callExploreQuality(t, server, `unsupported locale "ku" during static registration`, true)
		require.False(t, result.IsError, text)
		require.Contains(t, text, "demo/registry.go::registerFormats")
	})
	t.Run("explicit symbol", func(t *testing.T) {
		server := newExploreQualityServer(t)
		result, text := callExploreQuality(t, server, "demo/api.go::Formatter.Convert", true)
		require.False(t, result.IsError, text)
		require.Contains(t, text, "demo/api.go::Formatter.Convert")
	})
}

func BenchmarkRerankExploreConceptCoverage80(b *testing.B) {
	candidates := make([]*rerank.Candidate, 80)
	for i := range candidates {
		candidates[i] = &rerank.Candidate{
			Node: &graph.Node{
				ID: fmt.Sprintf("node-%d", i), Name: fmt.Sprintf("candidate%d", i),
				QualName: fmt.Sprintf("pipeline.candidate%d", i), Kind: graph.KindFunction,
				FilePath: fmt.Sprintf("internal/pipeline/file%d.go", i%8),
				Meta:     map[string]any{"doc": "Parse requests and dispatch matching candidates through the pipeline."},
			},
			TextRank: i, VectorRank: -1, Score: float64(80 - i),
		}
	}
	query := "find request parsing and candidate dispatch in the matching pipeline"
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		work := append([]*rerank.Candidate(nil), candidates...)
		rerankExploreConceptCoverage(query, work)
	}
}
