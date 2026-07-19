package graph

import "sort"

// RepoLightNodeReader returns metadata-free identity/name/location rows for a
// set of repository prefixes in one backend operation.
type RepoLightNodeReader interface {
	RepoNodesLight(repoPrefixes []string) []*Node
}

// ReadRepoNodesLight uses the compact production capability. The adapter
// fallback first projects only scoped IDs by the finite node-kind registry,
// then performs one batched fetch; it never materializes a workspace snapshot.
func ReadRepoNodesLight(store Store, repoPrefixes []string) []*Node {
	if store == nil || len(repoPrefixes) == 0 {
		return nil
	}
	if reader, ok := store.(RepoLightNodeReader); ok {
		return reader.RepoNodesLight(repoPrefixes)
	}
	kinds := make([]NodeKind, 0, len(validNodeKinds))
	for kind := range validNodeKinds {
		kinds = append(kinds, kind)
	}
	ids := ReadRepoNodeIDsByKinds(store, repoPrefixes, kinds)
	nodes := store.GetNodesByIDs(ids)
	out := make([]*Node, 0, len(nodes))
	for _, node := range nodes {
		if node != nil {
			out = append(out, &Node{
				ID: node.ID, Kind: node.Kind, Name: node.Name, FilePath: node.FilePath,
				Language: node.Language, RepoPrefix: node.RepoPrefix,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RepoNodesLight walks only requested in-memory repository buckets and copies
// the six fields consumed by framework preflights.
func (g *Graph) RepoNodesLight(repoPrefixes []string) []*Node {
	var out []*Node
	g.visitRepoProjectionNodes(repoPrefixes, func(node *Node) {
		out = append(out, &Node{
			ID: node.ID, Kind: node.Kind, Name: node.Name, FilePath: node.FilePath,
			Language: node.Language, RepoPrefix: node.RepoPrefix,
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var _ RepoLightNodeReader = (*Graph)(nil)
