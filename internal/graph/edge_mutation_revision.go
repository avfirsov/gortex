package graph

// EdgeMutationRevision returns a monotonic revision for persisted edge state.
// It changes when an edge is inserted, removed, reindexed, or replaced in
// place. Resolver callers use it only as an invalidation token; skipped values
// and false-positive increments are harmless.
func (g *Graph) EdgeMutationRevision() uint64 {
	return g.edgeMutGen.Load()
}

// MutationRevision returns a monotonic invalidation token for resolver lookup
// and pass indexes. Node and edge counters advance independently; their sum is
// monotonic and avoids another atomic on every edge write.
func (g *Graph) MutationRevision() uint64 {
	return g.nodeMutGen.Load() + g.edgeMutGen.Load()
}
