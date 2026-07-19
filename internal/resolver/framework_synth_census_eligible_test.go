package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// A non-nil scope under the daemon's full-coverage attestation must build
// the full admission census from the raw store — the exact production shape
// of a cold index, whose all-repos scope historically bypassed every gate.
func TestCensusEligibleScopedRunBuildsFullCensus(t *testing.T) {
	t.Parallel()
	webNode := &graph.Node{ID: "app.ts::f", Kind: graph.KindFunction, Name: "f", Language: "typescript"}
	scope := map[string]bool{"repo": true}

	plain := summarizeFrameworkCandidates(edgeCensusStore([]*graph.Node{webNode}, nil), scope)
	require.False(t, plain.edges.valid, "an unattested scoped census must not claim the full stream")
	require.False(t, plain.fullCensus)

	attested := summarizeFrameworkCandidatesCensus(edgeCensusStore([]*graph.Node{webNode}, nil), scope, nil, true)
	assert.True(t, attested.fullCensus)
	assert.True(t, attested.edges.valid, "the attestation makes the census authoritative")

	// A file-scoped incremental frontier must never combine with the
	// attestation: partial file coverage cannot prove absence.
	fileScoped := summarizeFrameworkCandidatesCensus(edgeCensusStore([]*graph.Node{webNode}, nil), scope, []string{"app.ts"}, true)
	assert.False(t, fileScoped.fullCensus)
	assert.False(t, fileScoped.edges.valid)
}

// grpc-stub gates on the pass's EXACT admission predicate — the via alone is
// insufficient without non-empty service+method metadata; temporal gates on
// its via prefix or the exact Java annotation-role predicate.
func TestCensusGatesGRPCAndTemporalExactPredicates(t *testing.T) {
	t.Parallel()
	summaryFor := func(edges ...*graph.Edge) frameworkCandidateSummary {
		return summarizeFrameworkCandidates(edgeCensusStore(nil, edges), nil)
	}
	synths := map[string]FrameworkSynthesizer{}
	for _, s := range defaultFrameworkSynthesizers() {
		synths[s.Name()] = s
	}
	grpc, temporal := synths[SynthGRPCStub], synths[SynthTemporalStub]
	require.NotNil(t, grpc)
	require.NotNil(t, temporal)

	empty := summaryFor()
	assert.False(t, shouldRunFrameworkSynthesizer(grpc, nil, empty),
		"a marker-free census must skip grpc-stub")
	assert.False(t, shouldRunFrameworkSynthesizer(temporal, nil, empty),
		"a marker-free census must skip temporal-stub")

	viaOnly := summaryFor(&graph.Edge{
		From: "a::f", To: "unresolved::X", Kind: graph.EdgeCalls,
		Meta: map[string]any{"via": "grpc.stub"},
	})
	assert.False(t, shouldRunFrameworkSynthesizer(grpc, nil, viaOnly),
		"the pass discards service/method-less stubs — the gate mirrors that exactly")

	fullStub := summaryFor(&graph.Edge{
		From: "a::f", To: "unresolved::X", Kind: graph.EdgeCalls,
		Meta: map[string]any{"via": "grpc.stub", "grpc_service": "Svc", "grpc_method": "Do"},
	})
	assert.True(t, shouldRunFrameworkSynthesizer(grpc, nil, fullStub))

	temporalVia := summaryFor(&graph.Edge{
		From: "a::f", To: "unresolved::X", Kind: graph.EdgeCalls,
		Meta: map[string]any{"via": "temporal.workflow"},
	})
	assert.True(t, shouldRunFrameworkSynthesizer(temporal, nil, temporalVia))

	annotated := summaryFor(&graph.Edge{
		From: "a::W", To: javaWorkflowIfaceAnnoID, Kind: graph.EdgeAnnotated,
	})
	assert.True(t, shouldRunFrameworkSynthesizer(temporal, nil, annotated),
		"the Java annotation half of the presence probe must admit the pass")

	// Un-censused (subset-scoped) runs stay fail-open for both.
	scope := map[string]bool{"repo": true}
	scoped := summarizeFrameworkCandidates(edgeCensusStore(nil, nil), scope)
	assert.True(t, shouldRunFrameworkSynthesizer(grpc, scope, scoped))
	assert.True(t, shouldRunFrameworkSynthesizer(temporal, scope, scoped))
}
