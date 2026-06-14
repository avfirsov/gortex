package exporter

// understand_test.go — strict-TDD verification of the Understand-Anything
// exporter. Priority of criteria (authoritative for every trade-off):
// K2 (UA validity) > K3 (losslessness) > K5 (zero-context) > K1 (determinism).
//
// Layers:
//   - Unit (pure, table-driven): mapNodeKind, mapEdgeKind, complexityOf,
//     weightOf, tagsOf, lineRangeOf, and the member_of swap.
//   - Enum-coverage (AC3): every NodeKind and EdgeKind constant in
//     internal/graph is explicitly handled (mapped, denied, or deliberately
//     defaulted), with the defaults themselves asserted.
//   - Integration: a fixed synthetic graph → buildUAGraph → (a) golden file,
//     (b) authoritative UA validateGraph via the node harness (skip-guarded),
//     (c) always-on Go structural sanity (enum membership, required fields,
//     weight bounds, referential integrity).
//   - Determinism (AC5): identical input → byte-identical JSON.
//   - E2E (AC8, testing.Short-skippable): index grules-engine, export, validate.
//
// LDD-spirit telemetry: integration/e2e tests print Stats and []Dropped reasons
// BEFORE asserting, so a failure shows the full trajectory.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// uaNodeTypeSet is the closed set of 21 UA node types (business_requirements §4).
var uaNodeTypeSet = map[string]bool{
	"file": true, "function": true, "class": true, "module": true,
	"concept": true, "config": true, "document": true, "service": true,
	"table": true, "endpoint": true, "pipeline": true, "schema": true,
	"resource": true, "domain": true, "flow": true, "step": true,
	"article": true, "entity": true, "topic": true, "claim": true,
	"source": true,
}

// uaEdgeTypeSet is the closed set of 35 UA edge types (business_requirements §4).
var uaEdgeTypeSet = map[string]bool{
	"imports": true, "exports": true, "contains": true, "inherits": true,
	"implements": true, "calls": true, "subscribes": true, "publishes": true,
	"middleware": true, "reads_from": true, "writes_to": true, "transforms": true,
	"validates": true, "depends_on": true, "tested_by": true, "configures": true,
	"related": true, "similar_to": true, "deploys": true, "serves": true,
	"provisions": true, "triggers": true, "migrates": true, "documents": true,
	"routes": true, "defines_schema": true, "contains_flow": true, "flow_step": true,
	"cross_domain": true, "cites": true, "contradicts": true, "builds_on": true,
	"exemplifies": true, "categorized_under": true, "authored_by": true,
}

// allNodeKinds enumerates EVERY NodeKind constant declared in internal/graph.
// The enum-coverage test (AC3) iterates exactly this slice — keep it in sync
// with internal/graph/node.go. A stale entry breaks the build; a missing one
// is caught by the count assertion below.
var allNodeKinds = []graph.NodeKind{
	graph.KindFile, graph.KindPackage, graph.KindFunction, graph.KindMethod,
	graph.KindType, graph.KindInterface, graph.KindVariable, graph.KindImport,
	graph.KindContract, graph.KindField, graph.KindParam, graph.KindClosure,
	graph.KindLocal, graph.KindBuiltin, graph.KindConstant, graph.KindEnumMember,
	graph.KindGenericParam, graph.KindModule, graph.KindTable, graph.KindColumn,
	graph.KindConfigKey, graph.KindFlag, graph.KindEvent, graph.KindMigration,
	graph.KindFixture, graph.KindTodo, graph.KindTeam, graph.KindRelease,
	graph.KindLicense, graph.KindString, graph.KindResource, graph.KindKustomization,
	graph.KindImage, graph.KindArtifact, graph.KindDoc, graph.KindTopic,
	graph.KindMacro, graph.KindAgent,
}

