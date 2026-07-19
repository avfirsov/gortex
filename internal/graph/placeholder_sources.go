package graph

import "strings"

// From-side placeholder reconciliation.
//
// Extractors key dataflow edges (arg_of, value_flow) FROM the same
// `unresolved::` placeholder string a sibling reference edge carries as its
// target. The resolver rewrites unresolved TARGETS, but nothing ever
// re-pointed the from-side placeholders — a production audit found ~160k
// dataflow edges permanently stuck on placeholder sources, invisible to
// taint/flow queries. When a resolution batch rewrites `unresolved::X → N`
// at a site, the dataflow edges keyed from that placeholder AT THE SAME
// file+line re-point to N in the same breath.
//
// The site match is deliberately EXACT: placeholder strings (e.g.
// `repo/unresolved::*.id`) are shared across many sites, and different
// sites resolve to different nodes — a blanket from==placeholder repoint
// would mis-bind. Non-matching placeholders simply stay pending for the
// pass that resolves their own site.

// PlaceholderRepoint names one eligible rewrite: dataflow edges from
// OldFrom at (FilePath, Line) move to NewFrom.
type PlaceholderRepoint struct {
	OldFrom  string
	FilePath string
	Line     int
	NewFrom  string
}

// PlaceholderSourceKind reports whether kind participates in from-side
// placeholder reconciliation. Deliberately narrow: the audit found exactly
// these two classes stuck.
func PlaceholderSourceKind(kind EdgeKind) bool {
	return kind == EdgeArgOf || kind == EdgeValueFlow
}

// PlaceholderSourceRepoints extracts the eligible repoints from a
// resolution reindex batch: entries whose OldTo was an unresolved
// placeholder and whose edge now carries a real target.
//
// Two placeholder string conventions coexist for the SAME site: reference
// targets stay in the bare `unresolved::X` form, while applyRepoPrefix
// prefixes dataflow SOURCES like node IDs — `<repo>/unresolved::X` (a form
// IsUnresolvedTarget does not even classify, which is precisely why no pass
// ever touched these edges). Each eligible entry therefore emits a repoint
// per candidate form; probing an absent form is one empty adjacency lookup.
func PlaceholderSourceRepoints(reindexes []EdgeReindex) []PlaceholderRepoint {
	var out []PlaceholderRepoint
	for _, r := range reindexes {
		if r.Edge == nil || r.OldTo == "" {
			continue
		}
		if !IsUnresolvedTarget(r.OldTo) || IsUnresolvedTarget(r.Edge.To) || r.Edge.To == "" {
			continue
		}
		out = append(out, PlaceholderRepoint{
			OldFrom:  r.OldTo,
			FilePath: r.Edge.FilePath,
			Line:     r.Edge.Line,
			NewFrom:  r.Edge.To,
		})
		if prefix := RepoPrefixOfID(r.Edge.From); prefix != "" && strings.HasPrefix(r.OldTo, UnresolvedMarker) {
			out = append(out, PlaceholderRepoint{
				OldFrom:  prefix + "/" + r.OldTo,
				FilePath: r.Edge.FilePath,
				Line:     r.Edge.Line,
				NewFrom:  r.Edge.To,
			})
		}
	}
	return out
}

// ReconcilePlaceholderSources applies the repoints through the store's
// existing contracts: ONE batched adjacency fetch for every distinct
// placeholder (point lookups are forbidden inside a resolver pass — the
// interleave cache contract counts them as leaks), then a from-side
// ReindexEdges batch (EdgeReindex.OldFrom exists precisely for source
// moves), so both backends inherit correct index/bucket maintenance.
// Returns the number of dataflow edges re-pointed.
func ReconcilePlaceholderSources(g Store, repoints []PlaceholderRepoint) int {
	if g == nil || len(repoints) == 0 {
		return 0
	}
	froms := make([]string, 0, len(repoints))
	seen := make(map[string]struct{}, len(repoints))
	for _, rp := range repoints {
		if rp.OldFrom == "" || rp.NewFrom == "" || rp.OldFrom == rp.NewFrom {
			continue
		}
		if _, dup := seen[rp.OldFrom]; dup {
			continue
		}
		seen[rp.OldFrom] = struct{}{}
		froms = append(froms, rp.OldFrom)
	}
	if len(froms) == 0 {
		return 0
	}
	outByFrom := OutEdgesForNodes(g, froms)
	var batch []EdgeReindex
	for _, rp := range repoints {
		if rp.OldFrom == "" || rp.NewFrom == "" || rp.OldFrom == rp.NewFrom {
			continue
		}
		for _, e := range outByFrom[rp.OldFrom] {
			if e == nil || !PlaceholderSourceKind(e.Kind) {
				continue
			}
			// A duplicate repoint (two references at one site resolving in
			// the same batch) must not double-move an edge the first
			// repoint already claimed.
			if e.From != rp.OldFrom {
				continue
			}
			if e.FilePath != rp.FilePath || e.Line != rp.Line {
				continue
			}
			oldTo, oldKind := e.To, e.Kind
			e.From = rp.NewFrom
			// RefreshIdentity routes both backends through their from-aware
			// identity repair — the default reindex path only handles
			// target moves.
			batch = append(batch, EdgeReindex{
				Edge:            e,
				OldFrom:         rp.OldFrom,
				OldTo:           oldTo,
				OldKind:         oldKind,
				OldFilePath:     e.FilePath,
				OldLine:         e.Line,
				RefreshIdentity: true,
			})
		}
	}
	if len(batch) == 0 {
		return 0
	}
	g.ReindexEdges(batch)
	return len(batch)
}
