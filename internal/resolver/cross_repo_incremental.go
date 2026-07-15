package resolver

import (
	"strconv"

	"github.com/zzet/gortex/internal/graph"
)

// DetectCrossRepoEdgesForFiles materializes the cross-repo layer only for base
// edges incident to nodes in the exact changed-file frontier. Inspecting both
// incoming and outgoing edges covers unchanged callers rebound to a changed
// target as well as new calls emitted by the changed source file.
func DetectCrossRepoEdgesForFiles(g graph.Store, filePaths []string) int {
	if g == nil || len(filePaths) == 0 {
		return 0
	}
	baseKinds := make(map[graph.EdgeKind]bool)
	for _, kind := range graph.BaseKindsForCrossRepo() {
		baseKinds[kind] = true
	}
	if len(baseKinds) == 0 {
		return 0
	}

	nodeCache := make(map[string]*graph.Node)
	getNode := func(id string) *graph.Node {
		if node, ok := nodeCache[id]; ok {
			return node
		}
		node := g.GetNode(id)
		nodeCache[id] = node
		return node
	}
	seenEdges := make(map[string]bool)
	emitted := 0
	visit := func(edge *graph.Edge) {
		if edge == nil || !baseKinds[edge.Kind] {
			return
		}
		key := string(edge.Kind) + "\x00" + edge.From + "\x00" + edge.To + "\x00" + edge.FilePath + "\x00" + strconv.Itoa(edge.Line)
		if seenEdges[key] {
			return
		}
		seenEdges[key] = true
		from, to := getNode(edge.From), getNode(edge.To)
		if from == nil || to == nil || from.RepoPrefix == "" || to.RepoPrefix == "" || from.RepoPrefix == to.RepoPrefix {
			return
		}
		crossKind, ok := graph.CrossRepoKindFor(edge.Kind)
		if !ok {
			return
		}
		edge.CrossRepo = true
		g.AddEdge(&graph.Edge{
			From:            edge.From,
			To:              edge.To,
			Kind:            crossKind,
			FilePath:        edge.FilePath,
			Line:            edge.Line,
			Confidence:      edge.Confidence,
			ConfidenceLabel: edge.ConfidenceLabel,
			Origin:          edge.Origin,
			CrossRepo:       true,
			Meta: map[string]any{
				"base_kind":   string(edge.Kind),
				"source_repo": from.RepoPrefix,
				"target_repo": to.RepoPrefix,
			},
		})
		emitted++
	}

	seenNodes := make(map[string]bool)
	seenFiles := make(map[string]bool)
	for _, filePath := range filePaths {
		if filePath == "" || seenFiles[filePath] {
			continue
		}
		seenFiles[filePath] = true
		for _, node := range g.GetFileNodes(filePath) {
			if node == nil || seenNodes[node.ID] {
				continue
			}
			seenNodes[node.ID] = true
			for _, edge := range g.GetOutEdges(node.ID) {
				visit(edge)
			}
			for _, edge := range g.GetInEdges(node.ID) {
				visit(edge)
			}
		}
	}
	return emitted
}