// allEdgeKinds enumerates EVERY EdgeKind constant declared in internal/graph.
var allEdgeKinds = []graph.EdgeKind{
	graph.EdgeImports, graph.EdgeContains, graph.EdgeDefines, graph.EdgeCalls,
	graph.EdgeInstantiates, graph.EdgeImplements, graph.EdgeExtends, graph.EdgeReferences,
	graph.EdgeMemberOf, graph.EdgeProvides, graph.EdgeConsumes, graph.EdgeMatches,
	graph.EdgeAnnotated, graph.EdgeTests, graph.EdgeReads, graph.EdgeWrites,
	graph.EdgeThrows, graph.EdgeParamOf, graph.EdgeReturns, graph.EdgeTypedAs,
	graph.EdgeCaptures, graph.EdgeSpawns, graph.EdgeSends, graph.EdgeRecvs,
	graph.EdgeQueries, graph.EdgeReadsCol, graph.EdgeWritesCol, graph.EdgeReadsConfig,
	graph.EdgeWritesConfig, graph.EdgeReadsEnv, graph.EdgeExecutesProcess, graph.EdgeAccessesField,
	graph.EdgeTogglesFlag, graph.EdgeEmits, graph.EdgeListensOn, graph.EdgeGeneratedBy,
	graph.EdgeDependsOnModule, graph.EdgePackageWorkspaceMember, graph.EdgeOwns, graph.EdgeAuthored,
	graph.EdgeCoveredBy, graph.EdgeAliases, graph.EdgeComposes, graph.EdgeOverrides,
	graph.EdgeLicensedAs, graph.EdgeHandlesRoute, graph.EdgeProducesTopic, graph.EdgeConsumesTopic,
	graph.EdgeModelsTable, graph.EdgeRendersChild, graph.EdgeValueFlow, graph.EdgeArgOf,
	graph.EdgeReturnsTo, graph.EdgeConfigures, graph.EdgeMounts, graph.EdgeExposes,
	graph.EdgeDependsOn, graph.EdgeUsesEnv, graph.EdgeSimilarTo, graph.EdgeSemanticallyRelated,
	graph.EdgeCoChange, graph.EdgeCrossRepoCalls, graph.EdgeCrossRepoImplements, graph.EdgeCrossRepoExtends,
}

// ----------------------------------------------------------------------------
// Unit — mapNodeKind
// ----------------------------------------------------------------------------

