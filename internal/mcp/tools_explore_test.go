package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search"
	"github.com/zzet/gortex/internal/search/rerank"
)

func TestExploreLocalizableKind(t *testing.T) {
	// Real edit targets are localizable; structural noise is not.
	localizable := []graph.NodeKind{
		graph.KindFunction, graph.KindMethod, graph.KindType,
		graph.KindInterface, graph.KindField, graph.KindConstant,
		graph.KindVariable,
	}
	for _, k := range localizable {
		if !exploreLocalizableKind(k) {
			t.Errorf("kind %q should be localizable", k)
		}
	}
	noise := []graph.NodeKind{
		graph.KindParam, graph.KindLocal, graph.KindClosure,
		graph.KindGenericParam, graph.KindImport, graph.KindFile,
	}
	for _, k := range noise {
		if exploreLocalizableKind(k) {
			t.Errorf("kind %q should be filtered out", k)
		}
	}
}

func exploreTestTargets() []exploreTarget {
	fn := &graph.Node{ID: "retry.go::DoWithRetry", Name: "DoWithRetry", Kind: graph.KindFunction,
		FilePath: "retry.go", StartLine: 11, EndLine: 20, Language: "go"}
	helper := &graph.Node{ID: "retry.go::Backoff", Name: "Backoff", Kind: graph.KindFunction,
		FilePath: "retry.go", StartLine: 6, EndLine: 8, Language: "go"}
	caller := &graph.Node{ID: "client.go::Fetch", Name: "Fetch", Kind: graph.KindFunction,
		FilePath: "client.go", StartLine: 4, EndLine: 6, Language: "go"}
	return []exploreTarget{
		{node: fn, score: 0.9, callers: []*graph.Node{caller}, callees: []*graph.Node{helper},
			source: "func DoWithRetry(max int) error {\n\treturn nil\n}"},
		{node: helper, score: 0.5, callers: []*graph.Node{fn},
			source: "func Backoff(n int) int {\n\treturn n\n}"},
	}
}

func TestRenderExploreShape(t *testing.T) {
	out := (&Server{}).renderExplore("the retry backoff never fires on 429", exploreTestTargets(), 9000)

	// Ranked targets, with citeable path:line locations and an answer draft
	// before the detailed payload.
	for _, want := range []string{
		"EXPLORE — the retry backoff never fires on 429",
		"LOCALIZATION COMPLETE:",
		"A files/symbols/evidence/where request is localization-only even when it describes a bug: answer now",
		"For a requested implementation change, proceed directly to impact, edit, and test",
		"Do not make another localization, search, or read call",
		"## Answer draft",
		"FILE: retry.go:11-20  ·  SYMBOL: DoWithRetry  ·  EVIDENCE: ranked #1",
		"## Likely targets",
		"1. DoWithRetry  function  ·  retry.go:11-20",
		"2. Backoff  function  ·  retry.go:6-8",
		"^ callers: Fetch (client.go:4-6)",
		"v calls:   Backoff (retry.go:6-8)",
		"func DoWithRetry(max int) error", // full body for hot target
		"## Candidate files",
		"- retry.go  ·  Backoff, DoWithRetry",
		"— Coverage: 2 ranked candidate symbol(s) across 1 file(s)",
		"END OF LOCALIZATION — answer from this evidence or proceed with the requested change",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- got ---\n%s", want, out)
		}
	}
	if strings.Index(out, "## Answer draft") >= strings.Index(out, "## Likely targets") {
		t.Fatalf("answer draft must precede detailed targets:\n%s", out)
	}
	for _, forbidden := range []string{"REFINEMENT NEEDED", "make at most one", "rerun explore", "read(operation:", "Do not call another tool"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("answer-ready output must not invite another call via %q:\n%s", forbidden, out)
		}
	}
}

