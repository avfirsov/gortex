package store_sqlite

// EdgeMutationRevision returns a monotonic edge-state invalidation token.
// It is process-local and intentionally coarse: false positives merely retain
// exact resolver liveness validation after an interleaved write.
func (s *Store) EdgeMutationRevision() uint64 {
	return s.edgeMutationRevision.Load()
}

// MutationRevision is the cache-invalidation companion to
// EdgeMutationRevision. SQLite's central revision hook is deliberately coarse
// and already advances for node-only writes, so both capabilities share it.
func (s *Store) MutationRevision() uint64 {
	return s.edgeMutationRevision.Load()
}