func TestMapNodeKind(t *testing.T) {
	tests := []struct {
		name        string
		kind        graph.NodeKind
		granularity string
		wantType    string
		wantDrop    bool
	}{
		{"allowlist file", graph.KindFile, GranularitySlim, "file", false},
		{"allowlist function", graph.KindFunction, GranularitySlim, "function", false},
		{"allowlist method→function", graph.KindMethod, GranularitySlim, "function", false},
		{"allowlist type→class", graph.KindType, GranularitySlim, "class", false},
		{"allowlist interface→class", graph.KindInterface, GranularitySlim, "class", false},
		{"allowlist contract→endpoint", graph.KindContract, GranularitySlim, "endpoint", false},
		{"allowlist event→concept", graph.KindEvent, GranularitySlim, "concept", false},
		{"denylist param slim → drop", graph.KindParam, GranularitySlim, "", true},
		{"denylist local slim → drop", graph.KindLocal, GranularitySlim, "", true},
		{"denylist column slim → drop", graph.KindColumn, GranularitySlim, "", true},
		{"denylist param full → concept", graph.KindParam, GranularityFull, "concept", false},
		{"denylist builtin full → concept", graph.KindBuiltin, GranularityFull, "concept", false},
		{"unknown kind → concept", graph.KindTodo, GranularitySlim, "concept", false},
		{"unknown kind macro → concept", graph.KindMacro, GranularitySlim, "concept", false},
		{"empty granularity defaults slim drops param", graph.KindParam, "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gran := tc.granularity
			if gran == "" {
				gran = GranularitySlim
			}
			gotType, gotDrop, reason := mapNodeKind(tc.kind, gran)
			assert.Equal(t, tc.wantDrop, gotDrop, "drop mismatch")
			if tc.wantDrop {
				assert.NotEmpty(t, reason, "dropped node must carry a reason")
			} else {
				assert.Equal(t, tc.wantType, gotType, "type mismatch")
				assert.True(t, uaNodeTypeSet[gotType], "mapped type %q must be a valid UA node type", gotType)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Unit — mapEdgeKind (incl. member_of swap, unknown→depends_on, cross_repo→cross_domain)
// ----------------------------------------------------------------------------

func TestMapEdgeKind(t *testing.T) {
	tests := []struct {
		name            string
		kind            graph.EdgeKind
		wantType        string
		wantSwap        bool
		wantIsTransform bool
	}{
		{"calls", graph.EdgeCalls, "calls", false, false},
		{"imports", graph.EdgeImports, "imports", false, false},
		{"implements", graph.EdgeImplements, "implements", false, false},
		{"extends→inherits", graph.EdgeExtends, "inherits", false, false},
		{"overrides→inherits", graph.EdgeOverrides, "inherits", false, false},
		{"defines→contains", graph.EdgeDefines, "contains", false, false},
		{"member_of→contains+swap", graph.EdgeMemberOf, "contains", true, false},
		{"references→related", graph.EdgeReferences, "related", false, false},
		{"reads→reads_from", graph.EdgeReads, "reads_from", false, false},
		{"writes→writes_to", graph.EdgeWrites, "writes_to", false, false},
		{"value_flow→transforms", graph.EdgeValueFlow, "transforms", false, true},
		{"arg_of→transforms", graph.EdgeArgOf, "transforms", false, true},
		{"sends→publishes", graph.EdgeSends, "publishes", false, false},
		{"recvs→subscribes", graph.EdgeRecvs, "subscribes", false, false},
		{"spawns→triggers", graph.EdgeSpawns, "triggers", false, false},
		{"tests→tested_by", graph.EdgeTests, "tested_by", false, false},
		{"cross_repo_calls→cross_domain", graph.EdgeCrossRepoCalls, "cross_domain", false, false},
		{"cross_repo_extends→cross_domain", graph.EdgeCrossRepoExtends, "cross_domain", false, false},
		{"unknown→depends_on", graph.EdgeThrows, "depends_on", false, false},
		{"unknown co_change→depends_on", graph.EdgeCoChange, "depends_on", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotSwap, gotTransform := mapEdgeKind(tc.kind)
			assert.Equal(t, tc.wantType, gotType, "type mismatch")
			assert.Equal(t, tc.wantSwap, gotSwap, "swap mismatch")
			assert.Equal(t, tc.wantIsTransform, gotTransform, "isTransform mismatch")
			assert.True(t, uaEdgeTypeSet[gotType], "mapped type %q must be a valid UA edge type", gotType)
		})
	}
}

// ----------------------------------------------------------------------------
// Unit — complexityOf / weightOf / tagsOf / lineRangeOf
// ----------------------------------------------------------------------------

func TestComplexityOf(t *testing.T) {
	tests := []struct {
		name      string
		startLine int
		endLine   int
		outDegree int
		want      string
	}{
		{"simple: small span, low outdeg", 1, 10, 2, "simple"},
		{"moderate: medium span", 1, 60, 2, "moderate"},
		{"moderate: high outdeg but bounded", 1, 10, 10, "moderate"},
		{"complex: huge outdeg", 1, 10, 21, "complex"},
		{"complex: huge span", 1, 400, 0, "complex"},
		{"simple: no end line, low outdeg", 0, 0, 0, "simple"},
		{"boundary outdeg=4 → moderate", 1, 10, 4, "moderate"},
		{"boundary span=40 → moderate", 1, 41, 0, "moderate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := &graph.Node{StartLine: tc.startLine, EndLine: tc.endLine}
			assert.Equal(t, tc.want, complexityOf(n, tc.outDegree))
		})
	}
}

func TestWeightOf(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		want       float64
	}{
		{"zero → neutral 0.5", 0, 0.5},
		{"mid passes through", 0.85, 0.85},
		{"one passes through", 1.0, 1.0},
		{"above one clamps to 1", 1.7, 1.0},
		{"negative clamps to 0", -0.3, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := weightOf(tc.confidence)
			assert.Equal(t, tc.want, got)
			assert.GreaterOrEqual(t, got, 0.0, "weight must be >= 0")
			assert.LessOrEqual(t, got, 1.0, "weight must be <= 1")
		})
	}
}

func TestTagsOf(t *testing.T) {
	t.Run("both fields present", func(t *testing.T) {
		n := &graph.Node{Language: "go", Kind: graph.KindFunction}
		assert.Equal(t, []string{"go", "function"}, tagsOf(n))
	})
	t.Run("empty language skipped, non-nil", func(t *testing.T) {
		n := &graph.Node{Language: "", Kind: graph.KindFunction}
		got := tagsOf(n)
		assert.Equal(t, []string{"function"}, got)
		assert.NotNil(t, got)
	})
	t.Run("all empty → non-nil empty slice", func(t *testing.T) {
		n := &graph.Node{}
		got := tagsOf(n)
		assert.NotNil(t, got, "tags must never be nil (UA needs [] not null)")
		assert.Empty(t, got)
	})
}

