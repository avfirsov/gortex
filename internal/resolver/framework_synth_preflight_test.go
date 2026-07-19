package resolver

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type countingFrameworkLightStore struct {
	graph.Store
	nodes     []*graph.Node
	edges     []*graph.Edge
	nodeCalls int
	edgeCalls int
}

func (s *countingFrameworkLightStore) AllNodesLight() []*graph.Node {
	s.nodeCalls++
	return s.nodes
}

func (s *countingFrameworkLightStore) AllEdgesLight(...graph.EdgeKind) []*graph.Edge {
	s.edgeCalls++
	return s.edges
}

func TestFrameworkLanguageFamily(t *testing.T) {
	t.Parallel()
	tests := []struct {
		language string
		want     string
	}{
		{language: "Go", want: "go"},
		{language: "golang", want: "go"},
		{language: "TypeScript", want: "web"},
		{language: "Vue", want: "web"},
		{language: "Kotlin", want: "jvm"},
		{language: "Objective-C++", want: "apple"},
		{language: "C++", want: "c"},
		{language: "C#", want: "dotnet"},
		{language: "object-pascal", want: "pascal"},
		{language: "SQL", want: "sql"},
		{language: "unknown", want: ""},
		{language: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			t.Parallel()
			if got := frameworkLanguageFamily(tt.language); got != tt.want {
				t.Fatalf("frameworkLanguageFamily(%q) = %q, want %q", tt.language, got, tt.want)
			}
		})
	}
}

func TestFrameworkSynthLanguageFamiliesAreConservativeAndRegistered(t *testing.T) {
	t.Parallel()
	registered := map[string]bool{}
	for _, synth := range defaultFrameworkSynthesizers() {
		if registered[synth.Name()] {
			t.Fatalf("duplicate registered synthesizer %q", synth.Name())
		}
		registered[synth.Name()] = true
	}
	for name, families := range frameworkSynthLanguageFamilies {
		if !registered[name] {
			t.Errorf("language gate names unregistered synthesizer %q", name)
		}
		if len(families) == 0 {
			t.Errorf("language gate for %q has no families", name)
		}
		seen := map[string]bool{}
		for _, family := range families {
			if family == "" || seen[family] {
				t.Errorf("language gate for %q has invalid or duplicate family %q", name, family)
			}
			seen[family] = true
		}
	}
	for name := range frameworkSynthNodePreflights {
		if !registered[name] {
			t.Errorf("node preflight names unregistered synthesizer %q", name)
		}
	}

	// These passes intentionally remain language-ungated. Their candidates are
	// generic, span many runtime languages, or come from edge metadata rather
	// than a bounded language frontend. Some have independent node preflights.
	ambiguous := []string{
		SynthGRPCStub,
		SynthTemporalStub,
		SynthEventChannel,
		SynthObserverChannel,
		SynthReactSetState,
		SynthFlutterSetState,
		SynthFactoryChain,
		SynthFnValue,
		SynthValueRefName,
	}
	for _, name := range ambiguous {
		if _, gated := frameworkSynthLanguageFamilies[name]; gated {
			t.Errorf("generic/ambiguous synthesizer %q must not be language-gated", name)
		}
	}
}

func TestFrameworkSynthPreflightPreservesScopedFallbackSemantics(t *testing.T) {
	t.Parallel()
	nodes := []*graph.Node{
		{ID: "go-a", Kind: graph.KindFunction, Language: "go", RepoPrefix: "a"},
		{ID: "web-b", Kind: graph.KindMethod, Name: "render", Language: "typescript", RepoPrefix: "b"},
	}
	store := &countingFrameworkLightStore{Store: graph.New(), nodes: nodes}
	scope := map[string]bool{"a": true}
	summary := summarizeFrameworkCandidates(store, scope)
	if store.nodeCalls != 1 {
		t.Fatalf("light-node scans = %d, want 1", store.nodeCalls)
	}

	tests := []struct {
		name  string
		synth FrameworkSynthesizer
		want  bool
	}{
		{
			name:  "mapped legacy pass is bounded to scope",
			synth: synthFunc{name: SynthStoreFactory, fn: func(graph.Store) int { return 0 }},
			want:  false,
		},
		{
			name: "mapped scoped pass ignores language outside scope",
			synth: synthFunc{
				name:     SynthStoreFactory,
				fn:       func(graph.Store) int { return 0 },
				scopedFn: func(graph.Store, map[string]bool) int { return 0 },
			},
			want: false,
		},
		{
			name:  "mapped family present in scope",
			synth: synthFunc{name: SynthGinMiddleware, fn: func(graph.Store) int { return 0 }, scopedFn: func(graph.Store, map[string]bool) int { return 0 }},
			want:  true,
		},
		{
			name:  "unmapped generic pass always runs",
			synth: synthFunc{name: SynthTemporalStub, fn: func(graph.Store) int { return 0 }, scopedFn: func(graph.Store, map[string]bool) int { return 0 }},
			want:  true,
		},
		{
			name:  "node candidate outside scope does not enable legacy pass",
			synth: synthFunc{name: SynthReactSetState, fn: func(graph.Store) int { return 0 }},
			want:  false,
		},
		{
			name: "node candidate outside scope does not enable scoped pass",
			synth: synthFunc{
				name:     SynthReactSetState,
				fn:       func(graph.Store) int { return 0 },
				scopedFn: func(graph.Store, map[string]bool) int { return 0 },
			},
			want: false,
		},
		{
			name:  "missing required node candidate skips generic pass",
			synth: synthFunc{name: SynthFlutterSetState, fn: func(graph.Store) int { return 0 }},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldRunFrameworkSynthesizer(tt.synth, scope, summary); got != tt.want {
				t.Fatalf("shouldRunFrameworkSynthesizer(%q) = %v, want %v", tt.synth.Name(), got, tt.want)
			}
		})
	}
}

