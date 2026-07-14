package resolver

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

var rustBuilderWalkerFixture = map[string]string{
	"src/lib.rs": `
mod ignore;
mod walker;

pub use ignore::GitIgnore;
pub use walker::{run_walk, Walker};
`,
	"src/ignore.rs": `
pub trait IgnoreState {
    fn is_ignored(&self, path: &str) -> bool;
}

pub struct GitIgnore {}

impl IgnoreState for GitIgnore {
    fn is_ignored(&self, path: &str) -> bool { path.starts_with('.') }
}
`,
	"src/walker.rs": `
use crate::ignore::{GitIgnore, IgnoreState};

pub trait WalkState: IgnoreState {}
impl WalkState for GitIgnore {}

pub struct Walker {}

impl Walker {
    pub fn walk<S: WalkState>(&self, state: &S, path: &str) -> bool {
        state.is_ignored(path)
    }
}

pub fn run_walk() -> bool {
    let walker = Walker {};
    let state = GitIgnore {};
    walker.walk(&state, ".git");
    state.is_ignored("target")
}
`,
}

var rustReexportAliasFixture = map[string]string{
	"src/lib.rs": `
mod literal;
mod matcher;

pub use literal::Literal as RegexLiteral;
pub use matcher::accepts;
`,
	"src/literal.rs": `
pub struct Literal { value: String }

impl Literal {
    pub fn new(value: &str) -> Self { Self { value: value.to_string() } }
    pub fn is_match(&self, text: &str) -> bool { text.contains(&self.value) }
}
`,
	"src/matcher.rs": `
use crate::RegexLiteral as Pattern;

pub fn accepts(text: &str) -> bool {
    let pattern = Pattern::new("todo");
    pattern.is_match(text)
}
`,
}

func TestRustBuilderWalkerCrossFileCallChain(t *testing.T) {
	g, nodes, edges := extractRustFixture(t, rustBuilderWalkerFixture)
	t.Logf("fixture extracted %d nodes and %d edges", nodes, edges)

	resolved := ResolveRustScopeCalls(g)
	t.Logf("rust scope resolved %d edges", resolved)

	walk := requireRustNode(t, g, "walk", "src/walker.rs", graph.KindMethod)
	traitMethod := requireRustTraitMethod(t, g, "is_ignored", "src/ignore.rs")
	implMethod := requireRustConcreteMethod(t, g, "is_ignored", "src/ignore.rs")
	run := requireRustNode(t, g, "run_walk", "src/walker.rs", graph.KindFunction)

	requireEdge(t, g, walk.ID, traitMethod.ID, graph.EdgeCalls,
		"generic S: WalkState dispatch must inherit IgnoreState::is_ignored across files")
	requireEdge(t, g, implMethod.ID, traitMethod.ID, graph.EdgeOverrides,
		"concrete GitIgnore implementation must connect to its trait declaration")
	requireEdge(t, g, run.ID, walk.ID, graph.EdgeCalls,
		"the public entrypoint must retain a causal call edge into Walker::walk")
}

func TestRustTypeAliasIndexStopsAtCanonicalImportPath(t *testing.T) {
	g, _, _ := extractRustFixture(t, rustBuilderWalkerFixture)
	walkState := requireRustNode(t, g, "WalkState", "src/walker.rs", graph.KindInterface)

	canonical, changed, ok := newRustTypeAliasIndex(g).resolve(walkState, "IgnoreState")
	require.True(t, ok)
	require.True(t, changed)
	require.Equal(t, "crate::ignore::IgnoreState", canonical)
}

func TestRustAssociatedCallsFollowReexportAliasChain(t *testing.T) {
	g, nodes, edges := extractRustFixture(t, rustReexportAliasFixture)
	t.Logf("fixture extracted %d nodes and %d edges", nodes, edges)

	ResolveRustScopeCalls(g)

	accepts := requireRustNode(t, g, "accepts", "src/matcher.rs", graph.KindFunction)
	newMethod := requireRustNode(t, g, "new", "src/literal.rs", graph.KindMethod)
	matchMethod := requireRustNode(t, g, "is_match", "src/literal.rs", graph.KindMethod)

	requireEdge(t, g, accepts.ID, newMethod.ID, graph.EdgeCalls,
		"Pattern::new must follow Pattern -> RegexLiteral -> Literal")
	requireEdge(t, g, accepts.ID, matchMethod.ID, graph.EdgeCalls,
		"the constructor-derived Pattern receiver must resolve through the same alias chain")
}

