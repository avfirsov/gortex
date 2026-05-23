package graph

// Store is the persistence-and-query backend the rest of gortex sees
// behind the *Graph type. The only implementation today is the
// in-memory *Graph; future implementations will include an on-disk
// embedded-DB backend (local single-binary) and a remote network
// client. The interface is the seam that lets the rest of the
// codebase be backend-agnostic.
//
// The method set deliberately mirrors *Graph's current public API so
// the codebase compiles unchanged the day this interface lands. A few
// notes on shape:
//
//   - Slice-shaped reads (AllNodes / AllEdges / FindNodesByName / …)
//     materialise their result in memory — fine for the in-memory
//     store, but disk / remote backends will want iterator-shaped
//     variants added alongside as those implementations come online.
//
//   - Memory-estimate methods (RepoMemoryEstimate /
//     AllRepoMemoryEstimates) are inherently in-memory specific; disk
//     and remote backends return whatever they can compute and callers
//     treat the result as advisory.
//
//   - *Graph's ResolveMutex() is intentionally NOT on the interface.
//     It's an in-memory implementation detail (the indexer's
//     post-parse resolver uses it for fine-grained coordination) and
//     does not generalise to disk / remote backends. Resolver callers
//     keep operating on *Graph directly until that coordination is
//     reshaped.
type Store interface {
	// --- Writes -----------------------------------------------------

	AddNode(n *Node)
	AddBatch(nodes []*Node, edges []*Edge)
	AddEdge(e *Edge)
	SetEdgeProvenance(e *Edge, newOrigin string) bool
	ReindexEdge(e *Edge, oldTo string)
	RemoveEdge(from, to string, kind EdgeKind) bool
	EvictFile(filePath string) (nodesRemoved, edgesRemoved int)
	EvictRepo(repoPrefix string) (nodesRemoved, edgesRemoved int)

	// --- Point lookups ---------------------------------------------

	GetNode(id string) *Node
	GetNodeByQualName(qualName string) *Node

	// --- Name + scope queries --------------------------------------

	FindNodesByName(name string) []*Node
	FindNodesByNameInRepo(name, repoPrefix string) []*Node
	GetFileNodes(filePath string) []*Node
	GetRepoNodes(repoPrefix string) []*Node

	// --- Edge adjacency --------------------------------------------

	GetOutEdges(nodeID string) []*Edge
	GetInEdges(nodeID string) []*Edge

	// --- Bulk reads ------------------------------------------------

	AllNodes() []*Node
	AllEdges() []*Edge

	// --- Counts and stats ------------------------------------------

	NodeCount() int
	EdgeCount() int
	Stats() GraphStats
	RepoStats() map[string]GraphStats
	RepoPrefixes() []string

	// --- Provenance verification -----------------------------------

	EdgeIdentityRevisions() int
	VerifyEdgeIdentities() error

	// --- Memory estimation (advisory; in-memory-specific) ----------

	RepoMemoryEstimate(repoPrefix string) RepoMemoryEstimate
	AllRepoMemoryEstimates() map[string]RepoMemoryEstimate
}

// Compile-time assertion: *Graph satisfies the Store interface. If a
// *Graph method's signature ever drifts from the interface, the build
// fails fast here instead of at runtime when a different Store
// implementation gets swapped in.
var _ Store = (*Graph)(nil)
