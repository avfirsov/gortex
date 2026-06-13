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
