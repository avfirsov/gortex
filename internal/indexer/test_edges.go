package indexer

import (
	"path/filepath"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// markTestSymbolsAndEmitEdges runs after the resolver and before
// community detection. It performs two passes over the graph:
//
//  1. Walk every function/method node that lives in a test file (per
//     IsTestFile) and stamp Meta["test_role"] — "benchmark", "fuzz",
//     or "example" when the name matches a per-language convention
//     (per TestRole), otherwise "test" for plain test support code.
//     Meta["is_test"] = true is stamped alongside for back-compat with
//     consumers that only need the boolean. Symbols whose runner
//     discovers tests by attribute rather than by file location (Rust
//     #[test], JVM @Test — see AnnotationTestRole) are additionally
//     stamped from their EdgeAnnotated edges, so an inline #[test] fn
//     in a production-path file classifies too.
//
//  2. Walk every EdgeCalls. For each call whose source is a test
//     function and whose target is non-test, emit a parallel
//     EdgeTests pointing to the same target.
//
// The split lets agents distinguish prod callers from test callers
// (find_usages with exclude_tests) and lets get_test_targets answer
// "which tests cover X?" with a single reverse-edge walk instead of
// the runtime call-graph traversal it does today.
//
// Returns counts for telemetry: number of nodes marked as test,
// number of EdgeTests emitted.
func markTestSymbolsAndEmitEdges(g graph.Store) (markedTests int, edgesEmitted int) {
	return markTestSymbolsAndEmitEdgesScoped(g, nil)
}

// markTestSymbolsAndEmitEdgesScoped is markTestSymbolsAndEmitEdges with an armed
// changed-repo scope for the end-of-batch pass. A nil scope emits over the whole
// graph, so the fresh-index / single-repo path is byte-identical.
//
// Pass 1 (test-symbol classification) always runs whole-graph: the testNodes
// membership set it builds must be COMPLETE, because Pass 2 skips test→test
// calls via testNodes[e.To] and a callee can be a test in an unchanged repo
// (a cross-repo test→test call). Only Pass 2's driving EdgeCalls scan is scoped
// — an EdgeTests edge is FROM a test function, so a changed repo owns exactly
// the test edges its reindex dropped; an unchanged repo's persist on disk.
func markTestSymbolsAndEmitEdgesScoped(g graph.Store, changedPrefixes map[string]bool) (markedTests int, edgesEmitted int) {
	if g == nil {
		return 0, 0
	}
	// Serialise Node.Meta mutation against other graph-wide passes
	// (detectClonesAndEmitEdges, ResolveTemporalCalls, reach.BuildIndex).
	// See clones.go for the rationale — without this lock the writes
	// below race the readers and the runtime aborts with "concurrent
	// map read and map write".
	g.ResolveMutex().Lock()
	defer g.ResolveMutex().Unlock()

	testNodes, markedTests := markTestSymbolsLocked(g)
	if len(testNodes) == 0 {
		return markedTests, 0
	}
	edgesEmitted = emitTestEdgesLocked(g, testNodes, changedPrefixes)
	return markedTests, edgesEmitted
}

// markTestSymbolsLocked runs Pass 1: it stamps test Meta on every test symbol
// and returns the complete test-node membership set plus the marked count. The
// caller must hold g.ResolveMutex(). Always whole-graph — see the scoped
// entry point for why the set must be complete.
func markTestSymbolsLocked(g graph.Store) (testNodes map[string]bool, markedTests int) {
	// Pass 1: classify file nodes, then function/method nodes. Build
	// a local testNodes set keyed by node id so Pass 2 can probe it
	// without re-walking the Meta. (Node.Meta mutations on returned
	// nodes don't persist back to disk backends, so a later GetNode
	// in Pass 2 wouldn't see the is_test flag we set here.)
	testFiles := map[string]bool{}     // file node ID → is test file
	fileRunners := map[string]string{} // file FilePath → test runner
	for n := range g.NodesByKind(graph.KindFile) {
		if n == nil {
			continue
		}
		if IsTestFile(n.FilePath) {
			testFiles[n.ID] = true
			if n.Meta == nil {
				n.Meta = map[string]any{}
			}
			n.Meta["is_test_file"] = true
			if runner := detectTestRunnerForFile(g, n); runner != "" {
				n.Meta["test_runner"] = runner
				fileRunners[n.FilePath] = runner
			}
		}
	}

	// Annotation-driven test detection. Rust (#[test], #[tokio::test],
	// #[bench]) and JVM JUnit/TestNG (@Test, @ParameterizedTest, …)
	// runners discover tests by attribute, not by file location. The
	// language extractors already emit EdgeAnnotated edges to synthetic
	// annotation nodes (see EmitAnnotationEdge); consult them so an
	// inline #[test] fn in a production-path src/foo.rs — or a @Test
	// method in a class whose file name carries no test suffix — gets
	// the same is_test / test_role / EdgeTests treatment as a function
	// in a *_test.go file. Without this pass those tests are invisible
	// to get_test_targets / analyze kind=tests_as_edges / coverage_gaps.
	annoTestRole := map[string]string{} // symbol node ID → test role
	annoNodeRole := map[string]string{} // annotation node ID → role (cached resolution)
	for e := range g.EdgesByKind(graph.EdgeAnnotated) {
		if e == nil {
			continue
		}
		role, cached := annoNodeRole[e.To]
		if !cached {
			if anno := g.GetNode(e.To); anno != nil {
				role = AnnotationTestRole(anno.Language, anno.Name)
			}
			annoNodeRole[e.To] = role
		}
		if role == "" {
			continue
		}
		// Prefer the more specific "test" over "benchmark" when a
		// single symbol carries both (rare).
		if existing := annoTestRole[e.From]; existing == "" || (existing == "benchmark" && role == "test") {
			annoTestRole[e.From] = role
		}
	}

	testNodes = map[string]bool{}
	stampTestSymbol := func(n *graph.Node) {
		inTestFile := testFiles[n.FilePath]
		var role, runner string
		switch {
		case inTestFile:
			role = TestRole(n.Name, n.Language)
			if role == "" {
				role = "test"
			}
			runner = fileRunners[n.FilePath]
		case annoTestRole[n.ID] != "":
			role = annoTestRole[n.ID]
			runner = AnnotationTestRunner(n.Language)
		default:
			return
		}
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta["is_test"] = true
		n.Meta["test_role"] = role
		if runner != "" {
			n.Meta["test_runner"] = runner
		}
		testNodes[n.ID] = true
		markedTests++
	}
	for n := range g.NodesByKind(graph.KindFunction) {
		if n != nil {
			// Test-file membership is the authoritative signal. No
			// standard runner (go test, pytest, ...) picks up a test
			// by name outside a test file, so a production function
			// that merely starts with "Test"/"Benchmark" (e.g.
			// TestRole) must not be flagged. The name convention only
			// refines the *role* — benchmark / fuzz / example — for
			// symbols already inside a test file; anything else there
			// is test support code: role "test".
			stampTestSymbol(n)
		}
	}
	for n := range g.NodesByKind(graph.KindMethod) {
		if n != nil {
			stampTestSymbol(n)
		}
	}
	return testNodes, markedTests
}

// emitTestEdgesLocked runs Pass 2: for each (test, non-test) call it emits a
// parallel EdgeTests, deduped per (From, To) because a single test can call the
// same subject repeatedly. The testNodes set from Pass 1 is authoritative — no
// inline GetNode is needed because "From must be a test symbol" already enforces
// the kind filter (only function/method ids land in testNodes). The caller must
// hold g.ResolveMutex().
//
// With a nil scope it walks every EdgeCalls edge; with a scope it walks only the
// changed repos' out-edges (GetRepoEdges — one backend query per repo). The
// testNodes[e.To] test→test skip stays correct across repos because testNodes is
// complete (Pass 1 is whole-graph).
func emitTestEdgesLocked(g graph.Store, testNodes map[string]bool, changedPrefixes map[string]bool) int {
	edgesEmitted := 0
	seen := map[string]bool{}
	type pending struct {
		from, to, file string
		line           int
	}
	var out []pending
	process := func(e *graph.Edge) {
		if e == nil || e.Kind != graph.EdgeCalls {
			return
		}
		if !testNodes[e.From] {
			return
		}
		if testNodes[e.To] {
			return // test → test calls are infrastructure, not subject coverage
		}
		key := e.From + "\x00" + e.To
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, pending{from: e.From, to: e.To, file: e.FilePath, line: e.Line})
	}
	if changedPrefixes == nil {
		for e := range g.EdgesByKind(graph.EdgeCalls) {
			process(e)
		}
	} else {
		for prefix := range changedPrefixes {
			if prefix == "" {
				continue
			}
			for _, e := range g.GetRepoEdges(prefix) {
				process(e)
			}
		}
	}
	for _, p := range out {
		g.AddEdge(&graph.Edge{
			From:     p.from,
			To:       p.to,
			Kind:     graph.EdgeTests,
			FilePath: p.file,
			Line:     p.line,
			Origin:   graph.OriginASTInferred,
		})
		edgesEmitted++
	}
	return edgesEmitted
}