func TestRenderExploreBroadWeakNeighborhoodReturnsBestSupportedDraft(t *testing.T) {
	targets := []exploreTarget{
		{node: &graph.Node{ID: "internal/embedding/onnx.go::current", Name: "current", Kind: graph.KindVariable, FilePath: "internal/embedding/onnx.go"}, score: 0.9},
		{node: &graph.Node{ID: "internal/analyzer/state.go::dirty", Name: "dirty", Kind: graph.KindVariable, FilePath: "internal/analyzer/state.go"}, score: 0.8},
	}
	task := "review release blockers across impact reach sqlite races mcp facade routing codex claude guidance explore ranking token cost"
	out := (&Server{}).renderExplore(task, targets, 1600)
	for _, want := range []string{"BEST-SUPPORTED LOCALIZATION:", "with stated uncertainty", "## Answer draft", "at most one focused exact-ID read or exact literal/path/symbol search", "then stop localization", "Do not fan out or rerun broad exploration"} {
		if !strings.Contains(out, want) {
			t.Fatalf("weak neighborhood missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"REFINEMENT NEEDED", "make one focused refinement", "rerun explore for", "search(operation:"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("weak neighborhood must not invite broad refinement via %q:\n%s", forbidden, out)
		}
	}
	if strings.Contains(out, "LOCALIZATION COMPLETE:") {
		t.Fatalf("weak neighborhood must not overstate confidence:\n%s", out)
	}
}

func TestRenderExploreAnswerDraftIncludesQueryAlignedGraphNeighbor(t *testing.T) {
	head := &graph.Node{ID: "handler.go::HandleRequest", Name: "HandleRequest", Kind: graph.KindFunction, FilePath: "handler.go", StartLine: 10, EndLine: 18}
	retry := &graph.Node{ID: "retry/coordinator.go::RetryCoordinator", Name: "RetryCoordinator", Kind: graph.KindFunction, FilePath: "retry/coordinator.go", StartLine: 20, EndLine: 30}
	logger := &graph.Node{ID: "log.go::Logger", Name: "Logger", Kind: graph.KindFunction, FilePath: "log.go", StartLine: 5, EndLine: 8}
	out := (&Server{}).renderExplore(
		"locate retry coordinator implementation for transient failures",
		[]exploreTarget{{node: head, score: 1, callers: []*graph.Node{logger, retry}}},
		1600,
	)
	draftStart := strings.Index(out, "## Answer draft")
	detailsStart := strings.Index(out, "## Likely targets")
	if draftStart < 0 || detailsStart < 0 || draftStart >= detailsStart {
		t.Fatalf("invalid draft/detail ordering:\n%s", out)
	}
	draft := out[draftStart:detailsStart]
	for _, want := range []string{"SYMBOL: RetryCoordinator", "ID: retry/coordinator.go::RetryCoordinator", "retry/coordinator.go:20-30", "caller of ranked #1"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("query-aligned neighbor missing %q from answer draft:\n%s", want, draft)
		}
	}
	if strings.Contains(draft, "log.go::Logger") {
		t.Fatalf("unrelated graph neighbor must not be promoted into answer draft:\n%s", draft)
	}
}

func TestRenderExploreAnswerDraftPromotesBoundedStructuralNeighbors(t *testing.T) {
	node := func(id, name, file string) *graph.Node {
		return &graph.Node{ID: id, Name: name, Kind: graph.KindMethod, FilePath: file, StartLine: 20}
	}
	genericHidden := node("crates/ignore/src/walk.rs::WalkBuilder.hidden", "hidden", "crates/ignore/src/walk.rs")
	standardFilters := node("crates/ignore/src/walk.rs::WalkBuilder.standard_filters", "standard_filters", "crates/ignore/src/walk.rs")
	isHidden := node("crates/ignore/src/walk.rs::Ignore.is_hidden", "is_hidden", "crates/ignore/src/walk.rs")
	matched := node("crates/ignore/src/dir.rs::Ignore.matched_dir_entry", "matched_dir_entry", "crates/ignore/src/dir.rs")
	testCaller := &graph.Node{ID: "crates/ignore/tests/gitignore_tests.rs::hidden_test", Name: "hidden_test", Kind: graph.KindFunction, FilePath: "crates/ignore/tests/gitignore_tests.rs"}
	callers := []*graph.Node{testCaller, node("crates/core/src/log.rs::trace_event", "trace_event", "crates/core/src/log.rs"), matched}
	targets := []exploreTarget{
		{node: genericHidden, callees: []*graph.Node{standardFilters}},
		{node: node("rank2", "ignore_rules", "crates/ignore/src/dir.rs")},
		{node: node("rank3", "walk_entries", "crates/ignore/src/walk.rs")},
		{node: node("rank4", "ignore", "crates/ignore/src/dir.rs")},
		{node: node("rank5", "hidden", "crates/ignore/src/dir.rs")},
		{node: node("rank6", "filter_entry", "crates/ignore/src/walk.rs")},
		{node: isHidden, callers: callers},
		{node: node("rank8", "walk_state", "crates/ignore/src/walk.rs")},
		{node: node("rank9", "path_filter", "crates/ignore/src/dir.rs")},
		{node: node("rank10", "ignore_match", "crates/ignore/src/dir.rs")},
	}
	out := (&Server{}).renderExplore("locate is_hidden scoped ignore behavior", targets, 1600)
	draft := out[strings.Index(out, "## Answer draft"):strings.Index(out, "## Likely targets")]
	for _, want := range []string{"SYMBOL: matched_dir_entry", "ID: crates/ignore/src/dir.rs::Ignore.matched_dir_entry", "caller of ranked #7"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("rank-7 structural caller missing %q from answer draft:\n%s", want, draft)
		}
	}
	for _, forbidden := range []string{"hidden_test", "trace_event", "standard_filters"} {
		if strings.Contains(draft, forbidden) {
			t.Fatalf("generic or ineligible caller %q was promoted:\n%s", forbidden, draft)
		}
	}

	buildParallel := node("crates/ignore/src/walk.rs::WalkBuilder.build_parallel", "build_parallel", "crates/ignore/src/walk.rs")
	buildWithCWD := node("crates/ignore/src/dir.rs::IgnoreBuilder.build_with_cwd", "build_with_cwd", "crates/ignore/src/dir.rs")
	getCurrentDir := node("crates/ignore/src/walk.rs::get_or_set_current_dir", "get_or_set_current_dir", "crates/ignore/src/walk.rs")
	build := node("crates/ignore/src/walk.rs::WalkBuilder.build", "build", "crates/ignore/src/walk.rs")
	targets = []exploreTarget{
		{node: node("parallel-rank1", "parallel_walk", "crates/ignore/src/walk.rs")},
		{node: buildParallel, callees: []*graph.Node{build, getCurrentDir, buildWithCWD}},
		{node: node("parallel-rank3", "multi_root", "crates/ignore/src/walk.rs")},
		{node: node("parallel-rank4", "current_dir", "crates/ignore/src/walk.rs")},
		{node: node("parallel-rank5", "standard_filters", "crates/ignore/src/walk.rs")},
		{node: node("parallel-rank6", "add_root", "crates/ignore/src/walk.rs")},
		{node: node("parallel-rank7", "ignore_rule", "crates/ignore/src/dir.rs")},
		{node: node("parallel-rank8", "walk_state", "crates/ignore/src/walk.rs")},
		{node: node("parallel-rank9", "root_path", "crates/ignore/src/dir.rs")},
		{node: node("parallel-rank10", "ignore_match", "crates/ignore/src/dir.rs")},
	}
	out = (&Server{}).renderExplore("nondeterminism WalkBuilder parallel multi-root walk current_dir standard_filters add build_parallel", targets, 1600)
	draft = out[strings.Index(out, "## Answer draft"):strings.Index(out, "## Likely targets")]
	for _, want := range []string{"SYMBOL: build_with_cwd", "ID: crates/ignore/src/dir.rs::IgnoreBuilder.build_with_cwd", "callee of ranked #2"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("cross-file structural callee missing %q from answer draft:\n%s", want, draft)
		}
	}
	if strings.Contains(draft, "get_or_set_current_dir") {
		t.Fatalf("query-overlapping but parent-unrelated callee displaced structural implementation:\n%s", draft)
	}
}

