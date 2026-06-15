package mcp

import (
	"sort"

	"github.com/zzet/gortex/internal/analysis"
)

// GNX-3: token-economy on the blast-radius output. byDepthCounts is the
// headline an agent usually wants ("47 affected, 3 at depth-1") rather than 47
// rows; pageByDepth then serves the full rows on demand with offset / limit.

// byDepthCounts collapses the per-depth impact entries to a depth → count map.
func byDepthCounts(byDepth map[int][]analysis.ImpactEntry) map[int]int {
	out := make(map[int]int, len(byDepth))
	for d, entries := range byDepth {
		out[d] = len(entries)
	}
	return out
}

// pageByDepth flattens the per-depth entries in depth order, skips `offset`,
// and returns at most `limit` of them regrouped by depth, along with the count
// returned and whether the page was truncated. limit <= 0 means "no paging".
func pageByDepth(byDepth map[int][]analysis.ImpactEntry, offset, limit int) (paged map[int][]analysis.ImpactEntry, returned int, truncated bool) {
	total := 0
	depths := make([]int, 0, len(byDepth))
	for d, entries := range byDepth {
		depths = append(depths, d)
		total += len(entries)
	}
	sort.Ints(depths)

	if limit <= 0 && offset <= 0 {
		return byDepth, total, false
	}

	out := make(map[int][]analysis.ImpactEntry)
	skipped, taken := 0, 0
	for _, d := range depths {
		for _, e := range byDepth[d] {
			if skipped < offset {
				skipped++
				continue
			}
			if limit > 0 && taken >= limit {
				return out, taken, true
			}
			out[d] = append(out[d], e)
			taken++
		}
	}
	return out, taken, offset+taken < total
}

// applyImpactDepthPaging mutates an impact response map in place: it always
// adds by_depth_counts, and either drops the heavy by_depth rows
// (summary_only) or replaces them with a paged window (offset / limit).
func applyImpactDepthPaging(result map[string]any, byDepth map[int][]analysis.ImpactEntry, summaryOnly bool, offset, limit int) {
	result["by_depth_counts"] = byDepthCounts(byDepth)
	if summaryOnly {
		delete(result, "by_depth")
		return
	}
	paged, returned, truncated := pageByDepth(byDepth, offset, limit)
	result["by_depth"] = paged
	if truncated || offset > 0 {
		result["by_depth_returned"] = returned
		result["by_depth_truncated"] = truncated
		result["by_depth_offset"] = offset
	}
}
