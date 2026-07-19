package graph

import "context"

// ScopedEdgeKindEvicter removes edges whose source is in an exact frontier and
// whose kind is in the requested set. Implementations must keep the scope in
// the backend operation: partial derived-edge reconciliation uses this seam so
// one changed file never scans or rewrites an unchanged repository.
type ScopedEdgeKindEvicter interface {
	EvictEdgesFromSourcesByKinds(context.Context, []string, []EdgeKind) (int, error)
}

// EvictEdgesFromSourcesByKindsBackground is the non-cancellable indexing-pass
// entry point. Tests and request paths that own a context use the context-aware
// sibling below directly.
func EvictEdgesFromSourcesByKindsBackground(
	s Store,
	sourceIDs []string,
	kinds []EdgeKind,
) (removed int, supported bool, err error) {
	return EvictEdgesFromSourcesByKinds(context.Background(), s, sourceIDs, kinds)
}

// EvictEdgesFromSourcesByKinds dispatches only to the set-oriented capability.
// Production graph stores implement it; returning false for an adapter is safer
// than hiding an N+1 RemoveEdge fallback in a partial-index hot path.
func EvictEdgesFromSourcesByKinds(
	ctx context.Context,
	s Store,
	sourceIDs []string,
	kinds []EdgeKind,
) (removed int, supported bool, err error) {
	if s == nil || len(sourceIDs) == 0 || len(kinds) == 0 {
		return 0, true, nil
	}
	evicter, ok := s.(ScopedEdgeKindEvicter)
	if !ok {
		return 0, false, nil
	}
	removed, err = evicter.EvictEdgesFromSourcesByKinds(ctx, sourceIDs, kinds)
	return removed, true, err
}

// EvictEdgesFromSourcesByKinds implements the bounded capability for the
// in-memory graph. It performs one batched adjacency read; the per-edge writes
// below are in-process shard mutations, not backend round-trips.
func (g *Graph) EvictEdgesFromSourcesByKinds(
	ctx context.Context,
	sourceIDs []string,
	kinds []EdgeKind,
) (int, error) {
	if g == nil || len(sourceIDs) == 0 || len(kinds) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	kindSet := make(map[EdgeKind]struct{}, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = struct{}{}
	}
	sources := make([]string, 0, len(sourceIDs))
	seenSources := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if id == "" {
			continue
		}
		if _, seen := seenSources[id]; seen {
			continue
		}
		seenSources[id] = struct{}{}
		sources = append(sources, id)
	}
	adjacency := g.GetOutEdgesByNodeIDs(sources)
	type edgeKey struct {
		from string
		to   string
		kind EdgeKind
	}
	keys := make([]edgeKey, 0)
	for _, source := range sources {
		for _, edge := range adjacency[source] {
			if edge == nil {
				continue
			}
			if _, wanted := kindSet[edge.Kind]; wanted {
				keys = append(keys, edgeKey{from: edge.From, to: edge.To, kind: edge.Kind})
			}
		}
	}
	removed := 0
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if g.RemoveEdge(key.from, key.to, key.kind) {
			removed++
		}
	}
	return removed, nil
}

var _ ScopedEdgeKindEvicter = (*Graph)(nil)
