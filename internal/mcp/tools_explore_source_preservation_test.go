package mcp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search/rerank"
)

func sourcePreservationCandidate(id string, textRank int, sourceSignal float64) *rerank.Candidate {
	candidate := &rerank.Candidate{
		Node:       &graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: id + ".go"},
		TextRank:   textRank,
		VectorRank: -1,
	}
	if sourceSignal > 0 {
		candidate.Signals = map[string]float64{exploreSourceLiteralSignal: sourceSignal}
	}
	return candidate
}

func TestLimitExploreCandidatesPreservingSourceLiteralReservesOneFullCapSlot(t *testing.T) {
	candidates := []*rerank.Candidate{
		sourcePreservationCandidate("primary-0", 0, 0),
		sourcePreservationCandidate("primary-1", 1, 0),
		sourcePreservationCandidate("primary-2", 2, 0),
		sourcePreservationCandidate("primary-3", 3, 0),
		sourcePreservationCandidate("source", 80, 1),
	}

	require.Nil(t, candidateByID(limitExploreCandidates(candidates, 4), "source"), "the fixture must exercise a source candidate outside the ordinary cap")
	bounded := limitExploreCandidatesPreservingSourceLiteral(candidates, 4)
	require.Len(t, bounded, 4)
	require.NotNil(t, candidateByID(bounded, "source"))
}

func TestMergeExploreCandidatesPreservesSourceLiteralSignalThroughDedupe(t *testing.T) {
	node := &graph.Node{ID: "same", Name: "same", Kind: graph.KindFunction, FilePath: "same.go"}
	primarySignals := map[string]float64{"ordinary": 1}
	merged := mergeExploreCandidates(
		[]*rerank.Candidate{{Node: node, TextRank: 0, VectorRank: -1, Signals: primarySignals}},
		[]*rerank.Candidate{{Node: node, TextRank: 9, VectorRank: -1, Signals: map[string]float64{exploreSourceLiteralSignal: 0.5}}},
		20,
	)

	require.Len(t, merged, 1)
	require.Equal(t, 0.5, merged[0].Signals[exploreSourceLiteralSignal])
	require.Zero(t, primarySignals[exploreSourceLiteralSignal], "request-local evidence must not mutate an input candidate")
}

func TestExploreAnswerReadyKeepsQuotedNonExactConceptNonTerminal(t *testing.T) {
	head := exploreTarget{
		node: &graph.Node{
			ID:       "demo/registry.go::locale_registry_pipeline",
			Name:     "locale_registry_pipeline",
			QualName: "demo.locale_registry_pipeline",
			Kind:     graph.KindFunction,
			FilePath: "demo/registry.go",
		},
		source:                "func locale_registry_pipeline() {}",
		conceptImplementation: true,
	}

	require.True(t, exploreAnswerReady("locate locale registry pipeline", []exploreTarget{head}), "the fixture must otherwise satisfy ordinary concept terminality")
	require.False(t, exploreAnswerReady(`locate locale registry pipeline for "ku"`, []exploreTarget{head}))
}

func BenchmarkLimitExploreCandidatesPreservingSourceLiteral80(b *testing.B) {
	candidates := make([]*rerank.Candidate, 0, 80)
	for i := 0; i < 79; i++ {
		candidates = append(candidates, sourcePreservationCandidate(fmt.Sprintf("primary-%02d", i), i, 0))
	}
	candidates = append(candidates, sourcePreservationCandidate("source", 200, 1))

	b.ReportAllocs()
	for b.Loop() {
		if got := limitExploreCandidatesPreservingSourceLiteral(candidates, 40); len(got) != 40 {
			b.Fatalf("candidate count = %d, want 40", len(got))
		}
	}
}
