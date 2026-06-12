package stdbench

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// DefaultKs are the rank cutoffs Recall@K / Precision@K report.
var DefaultKs = []int{1, 5, 10, 20}

// Retriever ranks corpus Doc IDs for a query, best first, capped at k.
type Retriever func(query string, k int) []string

// Metrics is the aggregate score of a retriever over a Dataset.
type Metrics struct {
	Dataset   string          `json:"dataset"`
	Queries   int             `json:"queries"`
	Scored    int             `json:"scored"` // queries with a relevance judgement
	RecallAtK map[int]float64 `json:"recall_at_k"`
	PrecAtK   map[int]float64 `json:"precision_at_k"`
	NDCGAt10  float64         `json:"ndcg_at_10"`
	MRR       float64         `json:"mrr"`
}

// Evaluate runs retrieve against every query in ds and aggregates the
// standard retrieval metrics. Queries with no relevance judgement are
// counted in Queries but excluded from the metric averages. ks is the
// Recall@K / Precision@K cutoff set; pass nil for DefaultKs.
func Evaluate(ds Dataset, retrieve Retriever, ks []int) Metrics {
	if len(ks) == 0 {
		ks = DefaultKs
	}
	maxK := 10 // NDCG@10 always needs at least the top 10.
	for _, k := range ks {
		if k > maxK {
			maxK = k
		}
	}

	m := Metrics{
		Dataset:   ds.Name,
		Queries:   len(ds.Queries),
		RecallAtK: make(map[int]float64, len(ks)),
		PrecAtK:   make(map[int]float64, len(ks)),
	}
	recallSum := make(map[int]float64, len(ks))
	precSum := make(map[int]float64, len(ks))
	var ndcgSum, rrSum float64

	for _, q := range ds.Queries {
		if len(q.Relevant) == 0 {
			continue
		}
		m.Scored++
		ranked := retrieve(q.Text, maxK)
		for _, k := range ks {
			hit := 0
			for i, id := range ranked {
				if i >= k {
					break
				}
				if q.Relevant[id] > 0 {
					hit++
				}
			}
			recallSum[k] += float64(hit) / float64(len(q.Relevant))
			precSum[k] += float64(hit) / float64(k)
		}
		ndcgSum += ndcg(ranked, q.Relevant, 10)
		rrSum += reciprocalRank(ranked, q.Relevant)
	}

	if m.Scored > 0 {
		for _, k := range ks {
			m.RecallAtK[k] = recallSum[k] / float64(m.Scored)
			m.PrecAtK[k] = precSum[k] / float64(m.Scored)
		}
		m.NDCGAt10 = ndcgSum / float64(m.Scored)
		m.MRR = rrSum / float64(m.Scored)
	}
	return m
}

// ndcg computes normalized discounted cumulative gain at cutoff k using
// the graded relevance labels in rel. Returns 0 when no relevant doc
// exists (ideal DCG would be zero).
func ndcg(ranked []string, rel map[string]int, k int) float64 {
	dcg := 0.0
	for i, id := range ranked {
		if i >= k {
			break
		}
		if g := rel[id]; g > 0 {
			dcg += float64(g) / math.Log2(float64(i+2))
		}
	}
	grades := make([]int, 0, len(rel))
	for _, g := range rel {
		if g > 0 {
			grades = append(grades, g)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	idcg := 0.0
	for i, g := range grades {
		if i >= k {
			break
		}
		idcg += float64(g) / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// reciprocalRank returns 1/rank of the first relevant hit, or 0 when no
// relevant doc appears in the ranked list.
func reciprocalRank(ranked []string, rel map[string]int) float64 {
	for i, id := range ranked {
		if rel[id] > 0 {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// Markdown renders the metrics as a Markdown section.
func (m Metrics) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", m.Dataset)
	fmt.Fprintf(&b, "_%d queries · %d scored against relevance judgements_\n\n", m.Queries, m.Scored)
	b.WriteString("| metric | value |\n|--------|-------|\n")

	ks := make([]int, 0, len(m.RecallAtK))
	for k := range m.RecallAtK {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	for _, k := range ks {
		fmt.Fprintf(&b, "| Recall@%d | %.3f |\n", k, m.RecallAtK[k])
	}
	for _, k := range ks {
		fmt.Fprintf(&b, "| Precision@%d | %.3f |\n", k, m.PrecAtK[k])
	}
	fmt.Fprintf(&b, "| NDCG@10 | %.3f |\n", m.NDCGAt10)
	fmt.Fprintf(&b, "| MRR | %.3f |\n", m.MRR)
	return b.String()
}
