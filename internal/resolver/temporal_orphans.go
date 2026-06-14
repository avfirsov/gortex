package resolver

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// TemporalOrphan names one side of a Temporal contract that has no
// counterpart in the graph.
type TemporalOrphan struct {
	From string `json:"from"`           // the dispatching / sending node
	Kind string `json:"kind"`           // activity / workflow / signal / query
	Name string `json:"name"`           // the dispatched / signalled / queried name
	File string `json:"file,omitempty"` // call-site file, when known
	Line int    `json:"line,omitempty"`
}

// TemporalOrphanReport is the result of DetectTemporalOrphans. Each list
// is a different integrity gap in the Temporal call graph.
type TemporalOrphanReport struct {
	// BrokenDispatch: a workflow dispatches an activity / child workflow
	// whose name resolves to nothing (the temporal.stub edge is still a
	// placeholder). Almost always an error — a broken or renamed call.
	BrokenDispatch []TemporalOrphan `json:"broken_dispatch"`
	// SignalNoHandler / QueryNoHandler: a signal is sent / a query is
	// called with a name no workflow handles. An error (sending into the
	// void).
	SignalNoHandler []TemporalOrphan `json:"signal_no_handler"`
	QueryNoHandler  []TemporalOrphan `json:"query_no_handler"`
	// OrphanActivity / OrphanWorkflow: a registered activity / workflow
	// nobody dispatches or starts. A warning — dead code or unfinished.
	OrphanActivity []string `json:"orphan_activity"`
	OrphanWorkflow []string `json:"orphan_workflow"`
}

// DetectTemporalOrphans walks the resolved Temporal graph and reports the
// four integrity gaps above. Read-only.
func DetectTemporalOrphans(g graph.Store) TemporalOrphanReport {
	var rep TemporalOrphanReport
	if g == nil {
		return rep
	}

	// Signal/query handler name sets (providers).
	signalHandled := map[string]bool{}
	queryHandled := map[string]bool{}
	// Activities/workflows that have at least one resolved inbound
	// dispatch (consumed).
	consumed := map[string]bool{}

	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		via, _ := e.Meta["via"].(string)
		kind, _ := e.Meta["temporal_kind"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		switch via {
		case "temporal.handler":
			switch kind {
			case "signal":
				signalHandled[name] = true
			case "query":
				queryHandled[name] = true
			}
		case "temporal.stub":
			if strings.HasPrefix(e.To, temporalStubPrefix) {
				// P0: a dispatch whose call site is a test file is almost
				// always a fixture/mock (handler is a stub or lives in
				// another repo); counting it as a broken_dispatch is the
				// dominant false positive. Skip it — keyed on the edge's
				// own FilePath (the dispatcher), so it's robust under both
				// full and incremental reindex (no Node.Meta dependency).
				if isTestFilePath(e.FilePath) {
					continue
				}
				// FP: short lowercase bare identifiers (< 3 chars) with no
				// resolution metadata are local variables (activity options,
				// IDs, etc.), not activity/workflow names. The parser emits
				// a stub edge for every positional arg of ExecuteActivity,
				// and short names like "ao", "id", "fn" that lack const/
				// function/param/field metadata are false positives.
				if isShortLowercaseFP(name, e.Meta) {
					continue
				}
				rep.BrokenDispatch = append(rep.BrokenDispatch, TemporalOrphan{
					From: e.From, Kind: kind, Name: name, File: e.FilePath, Line: e.Line,
				})
			} else if e.To != "" {
				consumed[e.To] = true
			}
		}
	}

	// Second pass for senders/callers now that the handler sets are known.
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		via, _ := e.Meta["via"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		switch via {
		case "temporal.signal-send":
			if name != "" && !signalHandled[name] {
				rep.SignalNoHandler = append(rep.SignalNoHandler, TemporalOrphan{
					From: e.From, Kind: "signal", Name: name, File: e.FilePath, Line: e.Line,
				})
			}
		case "temporal.query-call":
			if name != "" && !queryHandled[name] {
				rep.QueryNoHandler = append(rep.QueryNoHandler, TemporalOrphan{
					From: e.From, Kind: "query", Name: name, File: e.FilePath, Line: e.Line,
				})
			}
		}
	}

	// Registered-but-unconsumed activities / workflows. Only Go nodes
	// carry temporal_role from a worker.Register* call; an activity with
	// no resolved inbound dispatch is dead.
	checkOrphanRole := func(n *graph.Node) {
		if n == nil || n.Language != "go" {
			return
		}
		role, _ := n.Meta["temporal_role"].(string)
		switch role {
		case "activity":
			if !consumed[n.ID] {
				rep.OrphanActivity = append(rep.OrphanActivity, n.ID)
			}
		case "workflow":
			if !consumed[n.ID] {
				rep.OrphanWorkflow = append(rep.OrphanWorkflow, n.ID)
			}
		}
	}
	for n := range g.NodesByKind(graph.KindFunction) {
		checkOrphanRole(n)
	}
	for n := range g.NodesByKind(graph.KindMethod) {
		checkOrphanRole(n)
	}
	return rep
}

// isShortLowercaseFP reports whether a dispatch name is a short (< 3 chars)
// all-lowercase bare identifier with no resolution metadata — a false
// positive from the parser treating a local variable (activity options,
// IDs, etc.) as a dispatch-name carrier. Names that have const, function,
// param, or field metadata are kept since those are proven dispatch-name
// carriers even when short.
func isShortLowercaseFP(name string, meta map[string]any) bool {
	if len(name) >= 3 || name == "" {
		return false
	}
	for _, ch := range name {
		if ch < 'a' || ch > 'z' {
			return false
		}
	}
	if _, ok := meta["temporal_default_const"]; ok {
		return false
	}
	if _, ok := meta["temporal_name_func"]; ok {
		return false
	}
	if _, ok := meta["temporal_name_param"]; ok {
		return false
	}
	if _, ok := meta["temporal_name_field"]; ok {
		return false
	}
	return true
}
