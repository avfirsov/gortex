package graph

// SemanticNodeStamp is the compact projection of compiler-derived node
// metadata promoted to dedicated store columns. Empty type fields mean "leave
// unchanged"; SemanticSource is applied whenever either type field is set.
type SemanticNodeStamp struct {
	NodeID         string
	SemanticType   string
	ReturnType     string
	SemanticSource string
}

// SemanticNodeStampWriter is an optional set-oriented store capability.
// PersistSemanticNodeStamps returns the number of existing nodes that received
// a non-empty SemanticType stamp (the provider's coverage count). Backends may
// separately suppress idempotent writes/invalidation, and must batch updates
// rather than issuing one statement per node.
type SemanticNodeStampWriter interface {
	PersistSemanticNodeStamps(updates []SemanticNodeStamp) int
}