func TestExploreAnswerDraftCapsAndDeduplicates(t *testing.T) {
	node := func(id, name string, line int) *graph.Node {
		return &graph.Node{ID: id, Name: name, Kind: graph.KindFunction, FilePath: id + ".go", StartLine: line}
	}
	deep := node("deep", "DeepTarget", 40)
	targets := []exploreTarget{
		{node: node("alpha", "Alpha", 10), callers: []*graph.Node{deep}},
		{node: node("beta", "Beta", 20)},
		{node: node("gamma", "Gamma", 30)},
		{node: deep},
		{node: node("epsilon", "Epsilon", 50)},
		{node: node("zeta", "Zeta", 60)},
	}
	entries := exploreAnswerDraft("locate DeepTarget implementation", targets)
	if len(entries) != exploreDraftTotalLimit {
		t.Fatalf("draft size = %d, want %d: %#v", len(entries), exploreDraftTotalLimit, entries)
	}
	if entries[0].node.ID != "deep" {
		t.Fatalf("exact query-aligned target must lead the draft, got %q", entries[0].node.ID)
	}
	seen := map[string]int{}
	for _, entry := range entries {
		seen[exploreDraftNodeKey(entry.node)]++
	}
	if seen["deep"] != 1 {
		t.Fatalf("aligned node must be promoted exactly once, got %d: %#v", seen["deep"], entries)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("draft node %q appears %d times", id, count)
		}
	}
}