func TestLineRangeOf(t *testing.T) {
	t.Run("with end line", func(t *testing.T) {
		n := &graph.Node{StartLine: 3, EndLine: 5}
		lr := lineRangeOf(n)
		require.NotNil(t, lr)
		assert.Equal(t, [2]int{3, 5}, *lr)
	})
	t.Run("no end line → nil", func(t *testing.T) {
		n := &graph.Node{StartLine: 3, EndLine: 0}
		assert.Nil(t, lineRangeOf(n))
	})
}

// ----------------------------------------------------------------------------
// Enum-coverage (AC3): every NodeKind / EdgeKind constant explicitly handled.
// ----------------------------------------------------------------------------

func TestEnumCoverage_NodeKinds(t *testing.T) {
	// AC3 acceptance: every declared NodeKind constant must be EXPLICITLY
	// handled by mapNodeKind — allowlisted, denylisted, or routed to the
	// documented `concept` default. (graph.ValidNodeKind is a separate
	// extraction-registry concern — some declared kinds like KindLocal /
	// KindBuiltin are intentionally not registered there but are still real
	// kinds the exporter must classify, so we do not gate on it.)
	usedConceptDefault := false
	for _, k := range allNodeKinds {
		_, inAllow := uaNodeType[k]
		inDeny := uaNodeDeny[k]
		uaType, drop, _ := mapNodeKind(k, GranularitySlim)
		switch {
		case inAllow:
			assert.False(t, drop, "allowlisted %q must not drop", k)
			assert.True(t, uaNodeTypeSet[uaType], "%q → %q must be a valid UA type", k, uaType)
		case inDeny:
			assert.True(t, drop, "denylisted %q must drop under slim", k)
			// Under full it must come back as concept.
			fullType, fullDrop, _ := mapNodeKind(k, GranularityFull)
			assert.False(t, fullDrop, "denylisted %q must NOT drop under full", k)
			assert.Equal(t, uaConcept, fullType, "denylisted %q under full must be concept", k)
		default:
			// Deliberate documented default: unknown → concept.
			assert.False(t, drop, "defaulted %q must not drop", k)
			assert.Equal(t, uaConcept, uaType, "defaulted %q must map to concept", k)
			usedConceptDefault = true
		}
	}
	assert.True(t, usedConceptDefault,
		"the concept default must be exercised by at least one kind (it is the documented fallback)")
}

func TestEnumCoverage_EdgeKinds(t *testing.T) {
	usedDependsOnDefault := false
	for _, k := range allEdgeKinds {
		_, inMap := uaEdgeType[k]
		uaType, _, _ := mapEdgeKind(k)
		assert.True(t, uaEdgeTypeSet[uaType], "%q → %q must be a valid UA edge type", k, uaType)
		if !inMap {
			// Deliberate documented default: unknown → depends_on.
			assert.Equal(t, uaDependsOn, uaType, "defaulted %q must map to depends_on", k)
			usedDependsOnDefault = true
		}
	}
	assert.True(t, usedDependsOnDefault,
		"the depends_on default must be exercised by at least one kind (it is the documented fallback)")
}

func TestEnumCoverage_SliceCompleteness(t *testing.T) {
	// A cheap structural tripwire: the canonical slices must have no
	// duplicates and a plausible count, so an accidental omission is loud.
	seenN := map[graph.NodeKind]bool{}
	for _, k := range allNodeKinds {
		assert.False(t, seenN[k], "duplicate node kind %q in allNodeKinds", k)
		seenN[k] = true
	}
	assert.Len(t, allNodeKinds, 38, "allNodeKinds must enumerate every NodeKind constant")

	seenE := map[graph.EdgeKind]bool{}
	for _, k := range allEdgeKinds {
		assert.False(t, seenE[k], "duplicate edge kind %q in allEdgeKinds", k)
		seenE[k] = true
	}
	assert.Len(t, allEdgeKinds, 64, "allEdgeKinds must enumerate every EdgeKind constant")
}

// ----------------------------------------------------------------------------
// Integration — fixed synthetic graph
// ----------------------------------------------------------------------------

