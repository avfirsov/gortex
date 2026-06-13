package mcp

import (
	"context"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/analyzer"
)

// handleAnalyzeSynthesizers rolls up the framework dynamic-dispatch
// synthesizer engine's output: every edge carrying a `synthesized_by`
// provenance marker, grouped by the synthesizer that produced it. This
// is the queryable face of the engine — "which framework-dispatch passes
// fired, and how many edges did each materialise?" — letting an agent
// audit the heuristic, framework-wired call edges (gRPC stub → handler,
// Temporal proxy → activity, event-channel emit → listener, native
// bridge call → implementation) separately from compiler-verified ones.
//
// Optional `name` filters to a single synthesizer.
func (s *Server) handleAnalyzeSynthesizers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	nameFilter := strings.TrimSpace(stringArg(args, "name"))

	var opts []analyzer.SynthesizersOption
	if nameFilter != "" {
		opts = append(opts, analyzer.WithSynthesizerNameFilter(nameFilter))
	}
	res := analyzer.AnalyzeSynthesizers(s.graph, opts...)

	if isCompact(req) {
		var b strings.Builder
		for _, r := range res.Synthesizers {
			b.WriteString(r.Name)
			b.WriteString(": ")
			b.WriteString(strconv.Itoa(r.Edges))
			b.WriteString(" edges (")
			b.WriteString(r.Provenance)
			b.WriteString(")\n")
		}
		if len(res.Synthesizers) == 0 {
			b.WriteString("no synthesized edges\n")
		}
		return mcp.NewToolResultText(b.String()), nil
	}

	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"synthesizers": res.Synthesizers,
		"total_edges":  res.TotalEdges,
	})
}
