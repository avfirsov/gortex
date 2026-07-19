package graph

// UnresolvedTargetClass is a storage-level shape of an unresolved edge target.
// It deliberately describes only syntax already present in the flat to_id
// column. Resolver policy (language, reachability, terminality, and candidate
// selection) is intentionally outside this diagnostic classification.
type UnresolvedTargetClass string

const (
	UnresolvedTargetBareSymbol     UnresolvedTargetClass = "bare_symbol"
	UnresolvedTargetWildcardMember UnresolvedTargetClass = "wildcard_member"
	UnresolvedTargetImport         UnresolvedTargetClass = "import"
	UnresolvedTargetRelativeImport UnresolvedTargetClass = "relative_import"
	UnresolvedTargetGRPC           UnresolvedTargetClass = "grpc"
	UnresolvedTargetRazorUsing     UnresolvedTargetClass = "razor_using"
	UnresolvedTargetQualified      UnresolvedTargetClass = "qualified_symbol"
	UnresolvedTargetEmpty          UnresolvedTargetClass = "empty"
)

// UnresolvedFrontierBucket is one grouped slice of the pending resolver
// frontier. Count is the number of edges, not distinct target names.
type UnresolvedFrontierBucket struct {
	TargetClass UnresolvedTargetClass
	Kind        EdgeKind
	Count       int64
}

// UnresolvedFrontierStats is a bounded diagnostic summary. QueryCount reports
// the number of backend round trips used to produce the complete summary; disk
// implementations should keep it independent of the number of pending edges.
type UnresolvedFrontierStats struct {
	Pending    int64
	GroupCount int
	QueryCount int
	Buckets    []UnresolvedFrontierBucket
}

// UnresolvedFrontierCounter is an optional disk-backend capability used for
// low-overhead resolver telemetry. Implementations must aggregate in the
// backend and must not materialize the unresolved edges or graph nodes.
type UnresolvedFrontierCounter interface {
	CountUnresolvedFrontier() (UnresolvedFrontierStats, error)
}
