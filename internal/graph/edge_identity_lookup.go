package graph

// EdgeIdentity is the complete logical key enforced by every Store backend.
// Payload fields such as confidence, provenance, and Meta deliberately do not
// participate: callers fetch the current row by this key and compare its full
// persisted payload separately.
type EdgeIdentity struct {
	From     string
	To       string
	Kind     EdgeKind
	FilePath string
	Line     int
}

// EdgeIdentityFor returns edge's complete logical key. A nil edge has the zero
// identity so batching callers can skip it without special allocation.
func EdgeIdentityFor(edge *Edge) EdgeIdentity {
	if edge == nil {
		return EdgeIdentity{}
	}
	return EdgeIdentity{
		From: edge.From, To: edge.To, Kind: edge.Kind,
		FilePath: edge.FilePath, Line: edge.Line,
	}
}

// EdgeIdentityBatchFinder is an optional exact-key projection. Implementations
// must batch keys set-wise; callers use it to validate yielded work without an
// EdgeExists N+1 or a broad source-site candidate query.
type EdgeIdentityBatchFinder interface {
	FindEdgesByIdentities(identities []EdgeIdentity) map[EdgeIdentity]*Edge
}

var _ EdgeIdentityBatchFinder = (*Graph)(nil)

// FindEdgesByIdentities is the in-memory exact-key projection. Requested
// sources are grouped by adjacency shard, each shard is read-locked once, and
// the existing logical-key position sidecar provides O(1) candidates. It never
// materializes AllEdges or scans unrelated source adjacency.
func (g *Graph) FindEdgesByIdentities(identities []EdgeIdentity) map[EdgeIdentity]*Edge {
	out := make(map[EdgeIdentity]*Edge)
	if len(identities) == 0 {
		return out
	}

	seen := make(map[EdgeIdentity]struct{}, len(identities))
	byShard := make(map[*shard][]EdgeIdentity)
	for _, identity := range identities {
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		s := g.shardFor(identity.From)
		byShard[s] = append(byShard[s], identity)
	}

	for s, keys := range byShard {
		s.mu.RLock()
		for _, identity := range keys {
			positions := s.outEdgeIdx[identity.From]
			position, ok := positions[hashEdgeKey(edgeKey(identity))]
			if !ok {
				continue
			}
			edges := s.outEdges[identity.From]
			if position < 0 || position >= len(edges) {
				continue
			}
			edge := edges[position]
			// Defend against the astronomically unlikely 128-bit hash collision
			// and against a corrupt/stale sidecar without returning a false hit.
			if edge != nil && EdgeIdentityFor(edge) == identity {
				out[identity] = edge
			}
		}
		s.mu.RUnlock()
	}
	return out
}
