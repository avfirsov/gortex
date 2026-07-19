package resolver

import (
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// edgeCensusStore builds the cold-census fixture shape: nodes ride the light
// projection, call edges land in the backing store so the EdgeCalls census
// sees them.
func edgeCensusStore(nodes []*graph.Node, callEdges []*graph.Edge) *countingFrameworkLightStore {
	base := graph.New()
	for _, e := range callEdges {
		base.AddEdge(e)
	}
	return &countingFrameworkLightStore{Store: base, nodes: nodes}
}

// One-way-gate safety for the EdgeCalls census preflights: with the pass's
// own admission evidence absent the pass must be skipped, and with it
// present the pass must run. Each fixture satisfies the pass's family and
// node gates so only the edge preflight decides the outcome.
func TestFrameworkSynthEdgePreflights(t *testing.T) {
	t.Parallel()
	webNode := &graph.Node{ID: "app.ts::f", Kind: graph.KindFunction, Name: "f", Language: "typescript"}
	tests := []struct {
		name       string
		synth      string
		nodes      []*graph.Node
		presentVia []*graph.Edge
		// absentVia, when set, is an edge that must NOT satisfy the gate
		// (a near-miss on the pass's admission predicate).
		absentVia []*graph.Edge
	}{
		{
			name:  "object-registry",
			synth: SynthObjectRegistry,
			nodes: []*graph.Node{webNode},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.AddCommand", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": objectRegistryVia, "registry_value": "AddCommand"}}},
			absentVia: []*graph.Edge{{From: "a", To: "unresolved::*.AddCommand", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": objectRegistryVia}}},
		},
		{
			name:  "ngrx-effect",
			synth: SynthNgRxEffect,
			nodes: []*graph.Node{webNode},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.loadUsers", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": ngrxEffectVia, "ngrx_action": "loadUsers"}}},
		},
		{
			name:  "express-resolve",
			synth: SynthExpressResolve,
			nodes: []*graph.Node{webNode},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.list", Kind: graph.EdgeCalls,
				Meta: map[string]any{"express_handler_ref": true, "express_ref_name": "auth"}}},
			// The pass consumes only unresolved-target placeholders, so a
			// resolved edge carrying the key must not admit it.
			absentVia: []*graph.Edge{{From: "a", To: "app.ts::list", Kind: graph.EdgeCalls,
				Meta: map[string]any{"express_handler_ref": true}}},
		},
		{
			name:  "redux-thunk",
			synth: SynthReduxThunk,
			nodes: []*graph.Node{webNode},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.setLoading", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": reduxThunkVia, "thunk_dispatch": "setLoading"}}},
		},
		{
			name:  "laravel-event",
			synth: SynthLaravelEvent,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Name: "handle", Language: "php"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.OrderShipped", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": laravelEventVia, "laravel_event_type": "OrderShipped"}}},
		},
		{
			name:  "vuex-dispatch",
			synth: SynthVuexDispatch,
			nodes: []*graph.Node{webNode},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.login", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": vuexDispatchVia, "vuex_key": "user/login", "vuex_kind": "action"}}},
		},
		{
			name:  "rtk-query",
			synth: SynthRTKQuery,
			nodes: []*graph.Node{webNode},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.getUser", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": rtkQueryVia, "rtk_endpoint": "getUser"}}},
		},
		{
			name:  "celery-dispatch",
			synth: SynthCelery,
			nodes: []*graph.Node{{ID: "tasks.py::send_email", Kind: graph.KindFunction, Name: "send_email", Language: "python"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.send_email", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": celeryVia, "celery_task": "send_email"}}},
		},
		{
			name:  "spring-event",
			synth: SynthSpringEvent,
			nodes: []*graph.Node{{ID: "App.java::publish", Kind: graph.KindMethod, Name: "publish", Language: "java"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.OrderPlaced", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": springEventVia, "spring_event_type": "OrderPlaced"}}},
		},
		{
			name:  "mediatr-dispatch",
			synth: SynthMediatR,
			nodes: []*graph.Node{{ID: "App.cs::Place", Kind: graph.KindMethod, Name: "Place", Language: "csharp"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.Handle", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": mediatrVia, "mediatr_request_type": "CreateOrder", "mediatr_kind": "request"}}},
		},
		{
			name:       "react-setstate",
			synth:      SynthReactSetState,
			nodes:      []*graph.Node{{ID: "c.jsx::render", Kind: graph.KindMethod, Name: "render"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.setState", Kind: graph.EdgeCalls}},
		},
		{
			name:       "flutter-setstate",
			synth:      SynthFlutterSetState,
			nodes:      []*graph.Node{{ID: "w.dart::build", Kind: graph.KindMethod, Name: "build"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::setState", Kind: graph.EdgeCalls}},
		},
		{
			name:  "rails-resolve",
			synth: SynthRailsResolve,
			nodes: []*graph.Node{{ID: "app.rb::run", Kind: graph.KindMethod, Name: "run", Language: "ruby"}},
			presentVia: []*graph.Edge{{From: "a", To: "unresolved::*.perform", Kind: graph.EdgeCalls,
				Meta: map[string]any{"recv_const": "UserService"}}},
			// The pass touches only unresolved targets; recv_const on a
			// resolved call proves nothing.
			absentVia: []*graph.Edge{{From: "a", To: "app.rb::perform", Kind: graph.EdgeCalls,
				Meta: map[string]any{"recv_const": "UserService"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synth := synthFunc{name: tt.synth, fn: func(graph.Store) int { return 0 }}

			skip := summarizeFrameworkCandidates(edgeCensusStore(tt.nodes, tt.absentVia), nil)
			if shouldRunFrameworkSynthesizer(synth, nil, skip) {
				t.Fatalf("%q must be skipped without its admission edge", tt.synth)
			}

			run := summarizeFrameworkCandidates(edgeCensusStore(tt.nodes, tt.presentVia), nil)
			if !shouldRunFrameworkSynthesizer(synth, nil, run) {
				t.Fatalf("%q must run when its admission edge is present", tt.synth)
			}
		})
	}
}

// A scoped synthesizer view can admit incident placeholder edges the scoped
// census stream would not see, so scoped runs never consult the edge
// census: the pre-existing scoped admission behavior is preserved bit for
// bit even when no via edge is in scope.
func TestFrameworkSynthEdgePreflightsSkippedOnScopedRuns(t *testing.T) {
	t.Parallel()
	nodes := []*graph.Node{
		{ID: "a::handle", Kind: graph.KindMethod, Name: "handle", Language: "php", RepoPrefix: "a"},
	}
	store := edgeCensusStore(nodes, nil)
	scope := map[string]bool{"a": true}
	summary := summarizeFrameworkCandidates(store, scope)
	require.False(t, summary.edges.valid, "a scoped census must not claim the full edge stream")
	synth := synthFunc{name: SynthLaravelEvent, fn: func(graph.Store) int { return 0 }}
	assert.True(t, shouldRunFrameworkSynthesizer(synth, scope, summary),
		"scoped admission must not consult the edge census")

	cold := summarizeFrameworkCandidates(store, nil)
	require.True(t, cold.edges.valid)
	assert.False(t, shouldRunFrameworkSynthesizer(synth, nil, cold),
		"cold admission requires the dispatch placeholder")
}

func TestFrameworkFamilyGateNeeded(t *testing.T) {
	t.Parallel()
	summarize := func(nodes ...*graph.Node) frameworkCandidateSummary {
		return summarizeFrameworkCandidates(edgeCensusStore(nodes, nil), nil)
	}
	razor := &graph.Node{ID: "p", Kind: graph.KindType, Name: "Page", Language: "razor"}
	ts := &graph.Node{ID: "c", Kind: graph.KindType, Name: "Counter", Language: "typescript"}
	vue := &graph.Node{ID: "v", Kind: graph.KindType, Name: "App", Language: "vue"}

	assert.False(t, frameworkFamilyGateNeeded(nil, summarize(ts)),
		"one strict family cannot produce a cross-family drop")
	assert.True(t, frameworkFamilyGateNeeded(nil, summarize(razor, ts)))
	// The framework census counts vue as web, but the gate's own strict
	// languageFamily does not — a vue+razor graph cannot drop an edge.
	vueSummary := summarize(vue, razor)
	require.Positive(t, vueSummary.all["web"])
	assert.False(t, frameworkFamilyGateNeeded(nil, vueSummary))
	// Scoped runs always run the gate: off-scope endpoints are uncensused.
	// The summary carries its own coverage marker, so it is built with the
	// same scope the production call site passes.
	scoped := map[string]bool{"a": true}
	scopedSummary := summarizeFrameworkCandidates(edgeCensusStore([]*graph.Node{ts}, nil), scoped)
	assert.True(t, frameworkFamilyGateNeeded(scoped, scopedSummary))
	// Under the daemon's full-coverage attestation the same non-nil scope
	// yields a full census, and the gate may again prove itself unnecessary.
	attested := summarizeFrameworkCandidatesCensus(edgeCensusStore([]*graph.Node{ts}, nil), scoped, nil, true)
	assert.False(t, frameworkFamilyGateNeeded(scoped, attested),
		"a full-coverage census proves a one-family graph cannot drop")
}

// The cold engine still drops a cross-strict-family synthesized reference:
// the two-family census admits the gate and the edge lands in Gated.
func TestRunFrameworkSynthesizersColdStillAppliesFamilyGate(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "p::Page", Kind: graph.KindType, Name: "Page", Language: "razor", RepoPrefix: "p"},
		{ID: "p::Counter", Kind: graph.KindType, Name: "Counter", Language: "typescript", RepoPrefix: "p"},
	}, []*graph.Edge{{From: "p::Page", To: "p::Counter", Kind: graph.EdgeReferences,
		Meta: map[string]any{MetaSynthesizedBy: SynthRustScope}}})
	rep := RunFrameworkSynthesizers(g)
	assert.Equal(t, 1, rep.Gated)
	assert.Empty(t, g.GetOutEdges("p::Page"))
}

func TestFrameworkReceiverGateNeeded(t *testing.T) {
	t.Parallel()
	summarize := func(nodes ...*graph.Node) frameworkCandidateSummary {
		return summarizeFrameworkCandidates(edgeCensusStore(nodes, nil), nil)
	}
	recv := &graph.Node{ID: "r", Kind: graph.KindType, Name: "Receiver", Language: "csharp"}
	other := &graph.Node{ID: "o", Kind: graph.KindInterface, Name: "Other", Language: "csharp"}
	recvDup := &graph.Node{ID: "r2", Kind: graph.KindType, Name: "Receiver", Language: "csharp"}
	goType := &graph.Node{ID: "g", Kind: graph.KindType, Name: "Other", Language: "go"}

	assert.False(t, frameworkReceiverGateNeeded(nil, summarize()))
	assert.False(t, frameworkReceiverGateNeeded(nil, summarize(recv)),
		"a single indexed type name cannot mismatch itself")
	assert.False(t, frameworkReceiverGateNeeded(nil, summarize(recv, recvDup)),
		"same-named types are one name")
	assert.False(t, frameworkReceiverGateNeeded(nil, summarize(recv, goType)),
		"only csharp type names feed the demote index")
	assert.True(t, frameworkReceiverGateNeeded(nil, summarize(recv, other)))
	scoped := map[string]bool{"a": true}
	scopedSummary := summarizeFrameworkCandidates(edgeCensusStore(nil, nil), scoped)
	assert.True(t, frameworkReceiverGateNeeded(scoped, scopedSummary))
	// Full-coverage attestation: the C# name census fills from the raw
	// stream even under a non-nil scope, so the gate can prove itself
	// unnecessary again.
	attested := summarizeFrameworkCandidatesCensus(edgeCensusStore(nil, nil), scoped, nil, true)
	assert.False(t, frameworkReceiverGateNeeded(scoped, attested))
}

// The cold engine still demotes a receiver-type misattribution when two
// distinct C# type names are indexed.
func TestRunFrameworkSynthesizersColdStillAppliesReceiverGate(t *testing.T) {
	g := graph.New()
	caller, target := addReceiverGateRepo(g, "changed")
	rep := RunFrameworkSynthesizers(g)
	assert.Equal(t, 1, rep.ReceiverGated)
	require.True(t, findCallEdge(g, caller, target).IsSpeculative())
}

// A claiming resolver whose declared target vocabulary has no indexed node
// cannot claim anything; the tail must return before collecting a single
// unresolved edge.
func TestRunClaimingResolversSkipsWithoutRequiredVocabulary(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "changed::QuerySet.iterator", Kind: graph.KindMethod, Name: "iterator",
		FilePath: "changed/query.py", Language: "python", RepoPrefix: "changed",
		Meta: map[string]any{"receiver": "QuerySet"}})
	g.AddEdge(&graph.Edge{From: "changed::QuerySet.iterator", To: "unresolved::*._iterable_class",
		Kind: graph.EdgeCalls, FilePath: "changed/query.py"})

	counting := &frameworkTailCountingStore{Store: g}
	claimed := RunClaimingResolversScoped(counting, map[string]bool{"changed": true})
	assert.Empty(t, claimed)
	assert.Equal(t, 1, counting.findByNames, "admission probe is one indexed lookup")
	assert.Zero(t, counting.repoEdgesByKinds, "no admissible resolver, no edge scan")

	assert.Empty(t, RunClaimingResolvers(counting), "cold form takes the same probe")
}