func TestExploreDraftExactAnchorUsesTokenBoundaries(t *testing.T) {
	n := &graph.Node{Name: "Get", QualName: "Client.Get", FilePath: "runtime/get.go"}
	if exploreDraftExactAnchor("target offset runtime user backend", n) {
		t.Fatal("short identifier must not match inside unrelated words")
	}
	if !exploreDraftExactAnchor("trace Client.Get behavior", n) {
		t.Fatal("qualified identifier token sequence should be an exact anchor")
	}
	if !exploreDraftExactAnchor("inspect get.go", n) {
		t.Fatal("path basename should be an exact anchor")
	}
	generic := &graph.Node{Name: "write", QualName: "Writer.write", FilePath: "runtime/output.go"}
	if exploreDraftExactAnchor("fix write behavior", generic) {
		t.Fatal("unqualified generic method must not become an exact anchor")
	}
	if !exploreDraftExactAnchor("fix Writer.write behavior", generic) {
		t.Fatal("qualified generic method should remain an exact anchor")
	}
	plainGeneric := &graph.Node{Name: "write", QualName: "write", FilePath: "runtime/output.go"}
	if exploreDraftExactAnchor("fix write behavior", plainGeneric) {
		t.Fatal("duplicated unqualified qualname must not bypass the generic-name guard")
	}
	convert := &graph.Node{Name: "Convert", QualName: "Convert", FilePath: "runtime/value.cs"}
	if exploreDraftExactAnchor("review Convert behavior", convert) {
		t.Fatal("unqualified cross-language generic method must not become an exact anchor")
	}
	convert.QualName = "Thing.Convert"
	if !exploreDraftExactAnchor("review Thing.Convert behavior", convert) {
		t.Fatal("qualified cross-language generic method should remain an exact anchor")
	}
}

func TestRenderExploreBroadWellAlignedNeighborhoodRemainsAnswerReady(t *testing.T) {
	targets := []exploreTarget{{
		node:  &graph.Node{ID: "internal/mcp/facade_tools.go::registerFacadeTools", Name: "registerFacadeTools", Kind: graph.KindMethod, FilePath: "internal/mcp/facade_tools.go"},
		score: 1.2,
	}}
	task := "investigate mcp facade tool routing schema registration operation dispatch surface architecture integration behavior"
	out := (&Server{}).renderExplore(task, targets, 1600)
	if !strings.Contains(out, "LOCALIZATION COMPLETE:") || !strings.Contains(out, "use the strongest supported rows") || !strings.Contains(out, "proceed directly to impact, edit, and test") {
		t.Fatalf("well-aligned ranked head must preserve strong terminality:\n%s", out)
	}
	for _, forbidden := range []string{"exact-ID source read", "REFINEMENT", "rerun explore", "read(operation:"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("well-aligned neighborhood must not invite another call via %q:\n%s", forbidden, out)
		}
	}
}

