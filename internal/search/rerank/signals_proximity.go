package rerank

import "sort"

// ProximitySignal scores a candidate by its Random-Walk-with-Restart
// (Personalized PageRank) proximity to the query's strongest seed
// matches. It is the graph-centrality spine of retrieval: where BM25
// answers "does this symbol's text match the query", proximity answers
// "is this symbol structurally central to the code the query is really
// about" — the function the matched seeds call, the type they share, the
// handler they converge on.
//
// The walk is seeded from the top text/vector matches in the candidate
// set (see selectCentralitySeeds) and run over the call/reference graph;
// the stationary distribution concentrates probability on nodes reachable
// from the seeds along many short, high-provenance paths. Seeds score
// near 1.0; their close neighbourhood scores high; unrelated candidates
// decay to 0. Unlike fan-in (raw in-degree) or HITS (global authority),
// proximity is query-personalized — a utility with huge global fan-in
// scores low unless it sits near the query's seeds.
//
// The contribution is the per-candidate centrality precomputed once per
// Rerank in Context.prepare and normalised to [0,1]. When no centrality
// provider is wired (cold graph, no adjacency snapshot) the signal
// degrades cleanly to 0.
type ProximitySignal struct{}

func (ProximitySignal) Name() string { return SignalProximity }

func (ProximitySignal) Contribute(_ string, c *Candidate, ctx *Context) float64 {
	if ctx == nil || c == nil || c.Node == nil || ctx.centralityScores == nil {
		return 0
	}
	return ctx.centralityScores[c.Node.ID]
}

// defaultCentralitySeeds caps how many of the strongest candidates seed
// the RWR walk. A small set keeps the walk focused on the query's true
// anchors (and the walk cheap); too many seeds dilute the restart vector
// across loosely related matches.
const defaultCentralitySeeds = 8

// selectCentralitySeeds picks the RWR seed node IDs from a candidate
// batch: the candidates with the best available retrieval rank (text
// rank preferred, vector rank as a fallback), capped at maxSeeds. These
// are the matches we are most confident about; the walk radiates outward
// from them. Deterministic: ties break on node ID.
func selectCentralitySeeds(cands []*Candidate, maxSeeds int) []string {
	if maxSeeds <= 0 {
		maxSeeds = defaultCentralitySeeds
	}
	type ranked struct {
		id   string
		rank int
	}
	const maxInt = int(^uint(0) >> 1)
	rs := make([]ranked, 0, len(cands))
	for _, c := range cands {
		if c == nil || c.Node == nil || c.Node.ID == "" {
			continue
		}
		r := maxInt
		if c.TextRank >= 0 && c.TextRank < r {
			r = c.TextRank
		}
		if c.VectorRank >= 0 && c.VectorRank < r {
			r = c.VectorRank
		}
		rs = append(rs, ranked{id: c.Node.ID, rank: r})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].rank != rs[j].rank {
			return rs[i].rank < rs[j].rank
		}
		return rs[i].id < rs[j].id
	})
	if len(rs) > maxSeeds {
		rs = rs[:maxSeeds]
	}
	seeds := make([]string, len(rs))
	for i, r := range rs {
		seeds[i] = r.id
	}
	return seeds
}