// registryScanCountingStore counts whole-graph kind scans so the reorder in
// ResolveObjectRegistryCalls is observable: without placeholders the pass
// must pay one EdgeCalls scan and build no index.
type registryScanCountingStore struct {
	graph.Store
	edgeScans map[graph.EdgeKind]int
	nodeScans int
}

func (s *registryScanCountingStore) EdgesByKind(kind graph.EdgeKind) iter.Seq[*graph.Edge] {
	if s.edgeScans == nil {
		s.edgeScans = map[graph.EdgeKind]int{}
	}
	s.edgeScans[kind]++
	return s.Store.EdgesByKind(kind)
}

func (s *registryScanCountingStore) NodesByKind(kind graph.NodeKind) iter.Seq[*graph.Node] {
	s.nodeScans++
	return s.Store.NodesByKind(kind)
}

func TestResolveObjectRegistryCallsCollectsPlaceholdersBeforeIndexes(t *testing.T) {
	empty := graph.New()
	registryMethod(empty, "bus.js::AddCommand", "bus.js::AddCommand.execute", "bus.js", "execute")
	counting := &registryScanCountingStore{Store: empty}
	require.Zero(t, ResolveObjectRegistryCalls(counting))
	assert.Equal(t, 1, counting.edgeScans[graph.EdgeCalls], "placeholder probe only")
	assert.Zero(t, counting.edgeScans[graph.EdgeMemberOf], "no member index without placeholders")
	assert.Zero(t, counting.nodeScans, "no method index without placeholders")

	// With a placeholder present the pass runs in full through the same
	// wrapper and lands the dispatch on the handler method.
	populated := graph.New()
	registryMethod(populated, "bus.js::AddCommand", "bus.js::AddCommand.execute", "bus.js", "execute")
	registryDispatch(populated, "bus.js::Bus.run", "bus.js", "AddCommand", "execute", 9)
	require.Equal(t, 1, ResolveObjectRegistryCalls(&registryScanCountingStore{Store: populated}))
	require.NotNil(t, synthRegistryEdge(populated, "bus.js::Bus.run", "bus.js::AddCommand.execute"))
}

