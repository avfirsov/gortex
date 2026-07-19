package graph

// ProxyNodeCountAtLeast evaluates the proxy heap-budget predicate directly on
// shard maps and stops as soon as the limit is reached.
func (g *Graph) ProxyNodeCountAtLeast(limit int) bool {
	if limit <= 0 {
		return false
	}
	count := 0
	for _, shard := range g.shards {
		shard.mu.RLock()
		for _, node := range shard.nodes {
			if IsProxyNode(node) {
				count++
				if count >= limit {
					shard.mu.RUnlock()
					return true
				}
			}
		}
		shard.mu.RUnlock()
	}
	return false
}
