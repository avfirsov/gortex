package graph

// WorkspaceSlug is one repository-to-boundary mapping used by warm-start
// backfill. Empty values preserve the existing node column.
type WorkspaceSlug struct {
	RepoPrefix string
	Workspace  string
	Project    string
}

// WorkspaceSlugBackfiller is an optional set-oriented mutation capability.
// Implementations must fill only empty workspace/project columns, process all
// supplied repositories as one bounded operation, and return changed rows.
type WorkspaceSlugBackfiller interface {
	BackfillWorkspaceSlugs(slugs []WorkspaceSlug) int
}

// BackfillWorkspaceSlugs updates resident nodes under shard write locks. It
// walks the native repo buckets once and never creates a graph-wide snapshot.
func (g *Graph) BackfillWorkspaceSlugs(slugs []WorkspaceSlug) int {
	byRepo := make(map[string]WorkspaceSlug, len(slugs))
	for _, slug := range slugs {
		if slug.RepoPrefix == "" || (slug.Workspace == "" && slug.Project == "") {
			continue
		}
		byRepo[slug.RepoPrefix] = slug
	}
	if len(byRepo) == 0 {
		return 0
	}

	changed := 0
	for _, shard := range g.shards {
		shard.mu.Lock()
		for repoPrefix, slug := range byRepo {
			for _, node := range shard.byRepo[repoPrefix] {
				if node == nil {
					continue
				}
				dirty := false
				if node.WorkspaceID == "" && slug.Workspace != "" {
					node.WorkspaceID = slug.Workspace
					dirty = true
				}
				if node.ProjectID == "" && slug.Project != "" {
					node.ProjectID = slug.Project
					dirty = true
				}
				if dirty {
					changed++
				}
			}
		}
		shard.mu.Unlock()
	}
	return changed
}

var _ WorkspaceSlugBackfiller = (*Graph)(nil)
