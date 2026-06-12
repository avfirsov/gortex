package resolver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// buildMultiLangGraph extracts each fixture file with the extractor
// matching its extension (.go / .py / .ts / .js) and loads the result
// into a fresh graph. Unlike buildGraphFromSources (TS/JS only) this
// builder spans every ecosystem the external-call synthesis pass
// classifies, so one table can exercise Go modules, pip packages, and
// npm packages through the same real extract → resolve pipeline.
func buildMultiLangGraph(t *testing.T, files map[string]string) graph.Store {
	t.Helper()
	g := graph.New()
	for path, src := range files {
		var (
			nodes []*graph.Node
			edges []*graph.Edge
		)
		switch {
		case strings.HasSuffix(path, ".go"):
			r, err := languages.NewGoExtractor().Extract(path, []byte(src))
			require.NoError(t, err, "go extract %s", path)
			nodes, edges = r.Nodes, r.Edges
		case strings.HasSuffix(path, ".py"):
			r, err := languages.NewPythonExtractor().Extract(path, []byte(src))
			require.NoError(t, err, "py extract %s", path)
			nodes, edges = r.Nodes, r.Edges
		case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
			r, err := languages.NewTypeScriptExtractor().Extract(path, []byte(src))
			require.NoError(t, err, "ts extract %s", path)
			nodes, edges = r.Nodes, r.Edges
		default:
			r, err := languages.NewJavaScriptExtractor().Extract(path, []byte(src))
			require.NoError(t, err, "js extract %s", path)
			nodes, edges = r.Nodes, r.Edges
		}
		for _, n := range nodes {
			g.AddNode(n)
		}
		for _, e := range edges {
			g.AddEdge(e)
		}
	}
	return g
}

// resolveAndSynthesize runs the production resolution pipeline against g
// — the per-edge ResolveAll pass plus the cross-package guard it ends
// with — and then the opt-in external-call synthesis pass. It mirrors
// the indexer settle point: synthesis runs strictly after resolution +
// guard, so the test exercises the same ordering the daemon uses.
func resolveAndSynthesize(g graph.Store, enabled bool) int {
	New(g).ResolveAll()
	return SynthesizeExternalCalls(g, enabled)
}

// callTargetsFrom collects the To-end of every call/reference edge
// leaving fromID, so a test can assert on the post-resolution shape of
// a caller's outbound calls.
func callTargetsFrom(g graph.Store, fromID string) []string {
	var out []string
	for _, e := range g.GetOutEdges(fromID) {
		if e.Kind == graph.EdgeCalls || e.Kind == graph.EdgeReferences {
			out = append(out, e.To)
		}
	}
	return out
}