func TestParseRustUseBindingsNestedGroups(t *testing.T) {
	got := parseRustUseBindings("crate::ignore::{self, GitIgnore, nested::{IgnoreState as State, Other}}")
	require.ElementsMatch(t, []rustUseBinding{
		{source: "crate::ignore", local: "ignore"},
		{source: "crate::ignore::GitIgnore", local: "GitIgnore"},
		{source: "crate::ignore::nested::IgnoreState", local: "State"},
		{source: "crate::ignore::nested::Other", local: "Other"},
	}, got)
}

func BenchmarkRustBuilderWalkerExtraction(b *testing.B) {
	extractor := languages.NewRustExtractor()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for path, source := range rustBuilderWalkerFixture {
			if _, err := extractor.Extract(path, []byte(source)); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkRustBuilderWalkerGraphAndResolve(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g, _, _ := extractRustFixture(b, rustBuilderWalkerFixture)
		ResolveRustScopeCalls(g)
	}
}

func extractRustFixture(tb testing.TB, files map[string]string) (graph.Store, int, int) {
	tb.Helper()
	g := graph.New()
	extractor := languages.NewRustExtractor()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	nodeCount, edgeCount := 0, 0
	for _, path := range paths {
		result, err := extractor.Extract(path, []byte(files[path]))
		require.NoError(tb, err, "extract %s", path)
		addRustExtraction(g, result)
		nodeCount += len(result.Nodes)
		edgeCount += len(result.Edges)
	}
	return g, nodeCount, edgeCount
}

func addRustExtraction(g graph.Store, result *parser.ExtractionResult) {
	for _, node := range result.Nodes {
		g.AddNode(node)
	}
	for _, edge := range result.Edges {
		g.AddEdge(edge)
	}
}

func requireRustNode(tb testing.TB, g graph.Store, name, file string, kind graph.NodeKind) *graph.Node {
	tb.Helper()
	var matches []*graph.Node
	for _, node := range g.FindNodesByName(name) {
		if node != nil && node.FilePath == file && node.Kind == kind {
			matches = append(matches, node)
		}
	}
	require.Len(tb, matches, 1, "%s %s in %s", kind, name, file)
	return matches[0]
}

func requireRustTraitMethod(tb testing.TB, g graph.Store, name, file string) *graph.Node {
	tb.Helper()
	var matches []*graph.Node
	for _, node := range g.FindNodesByName(name) {
		if node == nil || node.FilePath != file || node.Kind != graph.KindMethod || node.Meta == nil {
			continue
		}
		if node.Meta["trait_decl"] == "true" {
			matches = append(matches, node)
		}
	}
	require.Len(tb, matches, 1, "trait method %s in %s", name, file)
	return matches[0]
}

func requireRustConcreteMethod(tb testing.TB, g graph.Store, name, file string) *graph.Node {
	tb.Helper()
	var matches []*graph.Node
	for _, node := range g.FindNodesByName(name) {
		if node == nil || node.FilePath != file || node.Kind != graph.KindMethod {
			continue
		}
		if node.Meta != nil && node.Meta["trait_decl"] == "true" {
			continue
		}
		matches = append(matches, node)
	}
	require.Len(tb, matches, 1, "concrete method %s in %s", name, file)
	return matches[0]
}

func requireEdge(tb testing.TB, g graph.Store, from, to string, kind graph.EdgeKind, message string) {
	tb.Helper()
	var actual []string
	for _, edge := range g.GetOutEdges(from) {
		if edge.Kind != kind {
			continue
		}
		actual = append(actual, edge.To)
		if edge.To == to {
			return
		}
	}
	sort.Strings(actual)
	require.Failf(tb, message, "from=%s kind=%s want=%s actual=%v", from, kind, to, actual)
}
