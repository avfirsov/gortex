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

func TestSelectFinalExploreCandidatesPreservesSourceLiteralAfterRerank(t *testing.T) {
	prod := []*rerank.Candidate{
		sourcePreservationCandidate("primary-0", 0, 0),
		sourcePreservationCandidate("primary-1", 1, 0),
		sourcePreservationCandidate("primary-2", 2, 0),
		sourcePreservationCandidate("source", 80, 1),
	}
	tests := []*rerank.Candidate{sourcePreservationCandidate("test", 0, 0)}

	require.Nil(t, candidateByID(prod[:3], "source"), "the fixture must put source evidence outside the final reranked window")
	selected := selectFinalExploreCandidates(prod, tests, 3)

	require.Len(t, selected, 3)
	require.Equal(t, []string{"source", "primary-0", "primary-1"}, []string{
		selected[0].Node.ID,
		selected[1].Node.ID,
		selected[2].Node.ID,
	})
	require.Nil(t, candidateByID(selected, "test"), "source reservation must replace, not widen, the production cap")
}

func TestSelectFinalExploreCandidatesPromotesCompleteSourceForReversedInput(t *testing.T) {
	for name, prod := range map[string][]*rerank.Candidate{
		"source-last": {
			sourcePreservationCandidate("primary", 0, 0),
			sourcePreservationCandidate("source", 1, 1),
		},
		"source-first": {
			sourcePreservationCandidate("source", 1, 1),
			sourcePreservationCandidate("primary", 0, 0),
		},
	} {
		t.Run(name, func(t *testing.T) {
			selected := selectFinalExploreCandidates(prod, nil, 2)
			require.Len(t, selected, 2)
			require.Equal(t, "source", selected[0].Node.ID)
		})
	}
}

func TestSelectFinalExploreCandidatesKeepsAmbiguousSourceBehindOrdinaryHead(t *testing.T) {
	primary := sourcePreservationCandidate("primary", 0, 0)
	ambiguous := sourcePreservationCandidate("source", 1, 1)
	ambiguous.Signals[exploreContentRecallAmbiguousSignal] = 1

	selected := selectFinalExploreCandidates([]*rerank.Candidate{primary, ambiguous}, nil, 2)

	require.Len(t, selected, 2)
	require.Equal(t, "primary", selected[0].Node.ID)
	require.Equal(t, "source", selected[1].Node.ID)
}

func TestSelectFinalExploreCandidatesKeepsSettledProductionOrder(t *testing.T) {
	prod := []*rerank.Candidate{
		sourcePreservationCandidate("settled-0", 80, 0),
		sourcePreservationCandidate("settled-1", 70, 0),
		sourcePreservationCandidate("earlier-channel-rank", 0, 0),
	}

	selected := selectFinalExploreCandidates(prod, nil, 2)

	require.Len(t, selected, 2)
	require.Equal(t, "settled-0", selected[0].Node.ID)
	require.Equal(t, "settled-1", selected[1].Node.ID)
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
