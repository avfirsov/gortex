package resolver

import (
	"sort"
	"strconv"

	"github.com/zzet/gortex/internal/graph"
)

func frameworkScopePrefixes(scope map[string]bool) []string {
	if scope == nil {
		return nil
	}
	prefixes := make([]string, 0, len(scope))
	for prefix, enabled := range scope {
		if enabled {
			prefixes = append(prefixes, prefix)
		}
	}
	sort.Strings(prefixes)
	return prefixes
}

func frameworkEdgesByKinds(g graph.Store, kinds ...graph.EdgeKind) []*graph.Edge {
	if g == nil || len(kinds) == 0 {
		return nil
	}
	var out []*graph.Edge
	for _, kind := range kinds {
		for edge := range g.EdgesByKind(kind) {
			if edge != nil {
				out = append(out, edge)
			}
		}
	}
	return out
}

// frameworkRepoEdges returns source-owned edges for an armed partial scope.
// A nil scope preserves the cold/full whole-graph behavior.
func frameworkRepoEdges(g graph.Store, scope map[string]bool, kinds ...graph.EdgeKind) []*graph.Edge {
	if scope == nil {
		return frameworkEdgesByKinds(g, kinds...)
	}
	prefixes := frameworkScopePrefixes(scope)
	rows := graph.ReadRepoEdgesByKinds(g, prefixes, kinds)
	out := make([]*graph.Edge, 0, len(rows))
	for _, row := range rows {
		if row.Edge != nil {
			out = append(out, row.Edge)
		}
	}
	return out
}

func frameworkEdgeIdentity(edge *graph.Edge) string {
	if edge == nil {
		return ""
	}
	return edge.From + "\x00" + edge.To + "\x00" + string(edge.Kind) + "\x00" +
		edge.FilePath + "\x00" + strconv.Itoa(edge.Line)
}

func appendUniqueFrameworkEdges(dst []*graph.Edge, seen map[string]struct{}, edges ...*graph.Edge) []*graph.Edge {
	for _, edge := range edges {
		key := frameworkEdgeIdentity(edge)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, edge)
	}
	return dst
}

// frameworkCallsForScope returns calls sourced by a changed repository plus
// calls targeting changed methods. The latter is the exact reverse dependency
// frontier needed when a partial reindex replaces a method implementation.
func frameworkCallsForScope(g graph.Store, scope map[string]bool) []*graph.Edge {
	if scope == nil {
		return frameworkEdgesByKinds(g, graph.EdgeCalls)
	}
	prefixes := frameworkScopePrefixes(scope)
	seen := make(map[string]struct{})
	var out []*graph.Edge
	for _, row := range graph.ReadRepoEdgesByKinds(g, prefixes, []graph.EdgeKind{graph.EdgeCalls}) {
		out = appendUniqueFrameworkEdges(out, seen, row.Edge)
	}
	methodIDs := graph.ReadRepoNodeIDsByKinds(g, prefixes, []graph.NodeKind{graph.KindMethod})
	for _, incoming := range g.GetInEdgesByNodeIDs(methodIDs) {
		for _, edge := range incoming {
			if edge != nil && edge.Kind == graph.EdgeCalls {
				out = appendUniqueFrameworkEdges(out, seen, edge)
			}
		}
	}
	return out
}