// TestSynthesizeExternalCalls drives the pass through a real
// extract → resolve → synthesize pipeline for each ecosystem. Every row
// fixes one caller's call into an un-indexed external target and
// asserts, with the option both on and off, what the call edge lands
// on — and that language built-ins / standard-library calls are
// filtered out as noise.
func TestSynthesizeExternalCalls(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		// callerID identifies the function whose outbound call is
		// under test.
		callerID string
		// wantSyntheticID, when set with the option ON, is the
		// synthetic node ID the call edge must retarget onto.
		wantSyntheticID string
		// wantEcosystem / wantImportPath are asserted on the
		// synthetic node's Meta when wantSyntheticID is set.
		wantEcosystem  string
		wantImportPath string
		// noiseOnly marks a fixture whose only external call is a
		// language built-in / stdlib hop: the filter must synthesize
		// nothing even with the option ON.
		noiseOnly bool
	}{
		{
			// An un-indexed npm package: `axios` is imported and
			// called, but no axios source is in the graph. With the
			// option on, the call must terminate on a synthetic node.
			name: "un-indexed npm package call",
			files: map[string]string{
				"web/api.ts": `import axios from "axios";
export function fetchUser(): void {
  axios.get("/user");
}`,
			},
			callerID:        "web/api.ts::fetchUser",
			wantSyntheticID: externalCallNodeID("stdlib", "axios"),
			wantEcosystem:   "stdlib",
			wantImportPath:  "axios",
		},
		{
			// A sibling microservice's client SDK — a scoped package
			// that is not part of this repo's index. The call into it
			// must be preserved as an explicit external terminal.
			name: "sibling-service client SDK call",
			files: map[string]string{
				"web/orders.ts": `import billing from "@acme/billing-service-client";
export function charge(): void {
  billing.createInvoice();
}`,
			},
			callerID:        "web/orders.ts::charge",
			wantSyntheticID: externalCallNodeID("stdlib", "@acme/billing-service-client"),
			wantEcosystem:   "stdlib",
			wantImportPath:  "@acme/billing-service-client",
		},
		{
			// An un-indexed Go third-party module — a domain-qualified
			// import path, so the resolver lands it on a `dep::`
			// terminal. The synthetic node must carry it through.
			name: "un-indexed go module call",
			files: map[string]string{
				"svc/main.go": `package main

import "github.com/acme/stripe"

func Pay() {
	stripe.New("key")
}`,
			},
			callerID:        "svc/main.go::Pay",
			wantSyntheticID: externalCallNodeID("dep", "github.com/acme/stripe"),
			wantEcosystem:   "dep",
			wantImportPath:  "github.com/acme/stripe",
		},
		{
			// An un-indexed pip package: `requests` is imported and
			// called. Not a stdlib module, so it must be synthesized.
			name: "un-indexed pip package call",
			files: map[string]string{
				"app/client.py": `import requests

def fetch():
    requests.get("/health")
`,
			},
			callerID:        "app/client.py::fetch",
			wantSyntheticID: externalCallNodeID("stdlib", "requests"),
			wantEcosystem:   "stdlib",
			wantImportPath:  "requests",
		},
		{
			// Noise filter — Go standard library. Every Go file calls
			// `fmt`; materialising a node for it would bury the real
			// cross-system edges. Nothing must be synthesized.
			name: "go stdlib call is filtered as noise",
			files: map[string]string{
				"svc/log.go": `package main

import "fmt"

func Log() {
	fmt.Println("hello")
}`,
			},
			callerID:  "svc/log.go::Log",
			noiseOnly: true,
		},
		{
			// Noise filter — Python standard library. `os.getenv` is
			// an interpreter built-in, not a pip dependency.
			name: "python stdlib call is filtered as noise",
			files: map[string]string{
				"app/env.py": `import os

def home():
    os.getenv("HOME")
`,
			},
			callerID:  "app/env.py::home",
			noiseOnly: true,
		},
		{
			// Noise filter — a Node.js core module. `node:fs` is part
			// of the runtime, not an npm package.
			name: "node core module call is filtered as noise",
			files: map[string]string{
				"web/disk.ts": `import fs from "node:fs";
export function read(): void {
  fs.readFileSync("/etc/hosts");
}`,
			},
			callerID:  "web/disk.ts::read",
			noiseOnly: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Option OFF (the default): the synthesis pass is a pure
			// no-op — the resolved graph is left exactly as resolution
			// produced it, with no synthetic node and no retargeted
			// edge. Snapshot the counts after ResolveAll (which itself
			// mutates the graph) so the assertion isolates the pass.
			gOff := buildMultiLangGraph(t, tc.files)
			New(gOff).ResolveAll()
			nodesAfterResolve := gOff.NodeCount()
			edgesAfterResolve := gOff.EdgeCount()
			synthesizedOff := SynthesizeExternalCalls(gOff, false)
			assert.Zero(t, synthesizedOff, "option OFF must synthesize nothing")
			assert.Equal(t, nodesAfterResolve, gOff.NodeCount(),
				"option OFF must not add any node")
			assert.Equal(t, edgesAfterResolve, gOff.EdgeCount(),
				"option OFF must not add any edge")
			for _, to := range callTargetsFrom(gOff, tc.callerID) {
				assert.NotContains(t, to, externalCallPrefix,
					"option OFF must leave call edges off synthetic nodes")
			}

			// Option ON: genuine externals get a synthetic terminal;
			// noise (builtin / stdlib) is still filtered out.
			gOn := buildMultiLangGraph(t, tc.files)
			synthesizedOn := resolveAndSynthesize(gOn, true)

			if tc.noiseOnly {
				assert.Zero(t, synthesizedOn,
					"noise filter must exclude builtin/stdlib calls")
				for _, to := range callTargetsFrom(gOn, tc.callerID) {
					assert.NotContains(t, to, externalCallPrefix,
						"a builtin/stdlib call must not gain a synthetic node")
				}
				return
			}

			require.Positive(t, synthesizedOn,
				"a genuine external call must be synthesized with the option ON")

			// The synthetic node exists and is marked unmistakably as
			// both synthetic and external so analyzers can filter it.
			node := gOn.GetNode(tc.wantSyntheticID)
			require.NotNil(t, node, "synthetic external node must be added")
			assert.Equal(t, graph.KindModule, node.Kind)
			assert.Equal(t, tc.wantImportPath, node.Name)
			assert.Equal(t, true, node.Meta["synthetic"])
			assert.Equal(t, true, node.Meta["external_call"])
			assert.Equal(t, tc.wantEcosystem, node.Meta["ecosystem"])
			assert.Equal(t, tc.wantImportPath, node.Meta["import_path"])

			// The call edge was retargeted onto the synthetic node and
			// the synthetic node sees the inbound call edge — so a
			// call-chain walk reaches the external terminal.
			targets := callTargetsFrom(gOn, tc.callerID)
			assert.Contains(t, targets, tc.wantSyntheticID,
				"the call edge must retarget onto the synthetic node")
			require.NotEmpty(t, gOn.GetInEdges(tc.wantSyntheticID),
				"synthetic node must see the inbound call edge")

			// The retargeted edge is marked so a query can pick out the
			// cross-system terminals this pass created.
			edge := firstOutEdgeByKind(gOn, tc.callerID, graph.EdgeCalls)
			require.NotNil(t, edge)
			assert.Equal(t, tc.wantSyntheticID, edge.To)
			assert.Equal(t, true, edge.Meta["external_call"])
			assert.Equal(t, graph.OriginTextMatched, edge.Origin)
		})
	}
}

