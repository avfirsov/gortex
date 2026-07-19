package resolver

import (
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// Speculative dynamic-dispatch synthesis. Some call shapes are genuine static
// blind spots that no precision-first framework rule covers: computed member
// calls `obj["foo"]()`, getattr-style dispatch `getattr(o, "foo")()`, and
// decorator registries. The extractor stamps these dropped calls with
// Meta["dyn_shape"] + Meta["dyn_key"] (the method name when it is a literal),
// and this opt-in pass mints LOW-confidence best-guess `calls` edges to the
// plausible same-name targets — tagged OriginSpeculative + Meta[speculative]
// so they are hidden from every default query and surfaced only on demand.
//
// This is what codegraph's playbook refused as "partial coverage worse than
// none": gortex can ship it because the edges are present-but-hidden-by-default
// (zero pollution) and explicitly auditable via `analyze kind=speculative`.

const (
	// speculativeFanoutCap: above this many candidates the confidence floors;
	// above the hard cap the whole set is dropped as noise (codegraph's rule).
	speculativeFanoutCap = 12
	speculativeHardCap   = 40

	// Keep the opt-in pass's read maps and writes independent of total graph
	// size. At most speculativeSiteChunk dynamic sites are resolved together;
	// their fan-out is subsequently committed in speculativeWriteChunk slices.
	speculativeSiteChunk  = 128
	speculativeWriteChunk = 512
)

type speculativeDispatchSite struct {
	from, file, shape, key string
	line                   int
}

type speculativeEdgeSeq func(yield func(*graph.Edge) bool)

// ResolveSpeculativeDispatch mints speculative call edges for the tagged
// blind-spot call shapes. No-op when disabled. Returns the number of new
// logical edges staged by this invocation (an idempotent rerun returns zero).
func ResolveSpeculativeDispatch(g graph.Store, enabled bool) int {
	if g == nil || !enabled {
		return 0
	}

	mu := g.ResolveMutex()
	mu.Lock()
	defer mu.Unlock()

	sites := make([]speculativeDispatchSite, 0, speculativeSiteChunk)
	resolved := 0
	flush := func() {
		if len(sites) == 0 {
			return
		}

		callerSeen := make(map[string]struct{}, len(sites))
		callerIDs := make([]string, 0, len(sites))
		nameSeen := make(map[string]struct{}, len(sites))
		names := make([]string, 0, len(sites))
		for _, site := range sites {
			if _, seen := callerSeen[site.from]; !seen {
				callerSeen[site.from] = struct{}{}
				callerIDs = append(callerIDs, site.from)
			}
			if _, seen := nameSeen[site.key]; !seen {
				nameSeen[site.key] = struct{}{}
				names = append(names, site.key)
			}
		}
		callers := g.GetNodesByIDs(callerIDs)
		nodesByName := g.FindNodesByNames(names)

		// Candidate output is bounded by siteChunk*hardCap. Dedupe is local
		// to that bounded window; an edge written by an earlier window is
		// visible through the batched adjacency read below and is therefore
		// not staged twice.
		proposed := make([]*graph.Edge, 0, len(sites)*speculativeHardCap)
		proposedSeen := make(map[graph.EdgeEndpoint]struct{}, len(sites)*speculativeHardCap)
		for _, site := range sites {
			callerLang := ""
			if caller := callers[site.from]; caller != nil {
				callerLang = caller.Language
			}
			candidates, count := speculativeCandidatesFromNodes(nodesByName[site.key], callerLang)
			if count == 0 || count > speculativeHardCap {
				continue
			}
			conf := speculativeConfidence(count)
			for _, candidate := range candidates {
				endpoint := graph.EdgeEndpoint{From: site.from, To: candidate.ID}
				if _, seen := proposedSeen[endpoint]; seen {
					continue
				}
				proposedSeen[endpoint] = struct{}{}
				proposed = append(proposed, &graph.Edge{
					From: site.from, To: candidate.ID, Kind: graph.EdgeCalls,
					FilePath: site.file, Line: site.line,
					Origin:          graph.OriginSpeculative,
					Confidence:      conf,
					ConfidenceLabel: graph.ConfidenceLabelFor(graph.EdgeCalls, conf),
					Meta: map[string]any{
						graph.MetaSpeculative: true,
						MetaSynthesizedBy:     SynthSpeculative,
						MetaProvenance:        ProvenanceHeuristic,
						"via":                 "speculative." + site.shape,
						"candidate_count":     count,
						"dyn_key":             site.key,
					},
				})
			}
		}
		if len(proposed) == 0 {
			sites = sites[:0]
			return
		}

		// Replace the former graph-wide existing-edge scan with one bounded
		// adjacency batch for just this window's callers. Any real or already
		// synthesized call to an endpoint makes the logical mutation a no-op.
		outByCaller := g.GetOutEdgesByNodeIDs(callerIDs)
		existing := make(map[graph.EdgeEndpoint]struct{}, len(proposed))
		for _, edges := range outByCaller {
			for _, edge := range edges {
				if edge == nil || edge.Kind != graph.EdgeCalls || graph.IsUnresolvedTarget(edge.To) {
					continue
				}
				endpoint := graph.EdgeEndpoint{From: edge.From, To: edge.To}
				if _, wanted := proposedSeen[endpoint]; wanted {
					existing[endpoint] = struct{}{}
				}
			}
		}

		write := proposed[:0]
		for _, edge := range proposed {
			endpoint := graph.EdgeEndpoint{From: edge.From, To: edge.To}
			if _, found := existing[endpoint]; found {
				continue
			}
			write = append(write, edge)
		}
		for start := 0; start < len(write); start += speculativeWriteChunk {
			end := min(start+speculativeWriteChunk, len(write))
			g.AddBatch(nil, write[start:end])
		}
		resolved += len(write)
		sites = sites[:0]
	}

	for edge := range speculativeCallEdges(g) {
		if edge == nil || edge.Meta == nil {
			continue
		}
		shape, _ := edge.Meta["dyn_shape"].(string)
		if shape == "" {
			continue
		}
		// A precision-first synthesizer (object-registry) already bound this
		// computed-member dispatch site; do not also mint a hidden guess.
		if claimed, _ := edge.Meta["registry_claimed"].(bool); claimed {
			continue
		}
		key, _ := edge.Meta["dyn_key"].(string)
		if key == "" {
			continue // v1: literal-key shapes only (variable-key is unbounded)
		}
		sites = append(sites, speculativeDispatchSite{
			from: edge.From, file: edge.FilePath, line: edge.Line,
			shape: shape, key: key,
		})
		if len(sites) == speculativeSiteChunk {
			flush()
		}
	}
	flush()
	return resolved
}

// speculativeCallEdges performs one call-edge scan. Production SQLite uses
// the keyset-paged repo/file/kind projection (including the empty-prefix
// single-repo namespace); adapter stores keep their native kind iterator.
func speculativeCallEdges(g graph.Store) speculativeEdgeSeq {
	if _, ok := g.(graph.ScopedProjectionSequencer); ok {
		repos := append(g.RepoPrefixes(), "")
		sort.Strings(repos)
		return func(yield func(*graph.Edge) bool) {
			for row := range graph.EdgesInScopeSeq(g, repos, nil, graph.EdgeCalls) {
				if row.Edge != nil && !yield(row.Edge) {
					return
				}
			}
		}
	}
	return func(yield func(*graph.Edge) bool) {
		for edge := range g.EdgesByKind(graph.EdgeCalls) {
			if edge != nil && !yield(edge) {
				return
			}
		}
	}
}

// speculativeCandidatesFromNodes filters one batched FindNodesByNames slot.
// The returned slice never grows beyond hardCap+1; count remains exact so a
// language-filtered over-cap name is still rejected with the legacy rule.
func speculativeCandidatesFromNodes(nodes []*graph.Node, callerLang string) ([]*graph.Node, int) {
	out := make([]*graph.Node, 0, min(len(nodes), speculativeHardCap+1))
	count := 0
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		if graph.IsStub(n.ID) || graph.IsUnresolvedTarget(n.ID) {
			continue
		}
		if callerLang != "" && n.Language != "" && n.Language != callerLang {
			continue
		}
		count++
		if len(out) <= speculativeHardCap {
			out = append(out, n)
		}
	}
	return out, count
}

func speculativeConfidence(candidateCount int) float64 {
	conf := 1.0 / float64(candidateCount)
	if candidateCount > speculativeFanoutCap {
		conf = 0.05
	}
	if conf > 0.45 {
		conf = 0.45
	}
	if conf < 0.05 {
		conf = 0.05
	}
	return conf
}