// Pins the extractor invariant the rtk-query edge preflight leans on: every
// rtk_generated_hook node is minted together with a via=rtk-query
// placeholder EdgeCalls in the same extraction, so a graph with no such via
// edge holds no generated hooks and both resolver branches are inert.
func TestRTKQueryExtractorMintsHookWithViaPlaceholder(t *testing.T) {
	src := `import { createApi } from '@reduxjs/toolkit/query/react'
export const api = createApi({
  endpoints: (builder) => ({
    getUser: builder.query({ query: (id) => 'user/' + id }),
    addUser: builder.mutation({ query: (body) => ({ url: 'user', body }) }),
  }),
})
`
	r, err := languages.NewTypeScriptExtractor().Extract("api.ts", []byte(src))
	require.NoError(t, err)

	hooks := 0
	for _, n := range r.Nodes {
		if n == nil || n.Meta == nil {
			continue
		}
		if gen, _ := n.Meta["rtk_generated_hook"].(bool); !gen {
			continue
		}
		hooks++
		found := false
		for _, e := range r.Edges {
			if e == nil || e.From != n.ID || e.Kind != graph.EdgeCalls || e.Meta == nil {
				continue
			}
			if via, _ := e.Meta["via"].(string); via == rtkQueryVia {
				found = true
				break
			}
		}
		require.True(t, found, "generated hook %s minted without its via=%s placeholder", n.ID, rtkQueryVia)
	}
	require.Equal(t, 2, hooks, "fixture must mint one hook per endpoint")
}
