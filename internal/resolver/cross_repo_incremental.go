package resolver

import "github.com/zzet/gortex/internal/graph"

// DetectCrossRepoEdgesForFiles materializes the cross-repo layer only for base
// edges incident to nodes in the exact changed-file frontier. Inspecting both
// incoming and outgoing edges covers unchanged callers rebound to a changed
// target as well as new calls emitted by the changed source file.
func DetectCrossRepoEdgesForFiles(g graph.Store, filePaths []string) int {
	if g == nil || len(filePaths) == 0 {
		return 0
	}
	return materializeCrossRepoCandidates(g, crossRepoCandidatesForFiles(g, filePaths))
}
