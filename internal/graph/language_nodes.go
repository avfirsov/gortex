package graph

// GetNodesByLanguage returns every node whose promoted Language field matches
// language. It walks shard maps under read locks and materialises only matches.
func (g *Graph) GetNodesByLanguage(language string) []*Node {
	if language == "" {
		return nil
	}
	var out []*Node
	for _, s := range g.shards {
		s.mu.RLock()
		for _, n := range s.nodes {
			if n != nil && n.Language == language {
				out = append(out, n)
			}
		}
		s.mu.RUnlock()
	}
	return out
}
