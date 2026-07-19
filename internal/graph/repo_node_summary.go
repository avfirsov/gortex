package graph

// RepoLanguageNodeSummaryReader is an optional store capability for compiler
// matching and other repo-scoped algorithms that need only node identity and
// source location. Implementations must apply both predicates in the backend
// and must not materialize Meta, including promoted docs and signatures.
// Returned nodes are read-only projections and must never be written back.
type RepoLanguageNodeSummaryReader interface {
	GetRepoNodeSummariesByLanguage(repoPrefix, language string) []*Node
}
