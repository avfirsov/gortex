package query

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search/rerank"
)

func xrepoCand(id, repo string, textRank, vecRank int) *rerank.Candidate {
	return &rerank.Candidate{
		Node:       &graph.Node{ID: id, RepoPrefix: repo},
		TextRank:   textRank,
		VectorRank: vecRank,
	}
}

func TestCrossRepoRerank_PerRepoCounters(t *testing.T) {
	// Global text ranks 0..4 interleaved across two repositories.
	cands := []*rerank.Candidate{
		xrepoCand("alpha::a", "alpha", 0, -1),
		xrepoCand("beta::a", "beta", 1, -1),
		xrepoCand("alpha::b", "alpha", 2, -1),
		xrepoCand("beta::b", "beta", 3, -1),
		xrepoCand("alpha::c", "alpha", 4, -1),
	}
	crossRepoRerank(cands)
	want := map[string]int{
		"alpha::a": 0, "alpha::b": 1, "alpha::c": 2,
		"beta::a": 0, "beta::b": 1,
	}
	for _, c := range cands {
		require.Equalf(t, want[c.Node.ID], c.TextRank, "TextRank of %s", c.Node.ID)
	}
}

func TestCrossRepoRerank_SingleRepoNoOp(t *testing.T) {
	cands := []*rerank.Candidate{
		xrepoCand("r::a", "solo", 0, -1),
		xrepoCand("r::b", "solo", 1, -1),
		xrepoCand("r::c", "solo", 2, -1),
	}
	crossRepoRerank(cands)
	for i, c := range cands {
		require.Equal(t, i, c.TextRank, "single-repo ranks must be left untouched")
	}
}

func TestCrossRepoRerank_VectorChannelToo(t *testing.T) {
	cands := []*rerank.Candidate{
		xrepoCand("alpha::a", "alpha", -1, 0),
		xrepoCand("beta::a", "beta", -1, 1),
		xrepoCand("alpha::b", "alpha", -1, 2),
	}
	crossRepoRerank(cands)
	got := map[string]int{}
	for _, c := range cands {
		got[c.Node.ID] = c.VectorRank
	}
	require.Equal(t, 0, got["alpha::a"])
	require.Equal(t, 1, got["alpha::b"])
	require.Equal(t, 0, got["beta::a"])
}

func TestCrossRepoRerank_TieBreakDeterministic(t *testing.T) {
	// The exact-name fallback tier hands several candidates the same
	// global rank; per-repo renumbering must be ID-stable.
	build := func() []*rerank.Candidate {
		return []*rerank.Candidate{
			xrepoCand("alpha::z", "alpha", 5, -1),
			xrepoCand("alpha::a", "alpha", 5, -1),
			xrepoCand("beta::m", "beta", 5, -1),
		}
	}
	first := build()
	crossRepoRerank(first)
	for run := 0; run < 5; run++ {
		c := build()
		crossRepoRerank(c)
		for i := range c {
			require.Equal(t, first[i].TextRank, c[i].TextRank, "run %d differs", run)
		}
	}
	got := map[string]int{}
	for _, c := range first {
		got[c.Node.ID] = c.TextRank
	}
	// alpha::a sorts before alpha::z by ID → takes per-repo rank 0.
	require.Equal(t, 0, got["alpha::a"])
	require.Equal(t, 1, got["alpha::z"])
	require.Equal(t, 0, got["beta::m"])
}
