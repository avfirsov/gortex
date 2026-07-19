package goanalysis

import (
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// fileFuncIndex answers "which function/method contains this line" for one
// file. resolveGoUse asks that question once per identifier use — hundreds of
// thousands of times on a large module — and the linear scan over the file's
// node slice was a flat 28.8s per profiling window. The index holds the
// file's Function/Method nodes sorted by StartLine plus a prefix-max of
// EndLine, so a lookup is a binary search and a short leftward walk that
// stops as soon as no earlier-starting function can still span the line.
type fileFuncIndex struct {
	funcs        []*graph.Node
	prefixMaxEnd []int
}

// buildFileFuncIndexes precomputes one index per file from the repo-scoped
// node snapshot. Cost is one filter+sort per file, paid once per enrichment
// pass, against per-use lookups during both package walks.
func buildFileFuncIndexes(nodesByFile map[string][]*graph.Node) map[string]*fileFuncIndex {
	out := make(map[string]*fileFuncIndex, len(nodesByFile))
	for file, nodes := range nodesByFile {
		funcs := make([]*graph.Node, 0, len(nodes))
		for _, n := range nodes {
			if n != nil && (n.Kind == graph.KindFunction || n.Kind == graph.KindMethod) {
				funcs = append(funcs, n)
			}
		}
		if len(funcs) == 0 {
			continue
		}
		sort.Slice(funcs, func(i, j int) bool {
			if funcs[i].StartLine != funcs[j].StartLine {
				return funcs[i].StartLine < funcs[j].StartLine
			}
			if funcs[i].EndLine != funcs[j].EndLine {
				return funcs[i].EndLine < funcs[j].EndLine
			}
			return funcs[i].ID < funcs[j].ID
		})
		prefixMaxEnd := make([]int, len(funcs))
		maxEnd := funcs[0].EndLine
		for i, n := range funcs {
			if n.EndLine > maxEnd {
				maxEnd = n.EndLine
			}
			prefixMaxEnd[i] = maxEnd
		}
		out[file] = &fileFuncIndex{funcs: funcs, prefixMaxEnd: prefixMaxEnd}
	}
	return out
}

// containing returns the smallest function/method whose [StartLine, EndLine]
// spans line — the same smallest-containing policy as
// findContainingFuncInNodes, which remains the semantic reference. Among
// equal-size containing spans the reference's winner follows store slice
// order (already arbitrary); this picks deterministically by the sort above.
func (ix *fileFuncIndex) containing(line int) *graph.Node {
	if ix == nil || len(ix.funcs) == 0 {
		return nil
	}
	idx := sort.Search(len(ix.funcs), func(i int) bool {
		return ix.funcs[i].StartLine > line
	}) - 1
	var best *graph.Node
	bestSize := int(^uint(0) >> 1)
	for i := idx; i >= 0; i-- {
		// No function at or before i ends on/after line: nothing further
		// left can contain it either.
		if ix.prefixMaxEnd[i] < line {
			break
		}
		n := ix.funcs[i]
		if n.StartLine <= line && line <= n.EndLine {
			if size := n.EndLine - n.StartLine; size < bestSize {
				best = n
				bestSize = size
			}
		}
	}
	return best
}