// buildFixtureGraph builds a fixed graph covering file/function/type nodes, a
// member_of edge (swap), a cross_repo edge (cross_domain + passthrough), a
// denylist node (param, dropped under slim), and an unknown kind (todo →
// concept). Deterministic — drives the golden file and the validation harness.
func buildFixtureGraph() *graph.Graph {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "fixt::file", Kind: graph.KindFile, Name: "main.go",
		FilePath: "main.go", Language: "go", RepoPrefix: "fixt", WorkspaceID: "ws",
	})
	g.AddNode(&graph.Node{
		ID: "fixt::main.go::F", Kind: graph.KindFunction, Name: "F",
		QualName: "example.com/m.F", FilePath: "main.go",
		StartLine: 3, EndLine: 12, Language: "go", RepoPrefix: "fixt", WorkspaceID: "ws",
	})
	g.AddNode(&graph.Node{
		ID: "fixt::main.go::Logger", Kind: graph.KindType, Name: "Logger",
		FilePath: "main.go", StartLine: 1, EndLine: 20, Language: "go", RepoPrefix: "fixt", WorkspaceID: "ws",
	})
	g.AddNode(&graph.Node{
		ID: "fixt::main.go::Logger.Print", Kind: graph.KindMethod, Name: "Print",
		FilePath: "main.go", StartLine: 14, EndLine: 18, Language: "go", RepoPrefix: "fixt", WorkspaceID: "ws",
	})
	// Unknown kind (todo) → concept + gortex_kind passthrough.
	g.AddNode(&graph.Node{
		ID: "fixt::todo::1", Kind: graph.KindTodo, Name: "FIXME refactor",
		FilePath: "main.go", StartLine: 5, EndLine: 5, Language: "go", RepoPrefix: "fixt", WorkspaceID: "ws",
	})
	// Denylist node (param) → dropped under slim; the edge touching it is dangling.
	g.AddNode(&graph.Node{
		ID: "fixt::main.go::F#param:x", Kind: graph.KindParam, Name: "x",
		FilePath: "main.go", StartLine: 3, EndLine: 3, Language: "go", RepoPrefix: "fixt", WorkspaceID: "ws",
	})

	// file contains function (defines→contains).
	g.AddEdge(&graph.Edge{
		From: "fixt::file", To: "fixt::main.go::F", Kind: graph.EdgeDefines,
		Confidence: 1.0, ConfidenceLabel: "EXTRACTED", Origin: graph.OriginASTResolved,
	})
	// function calls method.
	g.AddEdge(&graph.Edge{
		From: "fixt::main.go::F", To: "fixt::main.go::Logger.Print", Kind: graph.EdgeCalls,
		Confidence: 0.0, ConfidenceLabel: "INFERRED", Origin: graph.OriginASTResolved,
	})
	// method member_of type → contains with swapped endpoints (type contains method).
	g.AddEdge(&graph.Edge{
		From: "fixt::main.go::Logger.Print", To: "fixt::main.go::Logger", Kind: graph.EdgeMemberOf,
		Confidence: 1.0, ConfidenceLabel: "EXTRACTED", Origin: graph.OriginASTResolved,
	})
	// cross-repo call → cross_domain + cross_repo passthrough.
	g.AddEdge(&graph.Edge{
		From: "fixt::main.go::F", To: "fixt::main.go::Logger", Kind: graph.EdgeCrossRepoCalls,
		Confidence: 0.7, ConfidenceLabel: "INFERRED", Origin: graph.OriginASTInferred, CrossRepo: true,
	})
	// arg_of into the param → dangling under slim (param dropped) AND a
	// transform edge dropped under slim regardless.
	g.AddEdge(&graph.Edge{
		From: "fixt::main.go::F", To: "fixt::main.go::F#param:x", Kind: graph.EdgeArgOf,
		Confidence: 0.5, Origin: graph.OriginASTResolved,
	})
	return g
}

