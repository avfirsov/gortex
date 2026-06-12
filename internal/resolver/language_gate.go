package resolver

import (
	"iter"

	"github.com/zzet/gortex/internal/graph"
)

// graphHasLanguage reports whether the backing store contains any node of
// the given language. Cheap — a LIMIT-1 probe — on stores that implement
// it (the on-disk backend); conservatively returns true on stores that don't, so a
// language-gated pass still runs rather than being silently skipped. Lets
// the Go / Python attribution passes skip a graph that has none of their
// language instead of scanning + discarding the whole node/edge set.
func (r *Resolver) graphHasLanguage(lang string) bool {
	if hl, ok := r.graph.(interface{ HasLanguage(string) bool }); ok {
		return hl.HasLanguage(lang)
	}
	return true
}

// nodesByKindLang yields nodes of the given kind AND language, pushed
// server-side when the store supports it (so only the matching language's
// nodes cross the cgo boundary), else NodesByKind + an in-Go language
// filter (memory / overlay are already in-memory, so there is no marshal
// cost to push down).
func (r *Resolver) nodesByKindLang(kind graph.NodeKind, lang string) iter.Seq[*graph.Node] {
	if nl, ok := r.graph.(interface {
		NodesByKindLang(graph.NodeKind, string) iter.Seq[*graph.Node]
	}); ok {
		return nl.NodesByKindLang(kind, lang)
	}
	return func(yield func(*graph.Node) bool) {
		for n := range r.graph.NodesByKind(kind) {
			if n != nil && n.Language == lang {
				if !yield(n) {
					return
				}
			}
		}
	}
}