func TestRecordFrameworkNodeCandidates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		node   *graph.Node
		family string
		want   string
	}{
		{name: "generic render method", node: &graph.Node{Kind: graph.KindMethod, Name: "render"}, want: SynthReactSetState},
		{name: "generic build method", node: &graph.Node{Kind: graph.KindMethod, Name: "build"}, want: SynthFlutterSetState},
		{name: "swiftui suffix", node: &graph.Node{Kind: graph.KindType, Name: "ProfileView"}, family: "apple", want: SynthSwiftUIResolve},
		{name: "uikit suffix", node: &graph.Node{Kind: graph.KindType, Name: "ProfileViewController"}, family: "apple", want: SynthUIKitResolve},
		{name: "vapor suffix", node: &graph.Node{Kind: graph.KindType, Name: "UserController"}, family: "apple", want: SynthVaporResolve},
		{name: "swift model directory", node: &graph.Node{Kind: graph.KindType, Name: "User", FilePath: "Sources/App/Models/User.swift"}, family: "apple", want: SynthSwiftUIResolve},
		{name: "event topic", node: &graph.Node{ID: "event::pubsub::in-process::saved", Kind: graph.KindEvent}, want: SynthEventChannel},
		{name: "observer registrar", node: &graph.Node{Kind: graph.KindMethod, Name: "addListener"}, want: frameworkMarkerObserverRegistrar},
		{name: "observer dispatcher", node: &graph.Node{Kind: graph.KindFunction, Name: "notifyObservers"}, want: frameworkMarkerObserverDispatcher},
		{name: "mybatis statement", node: &graph.Node{Kind: graph.KindMethod, Name: "findUser", Language: "mybatis"}, want: SynthMyBatis},
		{name: "sidekiq worker", node: &graph.Node{Kind: graph.KindMethod, Name: "perform", Language: "ruby"}, want: SynthSidekiq},
		{name: "laravel listener", node: &graph.Node{Kind: graph.KindMethod, Name: "handle", Language: "php"}, want: SynthLaravelEvent},
		{name: "swift bridge side", node: &graph.Node{Kind: graph.KindMethod, Name: "save", Language: "swift"}, want: frameworkMarkerSwift},
		{name: "objc bridge side", node: &graph.Node{Kind: graph.KindMethod, Name: "save:", Language: "objc"}, want: frameworkMarkerObjC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			markers := map[string]int{}
			recordFrameworkNodeCandidates(markers, tt.node, tt.family)
			if markers[tt.want] == 0 {
				t.Fatalf("candidate marker %q missing: %#v", tt.want, markers)
			}
		})
	}
}