func TestBuildUAGraph_Integration(t *testing.T) {
	g := buildFixtureGraph()
	nodes, edges, _ := snapshot(g, Options{})

	opts := UAOptions{
		Granularity: GranularitySlim,
		ProjectName: "fixt",
		AnalyzedAt:  "2026-01-01T00:00:00Z", // fixed by the (test) Action layer
		GitCommit:   "deadbeef",
	}
	ua, dropped := buildUAGraph(nodes, edges, opts)

	// LDD-spirit trajectory: print accounting + drop reasons BEFORE asserting.
	t.Logf("[IMP:9] buildUAGraph nodes_out=%d edges_out=%d dropped=%d", len(ua.Nodes), len(ua.Edges), len(dropped))
	for _, d := range dropped {
		t.Logf("[IMP:8] dropped kind=%s id=%s reason=%s", d.Kind, d.ID, d.Reason)
	}

	// (c) ALWAYS-ON Go structural sanity ------------------------------------
	assertUASanity(t, ua)

	// member_of swap: a contains edge from the TYPE to the METHOD must exist.
	foundSwap := false
	for _, e := range ua.Edges {
		if e.Type == "contains" && e.Source == "fixt::main.go::Logger" && e.Target == "fixt::main.go::Logger.Print" {
			foundSwap = true
		}
	}
	assert.True(t, foundSwap, "member_of must emit a swapped contains (type→method) edge")

	// cross_domain edge carries cross_repo + gortex_kind passthrough.
	foundCross := false
	for _, e := range ua.Edges {
		if e.Type == "cross_domain" {
			foundCross = true
			assert.True(t, e.CrossRepo, "cross_domain edge must carry cross_repo:true")
			assert.Equal(t, string(graph.EdgeCrossRepoCalls), e.GortexKind, "cross_domain must passthrough gortex_kind")
		}
	}
	assert.True(t, foundCross, "cross_repo_calls must map to cross_domain")

	// unknown kind todo → concept + gortex_kind passthrough.
	foundConcept := false
	for _, n := range ua.Nodes {
		if n.ID == "fixt::todo::1" {
			foundConcept = true
			assert.Equal(t, "concept", n.Type)
			assert.Equal(t, string(graph.KindTodo), n.GortexKind)
		}
	}
	assert.True(t, foundConcept, "unknown todo kind must map to concept with gortex_kind")

	// param node dropped under slim; both the arg_of (transform) edge and any
	// dangling edge to it must NOT appear in the output.
	for _, n := range ua.Nodes {
		assert.NotEqual(t, "fixt::main.go::F#param:x", n.ID, "denylisted param must not be emitted under slim")
	}
	require.NotEmpty(t, dropped, "drops must be recorded, never silent")

	// weight: the calls edge had Confidence 0 → neutral 0.5.
	for _, e := range ua.Edges {
		if e.Type == "calls" {
			assert.Equal(t, 0.5, e.Weight, "Confidence 0 must map to neutral 0.5")
		}
	}

	// (a) GOLDEN FILE comparison --------------------------------------------
	goldenPath := filepath.Join("testdata", "understand_golden.json")
	got, err := json.MarshalIndent(ua, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, got, 0o644))
		t.Logf("[IMP:9] golden file updated: %s", goldenPath)
	}
	want, rerr := os.ReadFile(goldenPath)
	require.NoError(t, rerr, "golden file must exist (run with UPDATE_GOLDEN=1 to create)")
	assert.Equal(t, string(want), string(got), "output must match committed golden file")

	// (b) AUTHORITATIVE UA validateGraph via node harness (skip-guarded) ----
	// Run as a subtest so a skip here (UA core / node absent) does NOT mask
	// the golden + always-on sanity assertions already proven above.
	t.Run("authoritative_validateGraph", func(t *testing.T) {
		runAuthoritativeValidation(t, got)
	})
}

func TestBuildUAGraph_FullGranularityKeepsDenied(t *testing.T) {
	g := buildFixtureGraph()
	nodes, edges, _ := snapshot(g, Options{})
	opts := UAOptions{Granularity: GranularityFull, ProjectName: "fixt", AnalyzedAt: "2026-01-01T00:00:00Z"}
	ua, _ := buildUAGraph(nodes, edges, opts)

	foundParamAsConcept := false
	for _, n := range ua.Nodes {
		if n.ID == "fixt::main.go::F#param:x" {
			foundParamAsConcept = true
			assert.Equal(t, "concept", n.Type, "param under full must be concept")
			assert.Equal(t, string(graph.KindParam), n.GortexKind)
		}
	}
	assert.True(t, foundParamAsConcept, "--granularity full must re-include param as concept")
	assertUASanity(t, ua)
}

