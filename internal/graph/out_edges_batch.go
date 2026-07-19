package graph

// OutEdgesForNodes returns each node's outgoing edges through the Store's
// required batched operation. There is intentionally no per-node fallback.
func OutEdgesForNodes(r Store, ids []string) map[string][]*Edge {
	return r.GetOutEdgesByNodeIDs(ids)
}

// ExistingNodeIDFinder is an optional lightweight existence projection.
// Disk stores can return only primary-key strings instead of decoding full
// nodes (including docs, signatures, and Meta) when a caller needs presence.
type ExistingNodeIDFinder interface {
	ExistingNodeIDs(ids []string) map[string]struct{}
}

// LookupExistingNodeIDs returns the requested IDs that already exist. The
// fallback remains one batched Store call; it never performs point lookups.
func LookupExistingNodeIDs(store Store, ids []string) map[string]struct{} {
	if finder, ok := store.(ExistingNodeIDFinder); ok {
		return finder.ExistingNodeIDs(ids)
	}
	nodes := store.GetNodesByIDs(ids)
	out := make(map[string]struct{}, len(nodes))
	for id := range nodes {
		out[id] = struct{}{}
	}
	return out
}

// ExistingNodeIDs is the in-memory backend's one-lock-per-shard existence
// projection. It does not allocate or copy Node payloads.
func (g *Graph) ExistingNodeIDs(ids []string) map[string]struct{} {
	out := make(map[string]struct{})
	idsByShard := make(map[*shard]map[string]struct{})
	for _, id := range ids {
		if id == "" {
			continue
		}
		s := g.shardFor(id)
		group := idsByShard[s]
		if group == nil {
			group = make(map[string]struct{})
			idsByShard[s] = group
		}
		group[id] = struct{}{}
	}
	for s, group := range idsByShard {
		s.mu.RLock()
		for id := range group {
			if _, ok := s.nodes[id]; ok {
				out[id] = struct{}{}
			}
		}
		s.mu.RUnlock()
	}
	return out
}

// EdgeEndpoint identifies every graph edge between one source and target,
// independent of kind or source location. Semantic providers use it to batch
// exact existence checks without materializing a caller's full adjacency.
type EdgeEndpoint struct {
	From string
	To   string
}

// EdgeSite identifies candidate edges emitted at one source location. Kind is
// optional: an empty Kind requests every edge kind at the site.
type EdgeSite struct {
	From string
	Line int
	Kind EdgeKind
}

type edgeEndpointKind struct {
	From string
	To   string
	Kind EdgeKind
}

// EdgeCandidateSet contains only rows requested by endpoint or source site.
// The SQLite backend fills it with predicate-shaped queries; the in-memory
// fallback filters its O(1) adjacency buckets.
type EdgeCandidateSet struct {
	byEndpoint     map[EdgeEndpoint][]*Edge
	byEndpointKind map[edgeEndpointKind][]*Edge
	bySite         map[EdgeSite][]*Edge
}

// NewEdgeCandidateSet returns an initialized, empty candidate set.
func NewEdgeCandidateSet() EdgeCandidateSet {
	return EdgeCandidateSet{
		byEndpoint:     make(map[EdgeEndpoint][]*Edge),
		byEndpointKind: make(map[edgeEndpointKind][]*Edge),
		bySite:         make(map[EdgeSite][]*Edge),
	}
}

// AddEndpoint records an edge under its exact endpoint.
func (c *EdgeCandidateSet) AddEndpoint(edge *Edge) {
	if edge == nil {
		return
	}
	if c.byEndpoint == nil {
		c.byEndpoint = make(map[EdgeEndpoint][]*Edge)
	}
	if c.byEndpointKind == nil {
		c.byEndpointKind = make(map[edgeEndpointKind][]*Edge)
	}
	key := EdgeEndpoint{From: edge.From, To: edge.To}
	c.byEndpoint[key] = append(c.byEndpoint[key], edge)
	kindKey := edgeEndpointKind{From: edge.From, To: edge.To, Kind: edge.Kind}
	c.byEndpointKind[kindKey] = append(c.byEndpointKind[kindKey], edge)
}

