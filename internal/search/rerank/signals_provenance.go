package rerank

import "github.com/zzet/gortex/internal/graph"

// ProvenanceSignal scores a candidate by the resolution provenance of
// its inbound call / reference edges. It rewards symbols reached by
// direct, structurally-unambiguous (ast_resolved) edges and attenuates
// symbols whose in-edges are dominated by the abundant LSP-dispatch /
// framework-wiring tier or the weak name-only tier. LSP enrichment
// materialises a dense layer of interface-dispatch and callback edges;
// counting them at full weight inflates the apparent importance of
// utility and framework code, so this signal pulls genuine domain
// authorities back up. It complements FanInSignal, which counts every
// inbound edge uniformly — keep its weight small so the two do not
// double-count topology.
//
// The contribution is the candidate's average inbound-edge provenance
// weight (graph.ProvenanceWeight) mapped onto [0,1]. A candidate with
// no inbound call / reference edges contributes 0 (degrade-to-zero,
// like the other graph-derived signals).
type ProvenanceSignal struct{}

// Name returns the canonical signal name registered in DefaultWeights.
func (ProvenanceSignal) Name() string { return SignalProvenance }

// Contribute returns the normalised average provenance weight of the
// candidate's inbound call / reference edges, in [0,1].
func (ProvenanceSignal) Contribute(_ string, c *Candidate, ctx *Context) float64 {
	if c == nil || c.Node == nil || ctx == nil {
		return 0
	}
	edges := ctx.inEdges(c.Node.ID)
	if len(edges) == 0 {
		return 0
	}
	var sum float64
	var count int
	for _, e := range edges {
		if e == nil || (e.Kind != graph.EdgeCalls && e.Kind != graph.EdgeReferences) {
			continue
		}
		sum += graph.ProvenanceWeight(e)
		count++
	}
	if count == 0 {
		return 0
	}
	avg := sum / float64(count)
	// Map the provenance-weight band onto [0,1]: an ast_resolved-only
	// in-neighbourhood scores 1, an LSP- or text-saturated one scores low.
	v := (avg - graph.ProvenanceWeightMin) / (graph.ProvenanceWeightMax - graph.ProvenanceWeightMin)
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
