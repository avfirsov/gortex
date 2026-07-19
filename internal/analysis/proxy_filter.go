package analysis

import "github.com/zzet/gortex/internal/graph"

// excludeProxyIDs is the id-keyed counterpart of excludeProxyNodes, for
// computations that work over a node-id list rather than node pointers.
func excludeProxyIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !graph.IsProxyID(id) {
			out = append(out, id)
		}
	}
	return out
}

// edgeTouchesProxy reports whether either endpoint of e is a federation
// proxy id, so an adjacency builder can skip the edge and a proxy stub
// never dilutes a real node's score.
func edgeTouchesProxy(e *graph.Edge) bool {
	return graph.IsProxyID(e.From) || graph.IsProxyID(e.To)
}