// AddSite records an edge under both its exact-kind and any-kind site keys.
func (c *EdgeCandidateSet) AddSite(edge *Edge) {
	if edge == nil {
		return
	}
	if c.bySite == nil {
		c.bySite = make(map[EdgeSite][]*Edge)
	}
	exact := EdgeSite{From: edge.From, Line: edge.Line, Kind: edge.Kind}
	c.bySite[exact] = append(c.bySite[exact], edge)
	anyKind := EdgeSite{From: edge.From, Line: edge.Line}
	c.bySite[anyKind] = append(c.bySite[anyKind], edge)
}

// Add makes a newly-created edge immediately visible to both lookup shapes.
func (c *EdgeCandidateSet) Add(edge *Edge) {
	c.AddEndpoint(edge)
	c.AddSite(edge)
}

// Endpoint returns the first edge whose endpoints exactly match the key.
func (c EdgeCandidateSet) Endpoint(from, to string) *Edge {
	for _, edge := range c.byEndpoint[EdgeEndpoint{From: from, To: to}] {
		// Candidate sets may retain a pointer under its former target after a
		// reindex. Recheck the live fields so that stale bucket cannot be claimed.
		if edge != nil && edge.From == from && edge.To == to {
			return edge
		}
	}
	return nil
}

// EndpointKind returns the first edge matching an exact endpoint and kind.
func (c EdgeCandidateSet) EndpointKind(from, to string, kind EdgeKind) *Edge {
	for _, edge := range c.byEndpointKind[edgeEndpointKind{From: from, To: to, Kind: kind}] {
		if edge != nil && edge.From == from && edge.To == to && edge.Kind == kind {
			return edge
		}
	}
	return nil
}

// Site returns candidate edges at one source line, optionally scoped by kind.
func (c EdgeCandidateSet) Site(from string, line int, kind EdgeKind) []*Edge {
	return c.bySite[EdgeSite{From: from, Line: line, Kind: kind}]
}

// GetEdgeCandidates is the in-memory backend's native batched
// implementation. It groups requested sources by shard and takes one read
// lock per shard; no point-lookup loop or graph-wide edge snapshot is used.
func (g *Graph) GetEdgeCandidates(endpoints []EdgeEndpoint, sites []EdgeSite) EdgeCandidateSet {
	out := NewEdgeCandidateSet()
	endpointWanted := make(map[EdgeEndpoint]struct{}, len(endpoints))
	siteWanted := make(map[EdgeSite]struct{}, len(sites))
	idsByShard := make(map[*shard]map[string]struct{})
	rememberFrom := func(id string) {
		if id == "" {
			return
		}
		s := g.shardFor(id)
		ids := idsByShard[s]
		if ids == nil {
			ids = make(map[string]struct{})
			idsByShard[s] = ids
		}
		ids[id] = struct{}{}
	}
	for _, key := range endpoints {
		if key.From == "" || key.To == "" {
			continue
		}
		endpointWanted[key] = struct{}{}
		rememberFrom(key.From)
	}
	for _, key := range sites {
		if key.From == "" {
			continue
		}
		siteWanted[key] = struct{}{}
		rememberFrom(key.From)
	}

	for s, ids := range idsByShard {
		s.mu.RLock()
		for id := range ids {
			for _, edge := range s.outEdges[id] {
				if edge == nil {
					continue
				}
				if _, ok := endpointWanted[EdgeEndpoint{From: edge.From, To: edge.To}]; ok {
					out.AddEndpoint(edge)
				}
				exactSite := EdgeSite{From: edge.From, Line: edge.Line, Kind: edge.Kind}
				anySite := EdgeSite{From: edge.From, Line: edge.Line}
				if _, ok := siteWanted[exactSite]; ok {
					out.AddSite(edge)
				} else if _, ok := siteWanted[anySite]; ok {
					out.AddSite(edge)
				}
			}
		}
		s.mu.RUnlock()
	}
	return out
}

// LookupEdgeCandidates is the common entry point used by semantic providers.
// Store requires a native batched implementation, so this function can never
// fall back to N point lookups or an AllEdges scan.
func LookupEdgeCandidates(store Store, endpoints []EdgeEndpoint, sites []EdgeSite) EdgeCandidateSet {
	return store.GetEdgeCandidates(endpoints, sites)
}
