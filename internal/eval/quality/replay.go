package quality

import (
	"fmt"
	"sort"
)

// ReplayQuery is one (query, expected) pair sourced from a query
// log. The expected list is optional — when present the replay
// computes recall@k against it; when absent the replay only
// produces ranking-delta metrics (Kendall τ, top-k churn) between
// baseline and candidate.
type ReplayQuery struct {
	Query    string   `json:"query"`
	Expected []string `json:"expected,omitempty"`
}

// RankerFunc is the contract a candidate / baseline implementation
// satisfies. Returns the ordered list of result IDs (file paths /
// symbol IDs — what shape the caller uses) for one query.
type RankerFunc func(query string, topK int) []string

// PerQueryDelta is one query's outcome from a replay run.
type PerQueryDelta struct {
	Query        string   `json:"query"`
	Baseline     []string `json:"baseline"`
	Candidate    []string `json:"candidate"`
	Kendall      float64  `json:"kendall_tau"`     // -1..+1; 1 = identical order
	Top1Changed  bool     `json:"top1_changed"`
	Top5Changes  int      `json:"top5_changes"`    // |baseline[:5] △ candidate[:5]|
	RecallBase   float64  `json:"recall_baseline,omitempty"`
	RecallCand   float64  `json:"recall_candidate,omitempty"`
}

// ReplayResult is the aggregate output of a replay run.
type ReplayResult struct {
	PerQuery       []PerQueryDelta `json:"per_query"`
	MeanKendall    float64         `json:"mean_kendall_tau"`
	Top1ChurnPct   float64         `json:"top1_churn_pct"`   // % of queries with top1 changed
	MeanTop5Churn  float64         `json:"mean_top5_changes"`
	RecallDelta    float64         `json:"recall_delta"`     // candidate - baseline mean recall (when ground truth exists)
}

// Replay walks the query log, scores each query against baseline +
// candidate, and aggregates the deltas. K is the top-K depth for
// the comparison (10 is the standard NDCG@10 / top-10 churn shape).
func Replay(queries []ReplayQuery, baseline, candidate RankerFunc, k int) (ReplayResult, error) {
	if baseline == nil || candidate == nil {
		return ReplayResult{}, fmt.Errorf("baseline and candidate rankers are required")
	}
	if k <= 0 {
		k = 10
	}
	out := ReplayResult{PerQuery: make([]PerQueryDelta, 0, len(queries))}
	var totKendall, totTop5 float64
	var top1Changes, withGT int
	var totRecallBase, totRecallCand float64
	for _, q := range queries {
		b := baseline(q.Query, k)
		c := candidate(q.Query, k)
		row := PerQueryDelta{
			Query:       q.Query,
			Baseline:    truncate(b, k),
			Candidate:   truncate(c, k),
			Kendall:     kendallTauTopK(b, c, k),
			Top1Changed: top1Differs(b, c),
			Top5Changes: setSymDiffSize(prefix(b, 5), prefix(c, 5)),
		}
		if len(q.Expected) > 0 {
			row.RecallBase = recallAtK(b, q.Expected, k)
			row.RecallCand = recallAtK(c, q.Expected, k)
			totRecallBase += row.RecallBase
			totRecallCand += row.RecallCand
			withGT++
		}
		totKendall += row.Kendall
		totTop5 += float64(row.Top5Changes)
		if row.Top1Changed {
			top1Changes++
		}
		out.PerQuery = append(out.PerQuery, row)
	}
	if n := len(queries); n > 0 {
		out.MeanKendall = totKendall / float64(n)
		out.MeanTop5Churn = totTop5 / float64(n)
		out.Top1ChurnPct = float64(top1Changes) / float64(n) * 100.0
	}
	if withGT > 0 {
		out.RecallDelta = (totRecallCand - totRecallBase) / float64(withGT)
	}
	return out, nil
}

// --- ranking-delta math ---------------------------------------------

// kendallTauTopK computes Kendall's τ over the top-K shared
// elements of two ranked lists. Returns 1 when both lists are
// empty or share fewer than 2 elements (τ is undefined; treat as
// "no measurable disagreement").
func kendallTauTopK(a, b []string, k int) float64 {
	aPref := prefix(a, k)
	bPref := prefix(b, k)
	rankA := indexMap(aPref)
	rankB := indexMap(bPref)
	// Intersection of the two prefixes.
	var shared []string
	for _, id := range aPref {
		if _, ok := rankB[id]; ok {
			shared = append(shared, id)
		}
	}
	n := len(shared)
	if n < 2 {
		return 1
	}
	var concordant, discordant int
	for i := range n {
		for j := i + 1; j < n; j++ {
			a, b := shared[i], shared[j]
			aOrder := rankA[a] < rankA[b]
			bOrder := rankB[a] < rankB[b]
			if aOrder == bOrder {
				concordant++
			} else {
				discordant++
			}
		}
	}
	pairs := concordant + discordant
	if pairs == 0 {
		return 1
	}
	return float64(concordant-discordant) / float64(pairs)
}

func top1Differs(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return false
	}
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	return a[0] != b[0]
}

func setSymDiffSize(a, b []string) int {
	as := map[string]struct{}{}
	for _, x := range a {
		as[x] = struct{}{}
	}
	bs := map[string]struct{}{}
	for _, x := range b {
		bs[x] = struct{}{}
	}
	count := 0
	for x := range as {
		if _, ok := bs[x]; !ok {
			count++
		}
	}
	for x := range bs {
		if _, ok := as[x]; !ok {
			count++
		}
	}
	return count
}

func recallAtK(returned, expected []string, k int) float64 {
	if len(expected) == 0 {
		return 0
	}
	expSet := map[string]bool{}
	for _, e := range expected {
		expSet[e] = true
	}
	hits := 0
	limit := min(k, len(returned))
	for i := range limit {
		if expSet[returned[i]] {
			hits++
		}
	}
	return float64(hits) / float64(len(expected))
}

func prefix(s []string, k int) []string {
	if k <= 0 || len(s) <= k {
		return s
	}
	return s[:k]
}

func truncate(s []string, k int) []string {
	out := make([]string, 0, k)
	out = append(out, prefix(s, k)...)
	return out
}

func indexMap(s []string) map[string]int {
	m := make(map[string]int, len(s))
	for i, v := range s {
		if _, ok := m[v]; !ok {
			m[v] = i
		}
	}
	return m
}

// Used only to keep imports stable when iterating.
var _ = sort.Sort
