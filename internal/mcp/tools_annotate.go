package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// annotateInput is one entry in the annotate_nodes `annotations` array:
// a gortex node id plus the optional UA-produced semantic fields. Every
// ua_* field is a pointer so an omitted field (nil) is distinguishable
// from an explicit empty value — only present fields are merged into the
// node's Meta, never a zero value that would clobber a prior annotation.
type annotateInput struct {
	ID           string   `json:"id"`
	UASummary    *string  `json:"ua_summary,omitempty"`
	UATags       []string `json:"ua_tags,omitempty"`
	UAComplexity *float64 `json:"ua_complexity,omitempty"`
	UADomain     *string  `json:"ua_domain,omitempty"`
}

// metaKV builds the namespaced Meta key/value map from the present ua_*
// fields. Keys are namespaced (ua_*) so a merge can never shadow an
// indexer-owned Meta key. An entry with only an id (no ua_* fields)
// yields an empty map — MergeNodeMeta then reports the node found but
// unchanged, which the handler counts as "unchanged".
func (a annotateInput) metaKV() map[string]any {
	kv := make(map[string]any, 4)
	if a.UASummary != nil {
		kv["ua_summary"] = *a.UASummary
	}
	if len(a.UATags) > 0 {
		// Store as []any so the value round-trips identically through the
		// gob snapshot and reflect.DeepEqual idempotency check (a JSON
		// re-decode of a stored []string would come back []any).
		tags := make([]any, len(a.UATags))
		for i, t := range a.UATags {
			tags[i] = t
		}
		kv["ua_tags"] = tags
	}
	if a.UAComplexity != nil {
		kv["ua_complexity"] = *a.UAComplexity
	}
	if a.UADomain != nil {
		kv["ua_domain"] = *a.UADomain
	}
	return kv
}

// registerAnnotateTools wires the L3 UA→gortex write-back tool
// annotate_nodes onto the MCP tool surface. Registering it here is
// sufficient to also serve POST /v1/tools/annotate_nodes: the daemon's
// HTTP handler dispatches against this same MCP registry, so no separate
// HTTP code is needed. It mirrors registerUnderstandTools and is called
// from NewServer beside it.
func (s *Server) registerAnnotateTools() {
	s.addTool(
		mcp.NewTool("annotate_nodes",
			mcp.WithDescription("Write UA-produced semantics back into existing graph nodes by id: merges namespaced ua_summary / ua_tags / ua_complexity / ua_domain into each node's Meta (idempotent, additive — never mutates structural data) and optionally adds semantically_related edges. Returns {annotated, unchanged, not_found, edges_added}."),
			mcp.WithString("annotations", mcp.Required(), mcp.Description(`JSON array of per-node annotations: [{"id":"<node id>","ua_summary":"...","ua_tags":["..."],"ua_complexity":0.0,"ua_domain":"..."}]. Every ua_* field is optional; only present fields are merged.`)),
			mcp.WithString("add_related", mcp.Description(`Optional JSON array of semantically_related edge pairs: [["idA","idB",0.8]]. The third element (score, 0..1) is optional and defaults to 0.5.`)),
		),
		s.handleAnnotateNodes,
	)
}

// handleAnnotateNodes is the Action-layer controller for annotate_nodes.
// It parses the annotations + add_related JSON arguments, merges each
// node's ua_* semantics via the shard-locked graph.Store.MergeNodeMeta
// (the only sanctioned Meta-mutation path), adds any requested
// semantically_related edges via the idempotent AddEdge, and returns the
// {annotated, unchanged, not_found, edges_added} summary.
//
// Failure handling is deliberately non-fatal per item: a node id that
// is not in the graph is recorded in not_found and the batch continues
// — one bad id never fails the whole call. Structural data is never
// touched: the only writes are additive ua_* Meta keys and
// semantically_related edges.
func (s *Server) handleAnnotateNodes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g := s.graph
	if g == nil {
		return mcp.NewToolResultError("annotate_nodes: graph is not initialised"), nil
	}
	args := req.GetArguments()

	// --- Parse the annotations array (required) ---------------------
	rawAnnotations := stringArg(args, "annotations")
	if rawAnnotations == "" {
		return mcp.NewToolResultError("annotate_nodes: 'annotations' is required (a JSON array of {id, ua_*})"), nil
	}
	var annotations []annotateInput
	if err := json.Unmarshal([]byte(rawAnnotations), &annotations); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("annotate_nodes: 'annotations' is not valid JSON: %v", err)), nil
	}

	// --- Merge each node's ua_* semantics ---------------------------
	annotated := 0
	unchanged := 0
	notFound := make([]string, 0)
	for _, a := range annotations {
		if a.ID == "" {
			// An entry with no id can't address a node — treat as a
			// not-found skip (recorded under the empty id) rather than
			// failing the batch.
			notFound = append(notFound, "")
			continue
		}
		changed, found := g.MergeNodeMeta(a.ID, a.metaKV())
		switch {
		case !found:
			notFound = append(notFound, a.ID)
		case changed:
			annotated++
		default:
			unchanged++
		}
	}

	// --- Add optional semantically_related edges --------------------
	edgesAdded := 0
	if rawRelated := stringArg(args, "add_related"); rawRelated != "" {
		var pairs [][]any
		if err := json.Unmarshal([]byte(rawRelated), &pairs); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("annotate_nodes: 'add_related' is not valid JSON: %v", err)), nil
		}
		for _, p := range pairs {
			if len(p) < 2 {
				continue
			}
			a, aok := p[0].(string)
			b, bok := p[1].(string)
			if !aok || !bok || a == "" || b == "" {
				continue
			}
			score := scoreOrDefault(p)
			g.AddEdge(&graph.Edge{
				From:       a,
				To:         b,
				Kind:       graph.EdgeSemanticallyRelated,
				Confidence: score,
				Origin:     "ua_annotated",
				Meta:       map[string]any{"similarity": score},
			})
			edgesAdded++
		}
	}

	if s.logger != nil {
		s.logger.Info("annotate_nodes",
			zap.Int("annotated", annotated),
			zap.Int("unchanged", unchanged),
			zap.Int("not_found", len(notFound)),
			zap.Int("edges_added", edgesAdded),
		)
	}

	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"annotated":   annotated,
		"unchanged":   unchanged,
		"not_found":   notFound,
		"edges_added": edgesAdded,
	})
}

// scoreOrDefault extracts the optional similarity score (third element)
// from an add_related pair. JSON numbers decode as float64; a missing or
// non-numeric third element falls back to 0.5 — the same default the
// graph-diffusion pass uses for an unscored semantically_related edge.
func scoreOrDefault(pair []any) float64 {
	if len(pair) >= 3 {
		if f, ok := pair[2].(float64); ok {
			return f
		}
	}
	return 0.5
}
