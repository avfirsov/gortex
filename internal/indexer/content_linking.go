package indexer

import (
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/artifacts"
	"github.com/zzet/gortex/internal/graph"
)

// contentLinkMinEdgeBudget is the floor on EdgeMotivates the doc->code linker
// may add. Above it the budget scales to 10% of the live edge count, so the
// derived why-layer can never approach doubling the graph.
const contentLinkMinEdgeBudget = 2000

// contentLinkEdgeBudget is the single authority bounding how many EdgeMotivates
// edges the content->code linker emits: max(2000, 10% of live edges).
func contentLinkEdgeBudget(edgeCount int) int {
	if scaled := edgeCount / 10; scaled > contentLinkMinEdgeBudget {
		return scaled
	}
	return contentLinkMinEdgeBudget
}

// linkContentToCode mints EdgeMotivates edges from content chunks (pdf / office
// / text KindDoc tagged data_class=content) to the code symbols their text
// names, reusing the artifact reference scanner (whole-token match, 4-char
// floor, 200 refs/chunk, 1 MiB scan cap). Same-repo edges are emitted here;
// because EdgeMotivates is registered in BaseKindsForCrossRepo, the
// DetectCrossRepoEdges pass that runs next mints the cross_repo_motivates
// parallel for any edge spanning a repo boundary. Bounded by a single edge
// budget so the why-layer cannot bloat the graph; over budget, it stops and
// logs rather than silently truncating.
func (idx *Indexer) linkContentToCode() {
	g := idx.graph
	if g == nil {
		return
	}
	nameIndex := artifacts.SymbolNameIndex(g, "") // union across all repos
	if len(nameIndex) == 0 {
		return
	}
	budget := contentLinkEdgeBudget(g.EdgeCount())

	var edges []*graph.Edge
	added := 0
	truncated := false

	// emit scans one chunk's FULL body for symbol references and mints the
	// EdgeMotivates edges, honouring the single edge budget. Returns false
	// when the budget is exhausted so the caller stops the scan.
	emit := func(chunkID, filePath, text string) bool {
		if text == "" {
			return true
		}
		signal := mineDocSignal(text)
		for _, sym := range artifacts.ScanSymbolRefs([]byte(text), nameIndex) {
			if sym == chunkID {
				continue
			}
			if added >= budget {
				truncated = true
				return false
			}
			edges = append(edges, &graph.Edge{
				From: chunkID, To: sym, Kind: graph.EdgeMotivates,
				FilePath: filePath, Origin: graph.OriginTextMatched,
				Meta: map[string]any{"signal": signal},
			})
			added++
		}
		return true
	}

	if cs := idx.contentSearcher(); cs != nil {
		// Disk path: read FULL section bodies from the content index — the
		// graph node keeps only a snippet — streamed so a content-heavy
		// repo's hundreds of thousands of sections never materialise here.
		// contentSearcher() resolves the disk sink even while idx.graph is
		// the in-memory shadow (where the content rows were streamed to disk).
		_ = cs.ScanContent(idx.repoPrefix, func(nodeID, filePath, body string) bool {
			return emit(nodeID, filePath, body)
		})
	} else {
		// In-memory fallback: with no content index the full text is still on
		// the nodes (streamContentSections leans only when a content searcher
		// exists). Prefer the compound repo+content predicate; adapter stores
		// still enumerate only KindDoc rows and never snapshot the node corpus.
		visit := func(chunk *graph.Node) bool {
			if !graph.IsContentNode(chunk) || chunk.RepoPrefix != idx.repoPrefix {
				return true
			}
			text, _ := chunk.Meta["section_text"].(string)
			return emit(chunk.ID, chunk.FilePath, text)
		}
		if reader, ok := g.(graph.ContentNodeReader); ok {
			for _, chunk := range reader.GetRepoContentNodes(idx.repoPrefix) {
				if !visit(chunk) {
					break
				}
			}
		} else {
			for chunk := range g.NodesByKind(graph.KindDoc) {
				if !visit(chunk) {
					break
				}
			}
		}
	}

	if len(edges) > 0 {
		g.AddBatch(nil, edges)
	}
	if idx.logger == nil {
		return
	}
	if truncated {
		idx.logger.Warn("indexer: content->code linking hit the edge budget; remaining links dropped",
			zap.Int("budget", budget), zap.Int("emitted", added))
	} else if added > 0 {
		idx.logger.Info("indexer: content->code why-links emitted (global)", zap.Int("edges", added))
	}
}
