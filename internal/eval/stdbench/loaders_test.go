package stdbench

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func queriesByID(ds Dataset) map[string]Query {
	out := make(map[string]Query, len(ds.Queries))
	for _, q := range ds.Queries {
		out[q.ID] = q
	}
	return out
}

func TestLoadCoIR(t *testing.T) {
	ds, err := LoadCoIR("testdata/coir")
	require.NoError(t, err)
	require.Equal(t, "CoIR", ds.Name)
	require.Len(t, ds.Corpus, 3)
	// Only queries present in qrels are kept — q3 has no judgement.
	require.Len(t, ds.Queries, 2)

	q := queriesByID(ds)
	require.Equal(t, "search a sorted array for an element", q["q1"].Text)
	require.Equal(t, map[string]int{"d1": 2}, q["q1"].Relevant)
	require.Equal(t, map[string]int{"d3": 1}, q["q2"].Relevant)
	// Corpus text folds the title into the body.
	require.Contains(t, ds.Corpus[0].Text, "binary search")
}

func TestLoadContextBench(t *testing.T) {
	ds, err := LoadContextBench("testdata/contextbench.jsonl")
	require.NoError(t, err)
	require.Equal(t, "ContextBench", ds.Name)
	// Both tasks carry the same 2-candidate pool — deduplicated to 2.
	require.Len(t, ds.Corpus, 2)
	require.Len(t, ds.Queries, 2)
	require.Equal(t, 2, ds.RelevantCount())

	q := queriesByID(ds)
	// A bare string relevance entry defaults to grade 1.
	require.Equal(t, map[string]int{"c1": 1}, q["t1"].Relevant)
	// An {id,score} relevance object keeps its graded score.
	require.Equal(t, map[string]int{"c2": 3}, q["t2"].Relevant)
}

func TestLoadSWEContextBench_SharesJSONLLoader(t *testing.T) {
	// SWE-ContextBench reuses the JSONL task loader — only the dataset
	// name differs from ContextBench.
	ds, err := LoadSWEContextBench("testdata/contextbench.jsonl")
	require.NoError(t, err)
	require.Equal(t, "SWE-ContextBench", ds.Name)
	require.Len(t, ds.Queries, 2)
	require.Len(t, ds.Corpus, 2)
}

func TestLoadCoIR_MissingDirectory(t *testing.T) {
	_, err := LoadCoIR("testdata/no-such-dir")
	require.Error(t, err)
}