func TestFrameworkSynthPreflightRequiresEveryNecessaryMarker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		synth string
		nodes []*graph.Node
		edges []*graph.Edge
		// callEdges are added to the backing store so the cold EdgeCalls
		// census sees them.
		callEdges []*graph.Edge
		want      bool
	}{
		{
			name:  "observer registrar alone is insufficient",
			synth: SynthObserverChannel,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Name: "addListener"}},
			want:  false,
		},
		{
			name:  "observer registrar and dispatcher",
			synth: SynthObserverChannel,
			nodes: []*graph.Node{
				{ID: "register", Kind: graph.KindMethod, Name: "addListener"},
				{ID: "dispatch", Kind: graph.KindMethod, Name: "notifyObservers"},
			},
			edges: []*graph.Edge{
				{From: "register", To: "listeners", Kind: graph.EdgeAccessesField},
				{From: "dispatch", To: "listeners", Kind: graph.EdgeAccessesField},
			},
			want: true,
		},
		{
			name:  "observer methods on different fields are insufficient",
			synth: SynthObserverChannel,
			nodes: []*graph.Node{
				{ID: "register", Kind: graph.KindMethod, Name: "addListener"},
				{ID: "dispatch", Kind: graph.KindMethod, Name: "notifyObservers"},
			},
			edges: []*graph.Edge{
				{From: "register", To: "listeners-a", Kind: graph.EdgeAccessesField},
				{From: "dispatch", To: "listeners-b", Kind: graph.EdgeAccessesField},
			},
			want: false,
		},
		{
			name:  "event topic enables event channel",
			synth: SynthEventChannel,
			nodes: []*graph.Node{{ID: "event::emitter::bus::saved", Kind: graph.KindEvent}},
			want:  true,
		},
		{
			name:  "swift alone is insufficient for objc bridge",
			synth: SynthSwiftObjC,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Language: "swift"}},
			want:  false,
		},
		{
			name:  "swift and objc enable bridge",
			synth: SynthSwiftObjC,
			nodes: []*graph.Node{
				{Kind: graph.KindMethod, Language: "swift"},
				{Kind: graph.KindMethod, Language: "objc"},
			},
			want: true,
		},
		{
			name:  "mybatis file without statement is insufficient",
			synth: SynthMyBatis,
			nodes: []*graph.Node{{Kind: graph.KindFile, Language: "mybatis"}, {Kind: graph.KindMethod, Language: "java"}},
			want:  false,
		},
		{
			name:  "mybatis statement and java domain enable mapper join",
			synth: SynthMyBatis,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Language: "mybatis"}, {Kind: graph.KindMethod, Language: "java"}},
			want:  true,
		},
		{
			name:  "sidekiq perform enables dispatch",
			synth: SynthSidekiq,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Name: "perform", Language: "ruby"}},
			want:  true,
		},
		{
			name:  "laravel handle plus dispatch placeholder enable event dispatch",
			synth: SynthLaravelEvent,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Name: "handle", Language: "php"}},
			callEdges: []*graph.Edge{{
				From: "app.php::fire", To: "unresolved::*.OrderShipped", Kind: graph.EdgeCalls,
				Meta: map[string]any{"via": laravelEventVia, "laravel_event_type": "OrderShipped"},
			}},
			want: true,
		},
		{
			name:  "laravel handle without a dispatch placeholder is insufficient",
			synth: SynthLaravelEvent,
			nodes: []*graph.Node{{Kind: graph.KindMethod, Name: "handle", Language: "php"}},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := graph.New()
			for _, e := range tt.callEdges {
				base.AddEdge(e)
			}
			store := &countingFrameworkLightStore{Store: base, nodes: tt.nodes, edges: tt.edges}
			summary := summarizeFrameworkCandidates(store, nil)
			synth := synthFunc{name: tt.synth, fn: func(graph.Store) int { return 0 }}
			if got := shouldRunFrameworkSynthesizer(synth, nil, summary); got != tt.want {
				t.Fatalf("shouldRunFrameworkSynthesizer(%q) = %v, want %v; markers=%v", tt.synth, got, tt.want, summary.allMarkers)
			}
		})
	}
}

func TestRunFrameworkSynthesizersUsesOneLightNodeScan(t *testing.T) {
	base := graph.New()
	base.AddNode(&graph.Node{ID: "go-node", Kind: graph.KindFunction, Name: "f", Language: "go", RepoPrefix: "repo"})
	store := &countingFrameworkLightStore{
		Store: base,
		nodes: []*graph.Node{
			{ID: "go-node", Kind: graph.KindFunction, Name: "f", Language: "go", RepoPrefix: "repo"},
			{ID: "register", Kind: graph.KindMethod, Name: "addListener", RepoPrefix: "repo"},
			{ID: "dispatch", Kind: graph.KindMethod, Name: "notifyObservers", RepoPrefix: "repo"},
		},
		edges: []*graph.Edge{
			{From: "register", To: "listeners", Kind: graph.EdgeAccessesField},
			{From: "dispatch", To: "listeners", Kind: graph.EdgeAccessesField},
		},
	}
	report := RunFrameworkSynthesizersScoped(store, map[string]bool{"repo": true})
	if store.nodeCalls != 1 {
		t.Fatalf("light-node scans = %d, want 1", store.nodeCalls)
	}
	if store.edgeCalls != 1 {
		t.Fatalf("light-edge scans = %d, want 1", store.edgeCalls)
	}
	wantRows := len(defaultFrameworkSynthesizers()) + len(defaultClaimingResolvers())
	if len(report.Per) != wantRows {
		t.Fatalf("report rows = %d, want %d", len(report.Per), wantRows)
	}
}