// detectTestRunnerForFile resolves the runner identifier for a test file
// node by consulting three signals, in priority order:
//
//  1. The file node's own Meta["test_runner"] — stamped by the JS / TS
//     extractors at parse time using DetectJSTSTestRunner. This is the
//     strongest signal because it has the file bytes to disambiguate
//     Mocha-TDD `suite(` from BDD `describe`.
//
//  2. Outgoing EdgeImports targets — the import path is preserved in
//     the target ID (e.g. `unresolved::import::pytest`) until the
//     resolver promotes the edge. Used as the primary signal for
//     languages where the parser does not run the JS / TS classifier
//     (Python: pytest vs unittest; Ruby: rspec vs minitest).
//
//  3. Language-level defaults that hold regardless of imports:
//     - Go always uses `gotest` — `go test` is the only runner.
//     - Python defaults to `pytest` (auto-discovery picks up unittest
//     test cases too; rare files that import only `unittest` are
//     caught by step 2).
//     - Ruby falls back to `rspec` for `_spec.rb` and `minitest` for
//     `_test.rb`.
//
// Returns "" when no signal applies; the caller leaves test_runner
// unset rather than guessing.
func detectTestRunnerForFile(g graph.Store, fileNode *graph.Node) string {
	if fileNode == nil {
		return ""
	}
	// 1) Parser-stamped runner (JS / TS).
	if fileNode.Meta != nil {
		if v, ok := fileNode.Meta["test_runner"].(string); ok && v != "" {
			return v
		}
	}
	// 2) Import-edge signal.
	if runner := detectRunnerFromImportEdges(g, fileNode); runner != "" {
		return runner
	}
	// 3) Language-level defaults.
	switch fileNode.Language {
	case "go":
		return "gotest"
	case "python":
		return "pytest"
	case "ruby":
		base := strings.ToLower(filepath.Base(fileNode.FilePath))
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		switch {
		case strings.HasSuffix(stem, "_spec"):
			return "rspec"
		case strings.HasSuffix(stem, "_test"):
			return "minitest"
		}
	}
	return ""
}