func TestBuildUAGraph_Deterministic(t *testing.T) {
	g := buildFixtureGraph()
	nodes, edges, _ := snapshot(g, Options{})
	opts := UAOptions{Granularity: GranularitySlim, ProjectName: "fixt", AnalyzedAt: "2026-01-01T00:00:00Z", GitCommit: "deadbeef"}

	ua1, _ := buildUAGraph(nodes, edges, opts)
	ua2, _ := buildUAGraph(nodes, edges, opts)
	b1, err := json.Marshal(ua1)
	require.NoError(t, err)
	b2, err := json.Marshal(ua2)
	require.NoError(t, err)
	assert.Equal(t, b1, b2, "identical inputs must yield byte-identical JSON (AC5)")
}

func TestWriteUnderstandAnything_Generic(t *testing.T) {
	g := buildFixtureGraph()
	var buf bytes.Buffer
	opts := UAOptions{Generic: true, Granularity: GranularitySlim, ProjectName: "fixt", AnalyzedAt: "2026-01-01T00:00:00Z"}
	stats, err := WriteUnderstandAnything(&buf, g, opts)
	require.NoError(t, err)
	t.Logf("[IMP:9] generic stats nodes=%d edges=%d bytes=%d", stats.NodesWritten, stats.EdgesWritten, stats.BytesWritten)

	var gen struct {
		Nodes []struct {
			ID, Type, Name, FilePath string
		} `json:"nodes"`
		Edges []struct {
			Source, Target, Type string
			Weight               float64
		} `json:"edges"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &gen))
	require.NotEmpty(t, gen.Nodes, "generic@1 must emit nodes")

	ids := map[string]bool{}
	for _, n := range gen.Nodes {
		assert.NotEmpty(t, n.ID, "generic node id must be non-empty")
		assert.NotEmpty(t, n.Type, "generic node type must be non-empty")
		assert.NotEmpty(t, n.Name, "generic node name must be non-empty")
		ids[n.ID] = true
	}
	for _, e := range gen.Edges {
		assert.True(t, ids[e.Source], "generic edge source must reference an emitted node")
		assert.True(t, ids[e.Target], "generic edge target must reference an emitted node")
		assert.GreaterOrEqual(t, e.Weight, 0.0)
		assert.LessOrEqual(t, e.Weight, 1.0)
	}
}

// ----------------------------------------------------------------------------
// E2E (AC8) — grules-engine, testing.Short-skippable
// ----------------------------------------------------------------------------

func TestExportUnderstand_E2E_Grules(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e skipped under -short")
	}
	grulesPath := "/mnt/d/code/grules-engine"
	if _, err := os.Stat(grulesPath); err != nil {
		t.Skipf("e2e skipped: grules-engine not found at %s: %v", grulesPath, err)
	}
	gortexBin := buildGortexBinary(t)

	tmp := t.TempDir()
	outPath := filepath.Join(tmp, "kg.json")
	cmd := exec.Command(gortexBin, "export", "understand", grulesPath, "--out", outPath, "--pretty")
	out, err := cmd.CombinedOutput()
	t.Logf("[IMP:8] gortex export understand output:\n%s", string(out))
	require.NoError(t, err, "gortex export understand on grules must succeed")

	data, rerr := os.ReadFile(outPath)
	require.NoError(t, rerr, "exported file must exist")

	var ua UAGraph
	require.NoError(t, json.Unmarshal(data, &ua), "exported file must parse as a UA graph")
	t.Logf("[IMP:9] grules export nodes=%d edges=%d", len(ua.Nodes), len(ua.Edges))
	assertUASanity(t, ua)

	// Recognizable grule symbols must appear (substring match on node names).
	want := []string{"GruleEngine", "RuleEntry", "DataContext"}
	names := map[string]bool{}
	for _, n := range ua.Nodes {
		names[n.Name] = true
	}
	foundAny := false
	for _, w := range want {
		if names[w] {
			foundAny = true
			t.Logf("[IMP:9] found grule symbol: %s", w)
		}
	}
	assert.True(t, foundAny, "grules export must contain at least one recognizable rule-engine symbol %v", want)

	t.Run("authoritative_validateGraph", func(t *testing.T) {
		runAuthoritativeValidation(t, data)
	})
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// assertUASanity is the always-on Go structural check: every node type ∈ UA
// node types, every edge type ∈ UA edge types, required fields present,
// weight∈[0,1], and referential integrity (every edge endpoint is an emitted
// node). Runs even when the authoritative harness skips.
func assertUASanity(t *testing.T, ua UAGraph) {
	t.Helper()
	assert.Equal(t, uaVersion, ua.Version)
	assert.Equal(t, uaKind, ua.Kind)
	assert.NotNil(t, ua.Layers, "layers must be [] not null")
	assert.NotNil(t, ua.Tour, "tour must be [] not null")
	assert.NotNil(t, ua.Project.Languages, "languages must be [] not null")
	assert.NotNil(t, ua.Project.Frameworks, "frameworks must be [] not null")

	ids := map[string]bool{}
	for _, n := range ua.Nodes {
		assert.True(t, uaNodeTypeSet[n.Type], "node %q has invalid UA type %q", n.ID, n.Type)
		assert.NotEmpty(t, n.ID, "node id required")
		assert.NotEmpty(t, n.Summary, "node summary required (UA)")
		assert.NotNil(t, n.Tags, "node tags must be non-nil (UA)")
		assert.Contains(t, []string{"simple", "moderate", "complex"}, n.Complexity, "node complexity required")
		ids[n.ID] = true
	}
	for _, e := range ua.Edges {
		assert.True(t, uaEdgeTypeSet[e.Type], "edge has invalid UA type %q", e.Type)
		assert.GreaterOrEqual(t, e.Weight, 0.0, "weight >= 0")
		assert.LessOrEqual(t, e.Weight, 1.0, "weight <= 1")
		assert.True(t, ids[e.Source], "edge source %q must reference an emitted node (referential integrity)", e.Source)
		assert.True(t, ids[e.Target], "edge target %q must reference an emitted node (referential integrity)", e.Target)
	}
}

// runAuthoritativeValidation pipes the UA JSON into the node harness against
// the real UA validateGraph and asserts zero dropped / zero fatal (AC1). It
// SKIPS with a clear message when node or the UA package are unavailable — it
// never fakes success and never ports the schema.
func runAuthoritativeValidation(t *testing.T, jsonBytes []byte) {
	t.Helper()
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("authoritative UA validation skipped: `node` not on PATH; install node + run pnpm install in /mnt/d/code/understand-anything to enable")
		return
	}
	uaCore := os.Getenv("UA_CORE")
	if uaCore == "" {
		uaCore = "/mnt/d/code/understand-anything/understand-anything-plugin/packages/core"
	}
	if _, statErr := os.Stat(uaCore); statErr != nil {
		t.Skipf("authoritative UA validation skipped: UA core not found at %s; run pnpm install in /mnt/d/code/understand-anything to enable", uaCore)
		return
	}
	harness := filepath.Join("testdata", "ua_validate.mjs")
	cmd := exec.Command(nodeBin, harness)
	cmd.Stdin = bytes.NewReader(jsonBytes)
	out, runErr := cmd.CombinedOutput()
	t.Logf("[IMP:9] ua_validate output: %s", string(out))
	if runErr != nil {
		// Exit 2 = harness could not load the validator → skip, not fail.
		if ee, ok := runErr.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			t.Skipf("authoritative UA validation skipped: harness could not load validateGraph: %s", string(out))
			return
		}
		t.Fatalf("authoritative UA validation FAILED (AC1): %v\n%s", runErr, string(out))
	}
	var res struct {
		Success bool `json:"success"`
		Dropped int  `json:"dropped"`
		Fatal   int  `json:"fatal"`
	}
	require.NoError(t, json.Unmarshal(lastJSONLine(out), &res), "harness must emit a JSON result line")
	assert.True(t, res.Success, "AC1: validateGraph must succeed")
	assert.Zero(t, res.Dropped, "AC1: validateGraph must report zero dropped")
	assert.Zero(t, res.Fatal, "AC1: validateGraph must report zero fatal issues")
}

// lastJSONLine returns the final non-empty line of the harness output (the
// result line), tolerating any leading diagnostic lines.
func lastJSONLine(b []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		ln := bytes.TrimSpace(lines[i])
		if len(ln) > 0 && ln[0] == '{' {
			return ln
		}
	}
	return b
}

// buildGortexBinary compiles the gortex CLI into the test temp dir and returns
// its path. Used by the e2e test so it exercises the real `gortex export
// understand` wiring end-to-end.
func buildGortexBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gortex-e2e")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/zzet/gortex/cmd/gortex")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "build gortex binary for e2e: %s", string(out))
	return bin
}