func BenchmarkSummarizeFrameworkCandidates100K(b *testing.B) {
	nodes := make([]*graph.Node, 100_000)
	languages := []string{"go", "typescript", "rust", "python", "java"}
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:         fmt.Sprintf("n-%d", i),
			Kind:       graph.KindFunction,
			Language:   languages[i%len(languages)],
			RepoPrefix: fmt.Sprintf("repo-%d", i%20),
		}
	}
	store := &countingFrameworkLightStore{Store: graph.New(), nodes: nodes}
	scope := map[string]bool{"repo-1": true, "repo-3": true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summary := summarizeFrameworkCandidates(store, scope)
		if len(summary.all) == 0 {
			b.Fatal("empty summary")
		}
	}
}

func BenchmarkSummarizeFrameworkObserverCandidates100K(b *testing.B) {
	nodes := make([]*graph.Node, 100_000)
	edges := make([]*graph.Edge, 100_000)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:         fmt.Sprintf("n-%d", i),
			Kind:       graph.KindFunction,
			Name:       "work",
			Language:   "go",
			RepoPrefix: "repo",
		}
		edges[i] = &graph.Edge{
			From: fmt.Sprintf("n-%d", i),
			To:   fmt.Sprintf("field-%d", i),
			Kind: graph.EdgeAccessesField,
		}
	}
	nodes[0].Name = "addListener"
	nodes[1].Name = "notifyObservers"
	store := &countingFrameworkLightStore{Store: graph.New(), nodes: nodes, edges: edges}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summary := summarizeFrameworkCandidates(store, nil)
		if summary.allMarkers[SynthObserverChannel] != 0 {
			b.Fatal("unrelated observer fields must not enable synthesis")
		}
	}
}

// One-way-gate safety for the round-12 census markers and conjunction gates:
// a workspace CONTAINING the candidate shape must always pass the gate (a
// wrong skip is a quality regression; a wrong run is only wasted time).
func TestNewCensusMarkersNeverSkipPresentCandidates(t *testing.T) {
	markers := map[string]int{}
	nodes := []*graph.Node{
		{ID: "a/i.cs::IHandler", Kind: graph.KindInterface, Language: "csharp", FilePath: "a/i.cs", Name: "IHandler"},
		{ID: "a/k.kt::Expecter", Kind: graph.KindFunction, Language: "kotlin", FilePath: "a/k.kt", Name: "Expecter"},
		{ID: "a/m.h::MAC", Kind: graph.KindMacro, Language: "c", FilePath: "a/m.h", Name: "MAC"},
		{ID: "repo::route::goframe::GET:/x", Kind: graph.KindContract, Language: "go", FilePath: "a/r.go", Name: "GET:/x"},
		{ID: "a/+page.svelte::cmp", Kind: graph.KindType, Language: "svelte", FilePath: "a/+page.svelte", Name: "cmp"},
		{ID: "a/+page.server.ts::load", Kind: graph.KindFunction, Language: "typescript", FilePath: "a/+page.server.ts", Name: "load"},
		{ID: "a/u.pas", Kind: graph.KindFile, Language: "pascal", FilePath: "a/u.pas", Name: "u.pas"},
		{ID: "a/u.dfm", Kind: graph.KindFile, Language: "", FilePath: "a/u.dfm", Name: "u.dfm"},
	}
	for _, n := range nodes {
		recordFrameworkNodeCandidates(markers, n, frameworkLanguageFamily(n.Language))
	}
	for _, name := range []string{
		SynthCSharpIfaceDispatch, SynthKMPExpectActual, SynthMacroExpansion, SynthGoFrameRoute,
	} {
		if markers[name] == 0 {
			t.Errorf("marker %s not recorded for a present candidate", name)
		}
	}
	for _, m := range []string{
		frameworkMarkerSvelteKitPage, frameworkMarkerSvelteKitServer,
		frameworkMarkerPascalSource, frameworkMarkerPascalForm,
	} {
		if markers[m] == 0 {
			t.Errorf("marker %s not recorded for a present candidate", m)
		}
	}

	// Conjunction gates: both sides present must pass; either side absent
	// must skip (that is their entire point).
	both := frameworkCandidateSummary{all: map[string]int{"web": 1, "apple": 1}, allMarkers: markers}
	webOnly := frameworkCandidateSummary{all: map[string]int{"web": 1}, allMarkers: markers}
	s := synthFunc{name: SynthExpoModules}
	if !shouldRunFrameworkSynthesizer(s, nil, both) {
		t.Error("expo bridge must run when both sides are present")
	}
	if shouldRunFrameworkSynthesizer(s, nil, webOnly) {
		t.Error("expo bridge must skip when the native side is absent")
	}
}