// detectRunnerFromImportEdges scans the outgoing EdgeImports of a test
// file node and returns a runner ID inferred from import paths. The
// import target ID format `unresolved::import::<path>` is preserved by
// the extractors until the resolver promotes the edge, which never
// happens for third-party / built-in modules — so this signal stays
// valid for the runner identifiers we care about. Supports JS / TS
// (mirrors DetectJSTSTestRunner so files compiled by a non-JS / TS
// extractor still classify correctly), Python (pytest / unittest),
// and Ruby (rspec / minitest).
func detectRunnerFromImportEdges(g graph.Store, fileNode *graph.Node) string {
	const prefix = "unresolved::import::"
	for _, e := range g.GetOutEdges(fileNode.ID) {
		if e == nil || e.Kind != graph.EdgeImports {
			continue
		}
		path := strings.TrimPrefix(e.To, prefix)
		path = strings.Trim(path, "\"'`")
		switch fileNode.Language {
		case "javascript", "typescript", "tsx", "jsx":
			switch {
			case path == "bun:test":
				return "bun-test"
			case path == "vitest" || strings.HasPrefix(path, "vitest/"):
				return "vitest"
			case path == "@playwright/test" || strings.HasPrefix(path, "@playwright/test/"):
				return "playwright"
			case path == "cypress" || strings.HasPrefix(path, "cypress/"):
				return "cypress"
			case path == "node:test" || strings.HasPrefix(path, "node:test/"):
				return "node-test"
			case path == "@jest/globals" || strings.HasPrefix(path, "@jest/globals/"),
				path == "jest" || strings.HasPrefix(path, "jest/"),
				path == "jest-mock", path == "ts-jest", path == "babel-jest",
				path == "@types/jest":
				return "jest"
			case path == "mocha" || strings.HasPrefix(path, "mocha/"),
				path == "@types/mocha", path == "mochawesome":
				return "mocha"
			}
		case "python":
			switch {
			case path == "pytest" || strings.HasPrefix(path, "pytest."),
				path == "pytest_asyncio" || path == "_pytest" || strings.HasPrefix(path, "_pytest."):
				return "pytest"
			case path == "unittest" || strings.HasPrefix(path, "unittest."):
				return "unittest"
			}
		case "ruby":
			switch {
			case path == "rspec" || strings.HasPrefix(path, "rspec/"),
				path == "rspec-core", path == "rspec/core":
				return "rspec"
			case path == "minitest" || strings.HasPrefix(path, "minitest/"),
				path == "minitest/autorun":
				return "minitest"
			case path == "test/unit":
				return "test-unit"
			}
		}
	}
	return ""
}