func TestRenderExplorePacksStrongestDraftBody(t *testing.T) {
	targets := []exploreTarget{
		{node: &graph.Node{ID: "generic.go::Current", Name: "Current", Kind: graph.KindFunction, FilePath: "generic.go", StartLine: 1}, source: "func Current() {\n\tgenericOne()\n}"},
		{node: &graph.Node{ID: "generic.go::State", Name: "State", Kind: graph.KindFunction, FilePath: "generic.go", StartLine: 10}, source: "func State() {\n\tgenericTwo()\n}"},
		{node: &graph.Node{ID: "retry.go::DeepTarget", Name: "DeepTarget", Kind: graph.KindFunction, FilePath: "retry.go", StartLine: 20}, source: "func DeepTarget() {\n\tdecisiveBody()\n}"},
	}
	out := (&Server{}).renderExplore("locate DeepTarget implementation", targets, 9000)
	draftStart := strings.Index(out, "## Answer draft")
	if draftStart < 0 {
		t.Fatalf("missing answer draft:\n%s", out)
	}
	if first := strings.Index(out[draftStart:], "SYMBOL:"); first < 0 || !strings.HasPrefix(out[draftStart+first:], "SYMBOL: DeepTarget") {
		t.Fatalf("query-aligned target must lead the answer draft:\n%s", out)
	}
	if !strings.Contains(out, "decisiveBody()") {
		t.Fatalf("query-aligned direct target must receive a full source body:\n%s", out)
	}
}

func TestRenderExploreDefaultBudgetRetainsTopBody(t *testing.T) {
	targets := exploreTestTargets()
	for i := 0; i < 4; i++ {
		targets = append(targets, exploreTarget{node: &graph.Node{
			ID:        fmt.Sprintf("internal/retry/candidate_%d.go::Candidate%d", i, i),
			Name:      fmt.Sprintf("Candidate%d", i),
			Kind:      graph.KindFunction,
			FilePath:  fmt.Sprintf("internal/retry/candidate_%d.go", i),
			StartLine: 10 + i,
		}})
	}
	out := (&Server{}).renderExplore("the retry backoff never fires on 429", targets, exploreDefaultBudgetTokens)
	if !strings.Contains(out, "func DoWithRetry(max int) error") {
		t.Fatalf("compact answer draft must not displace the top source body at the default budget:\n%s", out)
	}
	draftStart := strings.Index(out, "## Answer draft")
	detailsStart := strings.Index(out, "## Likely targets")
	if draftStart < 0 || detailsStart < 0 {
		t.Fatalf("missing draft/detail sections:\n%s", out)
	}
	if rows := strings.Count(out[draftStart:detailsStart], "- FILE:"); rows > exploreDraftTotalLimit {
		t.Fatalf("answer draft has %d rows, limit %d:\n%s", rows, exploreDraftTotalLimit, out[draftStart:detailsStart])
	}
}

func TestRenderExploreBudgetTruncation(t *testing.T) {
	// A budget below the body cost forces demotion to signatures, but every
	// candidate's LOCATION must still be listed (file-hit/symbol-hit never
	// depend on the budget) and the truncation must be reported honestly.
	out := (&Server{}).renderExplore("task", exploreTestTargets(), exploreMinBudgetTokens)
	for _, want := range []string{
		"1. DoWithRetry  function  ·  retry.go:11-20",
		"2. Backoff  function  ·  retry.go:6-8",
		"— Coverage:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("truncated render missing %q", want)
		}
	}
}

