package analysis

import "fmt"

// AdjacencyPersistenceSnapshot is the serializable form of an immutable CSR
// adjacency. The slices returned by PersistenceSnapshot alias the live
// snapshot and must be treated as read-only.
type AdjacencyPersistenceSnapshot struct {
	IDs          []string
	Offsets      []int32
	Neighbors    []int32
	Weights      []float64
	OutWeight    []float64
	PackageRoots map[string]uint64
}

func (a *AdjacencySnapshot) PersistenceSnapshot() AdjacencyPersistenceSnapshot {
	if a == nil {
		return AdjacencyPersistenceSnapshot{}
	}
	return AdjacencyPersistenceSnapshot{
		IDs:          a.ids,
		Offsets:      a.offsets,
		Neighbors:    a.neighbors,
		Weights:      a.weights,
		OutWeight:    a.outWeight,
		PackageRoots: a.pkgRoots,
	}
}

// RestoreAdjacencySnapshot validates and takes ownership of snapshot's slices.
// Rebuilding only the ID index avoids re-scanning the graph after restart.
func RestoreAdjacencySnapshot(snapshot AdjacencyPersistenceSnapshot) (*AdjacencySnapshot, error) {
	n := len(snapshot.IDs)
	if n == 0 {
		if (len(snapshot.Offsets) != 0 && len(snapshot.Offsets) != 1) || len(snapshot.Neighbors) != 0 || len(snapshot.Weights) != 0 || len(snapshot.OutWeight) != 0 {
			return nil, fmt.Errorf("adjacency cache: invalid empty snapshot")
		}
		return &AdjacencySnapshot{
			ids: snapshot.IDs, index: map[string]int{}, offsets: snapshot.Offsets,
			neighbors: snapshot.Neighbors, weights: snapshot.Weights,
			outWeight: snapshot.OutWeight, pkgRoots: snapshot.PackageRoots,
		}, nil
	}
	if len(snapshot.Offsets) != n+1 {
		return nil, fmt.Errorf("adjacency cache: offsets=%d, want %d", len(snapshot.Offsets), n+1)
	}
	if len(snapshot.OutWeight) != n {
		return nil, fmt.Errorf("adjacency cache: out weights=%d, want %d", len(snapshot.OutWeight), n)
	}
	if len(snapshot.Neighbors) != len(snapshot.Weights) {
		return nil, fmt.Errorf("adjacency cache: neighbors=%d weights=%d", len(snapshot.Neighbors), len(snapshot.Weights))
	}
	if snapshot.Offsets[0] != 0 || int(snapshot.Offsets[n]) != len(snapshot.Neighbors) {
		return nil, fmt.Errorf("adjacency cache: invalid offset bounds")
	}
	index := make(map[string]int, n)
	for i, id := range snapshot.IDs {
		if id == "" {
			return nil, fmt.Errorf("adjacency cache: empty id at %d", i)
		}
		if _, duplicate := index[id]; duplicate {
			return nil, fmt.Errorf("adjacency cache: duplicate id %q", id)
		}
		index[id] = i
		if snapshot.Offsets[i] > snapshot.Offsets[i+1] {
			return nil, fmt.Errorf("adjacency cache: offsets not monotonic at %d", i)
		}
	}
	for i, neighbor := range snapshot.Neighbors {
		if neighbor < 0 || int(neighbor) >= n {
			return nil, fmt.Errorf("adjacency cache: neighbor %d out of range at %d", neighbor, i)
		}
	}
	return &AdjacencySnapshot{
		ids:       snapshot.IDs,
		index:     index,
		offsets:   snapshot.Offsets,
		neighbors: snapshot.Neighbors,
		weights:   snapshot.Weights,
		outWeight: snapshot.OutWeight,
		pkgRoots:  snapshot.PackageRoots,
	}, nil
}

// LeidenPartitionSnapshot is the serializable state needed to resume
// package-incremental community detection after a process restart.
type LeidenPartitionSnapshot struct {
	PackageFingerprints map[string]uint64
	NodeCommunities     map[string]string
}

func (c *LeidenPartitionCache) PersistenceSnapshot() LeidenPartitionSnapshot {
	if c == nil {
		return LeidenPartitionSnapshot{}
	}
	return LeidenPartitionSnapshot{
		PackageFingerprints: c.pkgFingerprint,
		NodeCommunities:     c.nodeComm,
	}
}

// RestoreLeidenPartitionCache takes ownership of snapshot's maps and stamps
// the cache with the current process-local edge-identity revision. Durable
// validity was already established by the graph store's mutation gate.
func RestoreLeidenPartitionCache(snapshot LeidenPartitionSnapshot, edgeIdentityRevision int) *LeidenPartitionCache {
	if snapshot.PackageFingerprints == nil {
		snapshot.PackageFingerprints = map[string]uint64{}
	}
	if snapshot.NodeCommunities == nil {
		snapshot.NodeCommunities = map[string]string{}
	}
	return &LeidenPartitionCache{
		pkgFingerprint:        snapshot.PackageFingerprints,
		nodeComm:              snapshot.NodeCommunities,
		edgeIdentityRevisions: edgeIdentityRevision,
	}
}
