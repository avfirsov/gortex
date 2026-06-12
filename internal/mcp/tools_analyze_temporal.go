package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/analyzer"
	"github.com/zzet/gortex/internal/resolver"
)

// handleAnalyzeTemporalOrphans is the queryable face of the Temporal
// call-graph integrity check: broken dispatches (a workflow calls an
// activity/child-workflow that resolves to nothing), signals/queries with
// no handler, and registered activities/workflows nobody dispatches or
// starts. Exposed as `analyze kind=temporal_orphans`.
func (s *Server) handleAnalyzeTemporalOrphans(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rep := resolver.DetectTemporalOrphans(s.graph)
	return s.respondJSONOrTOON(ctx, req, analyzer.OrphanReportToMap(rep))
}