func TestRenderExploreLimitsFullBodiesToTopTargets(t *testing.T) {
	targets := exploreTestTargets()
	targets = append(targets, exploreTarget{
		node:   &graph.Node{Name: "Third", Kind: graph.KindFunction, FilePath: "third.go", StartLine: 1, EndLine: 4, Language: "go"},
		source: "func Third() int {\n\treturn 3\n}",
	})
	out := (&Server{}).renderExplore("task", targets, exploreDefaultBudgetTokens)
	if exploreDefaultBudgetTokens != 1600 {
		t.Fatalf("default explore budget=%d want 1600", exploreDefaultBudgetTokens)
	}
	if !strings.Contains(out, "3. Third  function") {
		t.Fatal("third candidate header was dropped")
	}
	if strings.Contains(out, "return 3") {
		t.Fatal("third candidate unexpectedly retained a full body")
	}
}

func TestExploreHelpers(t *testing.T) {
	if got := clampInt(5, 1, 3); got != 3 {
		t.Errorf("clampInt hi: got %d", got)
	}
	if got := clampInt(0, 2, 10); got != 2 {
		t.Errorf("clampInt lo: got %d", got)
	}
	if got := truncateOneLine("a\n b\tc", 100); got != "a b c" {
		t.Errorf("truncateOneLine collapse: %q", got)
	}
	if got := truncateOneLine(strings.Repeat("x", 10), 4); got != "xxxx…" {
		t.Errorf("truncateOneLine cap: %q", got)
	}
	if got := firstLines("1\n2\n3\n4", 2); got != "1\n2" {
		t.Errorf("firstLines: %q", got)
	}
	if got := dedupStrings([]string{"b", "a", "b"}); strings.Join(got, ",") != "a,b" {
		t.Errorf("dedupStrings: %v", got)
	}
	got := exploreConceptRecallTerms("Find the code responsible for case insensitive alternation literal prefixes")
	joined := strings.Join(got, ",")
	for _, want := range []string{"case", "insensitive", "alternation", "literal", "prefix"} {
		if !strings.Contains(joined, want) {
			t.Errorf("concept recall terms missing %q: %v", want, got)
		}
	}
	for _, noise := range []string{"find", "code"} {
		if strings.Contains(joined, noise) {
			t.Errorf("concept recall terms retained generic %q: %v", noise, got)
		}
	}
	if hasExploreExpansionTerms("case insensitive alternation", []string{"case", "alternation"}) {
		t.Fatal("subset concept bag should not trigger duplicate retrieval")
	}
	if hasExploreExpansionTerms("case insensitive alternations", []string{"case", "alternation"}) {
		t.Fatal("FTS-equivalent concept root should not trigger duplicate retrieval")
	}
	if !hasExploreExpansionTerms("case insensitive alternation", []string{"case", "prefix"}) {
		t.Fatal("genuinely new concept term should trigger expansion retrieval")
	}

	semantic := &rerank.Candidate{Node: &graph.Node{ID: "semantic"}, TextRank: -1, VectorRank: 0}
	textDuplicate := &rerank.Candidate{Node: &graph.Node{ID: "semantic"}, TextRank: 2, VectorRank: -1}
	textOnly := &rerank.Candidate{Node: &graph.Node{ID: "text"}, TextRank: 0, VectorRank: -1}
	merged := mergeExploreCandidates([]*rerank.Candidate{semantic}, []*rerank.Candidate{textDuplicate, textOnly}, 80)
	if len(merged) != 2 || merged[0].Node.ID != "semantic" || merged[0].VectorRank != 0 || merged[0].TextRank != 82 {
		t.Fatalf("candidate merge lost hybrid ranks or expansion provenance: %#v", merged)
	}
	if semantic.TextRank != -1 {
		t.Fatalf("candidate merge mutated its primary input: %#v", semantic)
	}

	primaryText := &rerank.Candidate{Node: &graph.Node{ID: "primary"}, TextRank: 0, VectorRank: -1}
	primaryMerged := mergeExploreCandidates([]*rerank.Candidate{primaryText}, []*rerank.Candidate{{Node: primaryText.Node, TextRank: 0, VectorRank: -1}}, 80)
	if primaryMerged[0].TextRank != 0 {
		t.Fatalf("expansion rank displaced primary query rank: %#v", primaryMerged[0])
	}

	window := make([]*rerank.Candidate, 0, 11)
	for i := 0; i < 10; i++ {
		window = append(window, &rerank.Candidate{Node: &graph.Node{ID: fmt.Sprintf("text-%d", i)}, TextRank: i, VectorRank: -1})
	}
	window = append(window, &rerank.Candidate{Node: &graph.Node{ID: "vector-only"}, TextRank: -1, VectorRank: 0})
	bounded := limitExploreCandidates(window, 5)
	foundVector := false
	for _, candidate := range bounded {
		foundVector = foundVector || candidate.Node.ID == "vector-only"
	}
	if !foundVector {
		t.Fatalf("bounded candidate union dropped top vector-only evidence: %#v", bounded)
	}
}

