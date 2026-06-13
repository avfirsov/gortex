package mcp

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/analyzer"
)

// Structured resolver-suppression taxonomy. When the resolver leaves a
// call / reference edge on an `unresolved::` placeholder it records no
// reason — an agent only sees that the edge is unresolved, not *why*.
// These constants are aliases for the canonical values in the analyzer
// package so MCP-layer test code can reference them without an import.
const (
	outcomeAmbiguousMultiMatch = analyzer.OutcomeAmbiguousMultiMatch
	outcomeCandidateOutOfScope = analyzer.OutcomeCandidateOutOfScope
	outcomeCrossLanguageOnly   = analyzer.OutcomeCrossLanguageOnly
	outcomeStubOnly            = analyzer.OutcomeStubOnly
	outcomeNoDefinition        = analyzer.OutcomeNoDefinition
)

// handleAnalyzeResolutionOutcomes classifies every unresolved call /
// reference edge by the structured reason the resolver gave up, and
// returns a per-reason rollup plus example rows. Optional `reason`
// filters to one outcome; optional `limit` caps the example rows.
func (s *Server) handleAnalyzeResolutionOutcomes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	reasonFilter := strings.TrimSpace(stringArg(args, "reason"))
	limit := intArg(args, "limit", 50)

	res := analyzer.AnalyzeResolutionOutcomes(s.graph, reasonFilter, limit)

	if isCompact(req) {
		var b strings.Builder
		reasons := make([]string, 0, len(res.ByReason))
		for r := range res.ByReason {
			reasons = append(reasons, r)
		}
		sort.Slice(reasons, func(i, j int) bool { return res.ByReason[reasons[i]] > res.ByReason[reasons[j]] })
		for _, r := range reasons {
			b.WriteString(r)
			b.WriteString(": ")
			b.WriteString(strconv.Itoa(res.ByReason[r]))
			b.WriteByte('\n')
		}
		if len(res.ByReason) == 0 {
			b.WriteString("no unresolved edges\n")
		}
		return mcp.NewToolResultText(b.String()), nil
	}

	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"by_reason": res.ByReason,
		"total":     res.Total,
		"rows":      res.Rows,
	})
}
