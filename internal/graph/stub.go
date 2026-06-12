package graph

import "strings"

// Stub-node identifier conventions.
//
// A "stub" is a placeholder Node the resolver materialises for a
// symbol the indexer can see referenced but not defined in the
// current repo's source: a stdlib call, a language builtin, an
// external module import, etc. Stubs let the graph hold edges
// to "external" targets uniformly with edges to first-party
// nodes.
//
// Format (all stubs):
//
//	<repoPrefix>::<kind>::<rest>
//
// where:
//
//	repoPrefix — the owning repo's RepoPrefix (Indexer.RepoPrefix).
//	             Empty only when the stub is created outside a
//	             per-repo context (legacy single-repo daemons).
//	kind       — one of: stdlib, builtin, external_call, module.
//	rest       — kind-specific (e.g. "fmt::Errorf" for stdlib).
//
// Why per-repo? Two repos pinned to different language SDK
// versions have semantically distinct stdlib symbols. Go 1.21's
// `min` is a builtin; in 1.20 it isn't. A global `builtin::go::min`
// node would conflate them and produce wrong cross-repo edges.
// Per-repo prefix keeps them as distinct nodes; a future
// "same-as" edge can union them when the workspace knows the
// versions actually match.
const (
	StubKindStdlib       = "stdlib"
	StubKindBuiltin      = "builtin"
	StubKindExternalCall = "external_call"
	StubKindModule       = "module"
)

// StubID composes a stub identifier with the per-repo prefix.
// Pass repoPrefix = "" when the caller is outside a per-repo
// context (single-repo daemons that haven't set a prefix).
func StubID(repoPrefix, kind string, parts ...string) string {
	var b strings.Builder
	if repoPrefix != "" {
		b.WriteString(repoPrefix)
		b.WriteString("::")
	}
	b.WriteString(kind)
	for _, p := range parts {
		b.WriteString("::")
		b.WriteString(p)
	}
	return b.String()
}

// IsStub reports whether id is any stub kind. Cheaper than
// StubKind when callers only need a yes/no.
func IsStub(id string) bool {
	return StubKind(id) != ""
}

// StubKind extracts the stub category (stdlib / builtin /
// external_call / module) from id. Returns "" if id is not a
// stub.
//
// Format dispatch:
//   - "<kind>::<rest>"               — legacy, no repo prefix
//   - "<repo>::<kind>::<rest>"       — per-repo prefix
//
// We match by looking for one of the known kind segments
// anywhere in the first two "::"-separated positions.
func StubKind(id string) string {
	for _, k := range stubKinds {
		// Without repo prefix: "<kind>::..."
		if strings.HasPrefix(id, k+"::") {
			return k
		}
	}
	// With repo prefix: "<repo>::<kind>::..."
	// Find the second "::" segment.
	first := strings.Index(id, "::")
	if first < 0 {
		return ""
	}
	rest := id[first+2:]
	for _, k := range stubKinds {
		if strings.HasPrefix(rest, k+"::") {
			return k
		}
	}
	return ""
}

// stubKinds is the closed set of stub categories. Ordered by
// expected frequency so the lookup loop bails early in the
// common case.
var stubKinds = []string{
	StubKindStdlib,
	StubKindExternalCall,
	StubKindBuiltin,
	StubKindModule,
}

// IsStdlibStub etc are convenience predicates that don't make
// the caller compare StubKind's return against a literal.
func IsStdlibStub(id string) bool       { return StubKind(id) == StubKindStdlib }
func IsBuiltinStub(id string) bool      { return StubKind(id) == StubKindBuiltin }
func IsExternalCallStub(id string) bool { return StubKind(id) == StubKindExternalCall }
func IsModuleStub(id string) bool       { return StubKind(id) == StubKindModule }

// StubRest returns the kind-specific tail of a stub id (the
// portion after "<repo>::<kind>::" or "<kind>::"). Returns "" if
// id is not a stub. Useful for the "fmt::Errorf" portion of a
// stdlib stub when callers need to inspect the symbol identity.
func StubRest(id string) string {
	kind := StubKind(id)
	if kind == "" {
		return ""
	}
	prefix := kind + "::"
	if idx := strings.Index(id, prefix); idx >= 0 {
		return id[idx+len(prefix):]
	}
	return ""
}