// TestSynthesizeExternalCalls_Idempotent pins the full-recompute
// contract: re-running the pass rewrites every edge onto the same
// deterministic synthetic node and accretes no duplicate node or edge.
func TestSynthesizeExternalCalls_Idempotent(t *testing.T) {
	files := map[string]string{
		"web/api.ts": `import axios from "axios";
export function fetchUser(): void {
  axios.get("/user");
}`,
	}
	g := buildMultiLangGraph(t, files)
	New(g).ResolveAll()

	first := SynthesizeExternalCalls(g, true)
	nodesAfterFirst := g.NodeCount()
	second := SynthesizeExternalCalls(g, true)
	third := SynthesizeExternalCalls(g, true)

	assert.Equal(t, 1, first)
	assert.Equal(t, first, second, "re-run must report the same count")
	assert.Equal(t, first, third)
	assert.Equal(t, nodesAfterFirst, g.NodeCount(),
		"re-run must not add a duplicate synthetic node")

	syntheticID := externalCallNodeID("stdlib", "axios")
	require.Len(t, g.GetInEdges(syntheticID), 1,
		"re-run must not accrete a duplicate inbound edge")
}

// TestSynthesizeExternalCalls_DisabledByDefault guards the
// zero-config contract: a graph carrying an un-indexed external call
// is left completely untouched when the option is off — the default.
func TestSynthesizeExternalCalls_DisabledByDefault(t *testing.T) {
	files := map[string]string{
		"app/client.py": `import requests

def fetch():
    requests.get("/health")
`,
	}
	g := buildMultiLangGraph(t, files)
	New(g).ResolveAll()

	nodesBefore := g.NodeCount()
	edgesBefore := g.EdgeCount()
	synthesized := SynthesizeExternalCalls(g, false)

	assert.Zero(t, synthesized)
	assert.Equal(t, nodesBefore, g.NodeCount(), "default-off must add no node")
	assert.Equal(t, edgesBefore, g.EdgeCount(), "default-off must add no edge")
}

// TestParseExternalCallTarget unit-tests the terminal classifier: the
// three external bookkeeping-string shapes yield the right ecosystem +
// import path, while real node IDs, bare placeholders, `builtin::`
// terminals, and already-synthesised nodes are rejected.
func TestParseExternalCallTarget(t *testing.T) {
	cases := []struct {
		target        string
		wantOK        bool
		wantEcosystem string
		wantPath      string
	}{
		{"dep::github.com/foo/bar::Baz", true, "dep", "github.com/foo/bar"},
		{"stdlib::axios::get", true, "stdlib", "axios"},
		{"stdlib::@acme/svc-client::call", true, "stdlib", "@acme/svc-client"},
		{"external::lodash", true, "external", "lodash"},
		// Rejected: real node IDs and non-external placeholders.
		{"pkg/foo.go::Bar", false, "", ""},
		{"unresolved::Foo", false, "", ""},
		{"unresolved::*.foo", false, "", ""},
		{"builtin::js::array::push", false, "", ""},
		{externalCallPrefix + "dep::x", false, "", ""},
		// `dep::` / `stdlib::` with no `::<symbol>` separator is malformed.
		{"dep::", false, "", ""},
		{"stdlib::axios", false, "", ""},
		{"external::", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			eco, path, ok := parseExternalCallTarget(tc.target)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantEcosystem, eco)
			assert.Equal(t, tc.wantPath, path)
		})
	}
}

// TestIsLanguageStdlib unit-tests the language-aware noise filter: the
// same import-path shape is stdlib or third-party depending on the
// caller's language.
func TestIsLanguageStdlib(t *testing.T) {
	cases := []struct {
		lang string
		path string
		want bool
	}{
		// Go: un-dotted first segment is stdlib; domain-led is a module.
		{"go", "fmt", true},
		{"go", "net/http", true},
		{"go", "encoding/json", true},
		{"go", "github.com/stripe/stripe-go", false},
		{"go", "gopkg.in/yaml.v3", false},
		// Python: interpreter modules vs pip packages.
		{"python", "os", true},
		{"python", "os.path", true},
		{"python", "collections", true},
		{"python", "requests", false},
		{"python", "numpy", false},
		// Node: core modules (bare and node:-prefixed) vs npm packages.
		{"javascript", "fs", true},
		{"typescript", "node:crypto", true},
		{"typescript", "stream/promises", true},
		{"typescript", "axios", false},
		{"typescript", "@acme/billing-client", false},
		// Rust: the std distribution vs crates.
		{"rust", "std", true},
		{"rust", "core::mem", true},
		{"rust", "tokio", false},
		// Unknown language: treat the path as external so a real edge
		// is never dropped by a missing rule.
		{"", "anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.lang+":"+tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, isLanguageStdlib(tc.lang, tc.path))
		})
	}
}
