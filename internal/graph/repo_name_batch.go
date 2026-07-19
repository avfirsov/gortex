package graph

// RepoNamesNodeFinder is an optional scoped sibling of FindNodesByNames.
// Implementations evaluate the repository predicate in the backend so a
// page-local semantic pass never materializes same-named symbols from every
// tracked repository.
type RepoNamesNodeFinder interface {
	FindNodesByNamesInRepo(names []string, repoPrefix string) map[string][]*Node
}

// RepoLanguageSymbolCounter counts semantic symbols without materializing
// their rows. File and import nodes are deliberately excluded, matching the
// coverage counters reported by semantic providers.
type RepoLanguageSymbolCounter interface {
	CountRepoLanguageSymbols(repoPrefix string, languages []string) int
}

var _ RepoNamesNodeFinder = (*Graph)(nil)
var _ RepoLanguageSymbolCounter = (*Graph)(nil)

// FindNodesByNamesInRepo scans the in-memory shards once for the whole name
// page. It intentionally does not loop through FindNodesByName: that would
// turn a bounded semantic page into one index lookup per distinct type name.
func (g *Graph) FindNodesByNamesInRepo(names []string, repoPrefix string) map[string][]*Node {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			wanted[name] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	out := make(map[string][]*Node, len(wanted))
	for _, shard := range g.shards {
		shard.mu.RLock()
		for _, node := range shard.nodes {
			if node == nil || node.RepoPrefix != repoPrefix {
				continue
			}
			if _, ok := wanted[node.Name]; ok {
				out[node.Name] = append(out[node.Name], node)
			}
		}
		shard.mu.RUnlock()
	}
	return out
}

func (g *Graph) CountRepoLanguageSymbols(repoPrefix string, languages []string) int {
	wanted := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		if language != "" {
			wanted[language] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return 0
	}
	count := 0
	for _, shard := range g.shards {
		shard.mu.RLock()
		for _, node := range shard.nodes {
			if node == nil || node.RepoPrefix != repoPrefix ||
				node.Kind == KindFile || node.Kind == KindImport {
				continue
			}
			if _, ok := wanted[node.Language]; ok {
				count++
			}
		}
		shard.mu.RUnlock()
	}
	return count
}