// UnresolvedMarker is the prefix the extractor emits for a call/
// reference target the resolver still needs to bind to a concrete
// Node.
//
// Forms:
//
//	unresolved::Name                — legacy / single-repo
//	<repoPrefix>::unresolved::Name  — multi-repo COPY rewrite (in
//	                                   copyBulkLocked, to dodge
//	                                   cross-repo PK collisions)
//
// IsUnresolvedTarget / UnresolvedName / UnresolvedRepoPrefix
// normalise over both shapes so callers (resolver, MCP filters,
// data-flow tracker) don't have to know the encoding.
const UnresolvedMarker = "unresolved::"

// IsUnresolvedTarget reports whether id names an unresolved
// extractor stub in either the bare or the multi-repo form.
func IsUnresolvedTarget(id string) bool {
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, UnresolvedMarker) {
		return true
	}
	return strings.Contains(id, "::"+UnresolvedMarker)
}

// UnresolvedName returns the bare symbol name encoded in an
// unresolved target id, stripping the `unresolved::` prefix (and
// any leading `<repoPrefix>::`). Returns "" when id is not an
// unresolved stub.
func UnresolvedName(id string) string {
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, UnresolvedMarker) {
		return id[len(UnresolvedMarker):]
	}
	idx := strings.Index(id, "::"+UnresolvedMarker)
	if idx < 0 {
		return ""
	}
	return id[idx+len("::"+UnresolvedMarker):]
}

// UnresolvedRepoPrefix returns the per-repo prefix encoded in an
// unresolved target id, or "" if the id is bare or not an
// unresolved stub.
func UnresolvedRepoPrefix(id string) string {
	if id == "" || strings.HasPrefix(id, UnresolvedMarker) {
		return ""
	}
	idx := strings.Index(id, "::"+UnresolvedMarker)
	if idx <= 0 {
		return ""
	}
	return id[:idx]
}

// StubRepoPrefix returns the per-repo prefix of a stub id, or
// "" if the id has no prefix or isn't a stub.
func StubRepoPrefix(id string) string {
	kind := StubKind(id)
	if kind == "" {
		return ""
	}
	// If id starts with the kind directly, there's no repo prefix.
	if strings.HasPrefix(id, kind+"::") {
		return ""
	}
	if idx := strings.Index(id, "::"); idx > 0 {
		return id[:idx]
	}
	return ""
}

// IsResolvableRefEdge reports whether an edge of this kind is a
// symbol-level reference that the resolver binds from an
// `unresolved::<Name>` stub — calls, references, value reads/writes,
// type positions (typed_as / returns), and type hierarchy
// (implements / extends / composes / instantiates). These are the edges
// that must survive a definition's re-index as pending stubs rather than
// be dropped wholesale. Structural edges (contains / defines / member_of
// / imports / param_of) and enrichment edges (tests / provides / spawns
// / annotated / …) are not name-resolved and are excluded — re-stubbing
// them would only create edges nothing ever rebinds.
func IsResolvableRefEdge(k EdgeKind) bool {
	switch k {
	case EdgeCalls, EdgeReferences, EdgeReads, EdgeWrites,
		EdgeTypedAs, EdgeReturns, EdgeInstantiates,
		EdgeImplements, EdgeExtends, EdgeComposes:
		return true
	}
	return false
}

// IsReferenceableSymbol reports whether a node of this kind can be the
// target of a cross-file symbol reference — and thus the subject of
// reverse resolution by name. Excludes files, imports, packages,
// params, closures, locals, builtins, generic params, and the
// coverage / infra node kinds, none of which a caller binds to by bare
// name from an unresolved stub.
func IsReferenceableSymbol(k NodeKind) bool {
	switch k {
	case KindFunction, KindMethod, KindType, KindInterface,
		KindVariable, KindConstant, KindField, KindEnumMember:
		return true
	}
	return false
}