// TestFacadeExploreDemotesRepeatedDataLeafNames reproduces the reported
// localization failure through the public facade: many unrelated declarations
// named `client` used to consume the whole result head, excluding the callable
// definition that explains the area. One best data-leaf match may remain, but
// repeated same-name leaves must not crowd out a differently-named code target.
func TestFacadeExploreDemotesRepeatedDataLeafNames(t *testing.T) {
	g := graph.New()
	bm := search.NewBM25()
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("pkg/service%d.go::client", i)
		n := &graph.Node{
			ID: id, Name: "client", Kind: graph.KindVariable,
			FilePath: fmt.Sprintf("pkg/service%d.go", i), Language: "go",
			Meta: map[string]any{"signature": "var client *Transport"},
		}
		g.AddNode(n)
		bm.Add(id, n.Name, n.FilePath, "trace client coordinated transport")
	}
	relevant := &graph.Node{
		ID: "pkg/coordinator.go::TransportCoordinator", Name: "TransportCoordinator",
		Kind: graph.KindFunction, FilePath: "pkg/coordinator.go", Language: "go",
		Meta: map[string]any{"signature": "func TransportCoordinator()"},
	}
	g.AddNode(relevant)
	// It matches the whole task, but its longer prose and non-literal symbol
	// name rank below the short exact-name `client` declarations. The concept
	// over-fetch window must retain it so name diversification can promote it.
	relevantText := strings.Repeat("architecture routing plumbing lifecycle ", 40) + "trace client coordinated transport coordinator"
	bm.Add(relevant.ID, relevant.Name, relevant.FilePath, relevantText)

	eng := query.NewEngine(g)
	eng.SetSearch(bm)
	srv := NewServer(eng, g, nil, nil, zap.NewNop(), nil)
	task := "trace how the client is coordinated"
	searchQuery := shapeExploreQuery(task)
	const maxSymbols = 6
	queryClass := rerank.ClassifyQuery(searchQuery)
	raw := eng.SearchSymbolsRanked(searchQuery, exploreCandidateFetchLimit(maxSymbols, queryClass), query.QueryOptions{}, srv.buildRerankContext(context.Background(), searchQuery))
	rawHead := make([]string, 0, 6)
	relevantRetrieved := false
	for _, candidate := range raw {
		if candidate != nil && candidate.Node != nil && candidate.Node.ID == relevant.ID {
			relevantRetrieved = true
		}
		if len(rawHead) == 6 {
			continue
		}
		if candidate == nil || candidate.Node == nil || !exploreLocalizableKind(candidate.Node.Kind) || !exploreCodeDefinitionKind(candidate.Node.Kind) {
			continue
		}
		rawHead = append(rawHead, candidate.Node.ID)
	}
	if len(rawHead) != 6 {
		t.Fatalf("fixture produced only %d raw localization candidates: %v", len(rawHead), rawHead)
	}
	if !relevantRetrieved {
		rawIDs := make([]string, 0, len(raw))
		for _, candidate := range raw {
			if candidate != nil && candidate.Node != nil {
				rawIDs = append(rawIDs, candidate.Node.ID)
			}
		}
		t.Fatalf("fixture's relevant callable fell outside the concept-query over-fetch window: query=%q raw=%v", searchQuery, rawIDs)
	}
	for _, id := range rawHead {
		if id == relevant.ID {
			t.Fatalf("fixture no longer reproduces crowd-out before explore diversification: %v", rawHead)
		}
	}
	req := mcpgo.CallToolRequest{}
	// Omit operation deliberately: the facade default must route to task.
	req.Params.Arguments = map[string]any{
		"task": task, "options": map[string]any{"max_symbols": maxSymbols},
	}
	result, err := srv.handleFacade(context.Background(), "explore", req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || len(result.Content) == 0 {
		t.Fatalf("explore failed: %#v", result)
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("unexpected explore result content: %#v", result.Content[0])
	}
	out := text.Text
	if strings.Contains(out, "get_symbol_source") || strings.Contains(out, "batch_symbols") || strings.Contains(out, "search_text") || strings.Contains(out, "find_files") {
		t.Fatalf("explore emitted unavailable legacy follow-up guidance:\n%s", out)
	}
	relevantAt := strings.Index(out, "TransportCoordinator")
	if relevantAt < 0 {
		t.Fatalf("callable target was crowded out by repeated data leaves:\n%s", out)
	}
	firstClient := strings.Index(out, ". client  variable")
	if firstClient < 0 {
		t.Fatalf("fixture did not surface its best literal data-leaf match:\n%s", out)
	}
	secondClient := strings.Index(out[firstClient+1:], ". client  variable")
	if secondClient >= 0 && firstClient+1+secondClient < relevantAt {
		t.Fatalf("repeated generic data leaves still rank ahead of the callable target:\n%s", out)
	}
}

