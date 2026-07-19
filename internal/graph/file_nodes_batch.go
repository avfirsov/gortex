package graph

// GetFileNodesByPaths returns nodes grouped by file path while taking each
// shard lock once. It is the in-memory reference implementation of the Store
// batch contract; disk backends push the path predicate into their database.
func (g *Graph) GetFileNodesByPaths(filePaths []string) map[string][]*Node {
	if len(filePaths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(filePaths))
	paths := make([]string, 0, len(filePaths))
	for _, path := range filePaths {
		if path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil
	}

	out := make(map[string][]*Node, len(paths))
	for _, shard := range g.shards {
		shard.mu.RLock()
		for _, path := range paths {
			if nodes := shard.byFile[path]; len(nodes) > 0 {
				out[path] = append(out[path], nodes...)
			}
		}
		shard.mu.RUnlock()
	}
	return out
}
