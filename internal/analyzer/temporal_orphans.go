package analyzer

// PURPOSE — thin adapter that converts a resolver.TemporalOrphanReport into
// the map[string]any shape the MCP handler marshals to JSON, so any future
// surface (CLI, HTTP) can produce the same shape without duplicating the
// field-name literals.
// RATIONALE — keeps the MCP handler as a thin wrapper with no structural
// knowledge of the output shape; the map is the single source of truth.
// KEYWORDS — temporal_orphans, adapter, pure, calculation

import "github.com/zzet/gortex/internal/resolver"

// OrphanReportToMap converts a TemporalOrphanReport to the canonical
// map[string]any shape that the MCP handler marshals. JSON field names
// are locked: broken_dispatch, signal_no_handler, query_no_handler,
// orphan_activity, orphan_workflow, totals.
func OrphanReportToMap(rep resolver.TemporalOrphanReport) map[string]any {
	return map[string]any{
		"broken_dispatch":   rep.BrokenDispatch,
		"signal_no_handler": rep.SignalNoHandler,
		"query_no_handler":  rep.QueryNoHandler,
		"orphan_activity":   rep.OrphanActivity,
		"orphan_workflow":   rep.OrphanWorkflow,
		"totals": map[string]int{
			"broken_dispatch":   len(rep.BrokenDispatch),
			"signal_no_handler": len(rep.SignalNoHandler),
			"query_no_handler":  len(rep.QueryNoHandler),
			"orphan_activity":   len(rep.OrphanActivity),
			"orphan_workflow":   len(rep.OrphanWorkflow),
		},
	}
}