func TestDiversifyRepeatedExploreNamesIsConceptOnlyAndStable(t *testing.T) {
	leaf1 := &rerank.Candidate{Node: &graph.Node{ID: "a", Name: "client", Kind: graph.KindVariable}}
	leaf2 := &rerank.Candidate{Node: &graph.Node{ID: "b", Name: "client", Kind: graph.KindField}}
	validate1 := &rerank.Candidate{Node: &graph.Node{ID: "v1", Name: "Validate", Kind: graph.KindMethod}}
	validate2 := &rerank.Candidate{Node: &graph.Node{ID: "v2", Name: "Validate", Kind: graph.KindMethod}}
	validate3 := &rerank.Candidate{Node: &graph.Node{ID: "v3", Name: "Validate", Kind: graph.KindFunction}}
	callable := &rerank.Candidate{Node: &graph.Node{ID: "c", Name: "FacadeRegistry", Kind: graph.KindFunction}}
	input := []*rerank.Candidate{leaf1, leaf2, validate1, validate2, validate3, callable}

	concept := diversifyRepeatedExploreNames(append([]*rerank.Candidate(nil), input...), rerank.QueryClassConcept)
	want := []*rerank.Candidate{leaf1, validate1, validate2, callable, leaf2, validate3}
	for i := range want {
		if concept[i] != want[i] {
			t.Fatalf("concept diversification[%d]=%s want %s", i, concept[i].Node.ID, want[i].Node.ID)
		}
	}
	symbol := diversifyRepeatedExploreNames(append([]*rerank.Candidate(nil), input...), rerank.QueryClassSymbol)
	for i := range input {
		if symbol[i] != input[i] {
			t.Fatalf("identifier lookup order changed at %d", i)
		}
	}
	if got := exploreCandidateFetchLimit(6, rerank.QueryClassConcept); got != 48 {
		t.Fatalf("concept fetch limit=%d want 48", got)
	}
	if got := exploreCandidateFetchLimit(6, rerank.QueryClassSymbol); got != 24 {
		t.Fatalf("symbol fetch limit=%d want 24", got)
	}
}

func TestStripLeadingExploreDirective(t *testing.T) {
	for input, want := range map[string]string{
		"Audit and fix MCP facade impact handling":         "MCP facade impact handling",
		"Please validate operation schema discoverability": "operation schema discoverability",
		"How does Validate work":                           "How does Validate work",
		"fix impact":                                       "fix impact",
	} {
		if got := stripLeadingExploreDirective(input); got != want {
			t.Errorf("stripLeadingExploreDirective(%q)=%q want %q", input, got, want)
		}
	}
}
