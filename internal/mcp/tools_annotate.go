package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// defaultAnnotateNamespace is the key prefix applied to merged metadata
// when the caller does not pass an explicit `namespace`. "ext" reads as
// "external" — metadata produced outside the indexer (an agent, an LLM,
// a downstream tool) — and keeps annotation keys clearly distinct from
// the indexer-owned Meta keys.
const defaultAnnotateNamespace = "ext"

// annotateInput is one entry in the annotate_nodes `annotations` array:
// a graph node id plus a free-form metadata map to merge onto it. The
// map is intentionally untyped — callers write whatever
// external/agent-produced fields they like (summary, tags, complexity,
// domain, …); only the keys actually present are merged, so an omitted
// field never clobbers a prior annotation.
type annotateInput struct {
	ID   string         `json:"id"`
	Meta map[string]any `json:"meta,omitempty"`
}

// namespacedKV builds the Meta key/value map that is merged into a node,
// prefixing every key with "<namespace>_". The prefix is what preserves
// the no-clobber invariant: indexer-owned Meta keys are unprefixed, so a
// namespaced annotation can never shadow one by accident. A key that
// already carries the prefix is left as-is, so a caller may pass either
// the bare ("summary") or fully-qualified ("ext_summary") form.
//
// An entry with an empty Meta map yields nil — MergeNodeMeta then reports
// the node found but unchanged, which the handler counts as "unchanged".
//
// Values are stored exactly as JSON decoded them (string, float64 for
// numbers, []any for arrays, map[string]any for objects). That is the
// shape MergeNodeMeta's reflect.DeepEqual idempotency check expects, so
// re-merging an identical annotation is a stable no-op.
func (a annotateInput) namespacedKV(namespace string) map[string]any {
	if len(a.Meta) == 0 {
		return nil
	}
	prefix := namespace + "_"
	kv := make(map[string]any, len(a.Meta))
	for k, v := range a.Meta {
		if k == "" {
			continue
		}
		key := k
		if !strings.HasPrefix(key, prefix) {
			key = prefix + key
		}
		kv[key] = v
	}
	return kv
}

// registerAnnotateTools wires the annotate_nodes write-back tool onto the
// MCP tool surface. Registering it here is sufficient to also serve
// POST /v1/tools/annotate_nodes: the daemon's HTTP handler dispatches
// against this same MCP registry, so no separate HTTP code is needed.
func (s *Server) registerAnnotateTools() {
	s.addTool(
		mcp.NewTool("annotate_nodes",
			mcp.WithDescription("Merge external / agent-produced metadata back into existing graph nodes by id. Each annotation's `meta` keys are namespaced (default `ext_`) before merging, so they can never overwrite indexer-owned Meta — the merge is idempotent and additive and never mutates structural data (id/kind/name/path/lines). Optionally adds semantically_related edges between node pairs. Returns {annotated, unchanged, not_found, edges_added}."),
			mcp.WithString("annotations", mcp.Required(), mcp.Description(`JSON array of per-node annotations: [{"id":"<node id>","meta":{"summary":"...","tags":["..."],"complexity":0.7,"domain":"..."}}]. The meta object is free-form; only present keys are merged, and each key is namespaced before the merge.`)),
			mcp.WithString("namespace", mcp.Description(`Key namespace prefix for merged meta (default "ext"). Every meta key is stored as "<namespace>_<key>" so annotations never shadow indexer-owned keys. A key already carrying the prefix is left as-is.`)),
			mcp.WithString("add_related", mcp.Description(`Optional JSON array of semantically_related edge pairs: [["idA","idB",0.8]]. The third element (score, 0..1) is optional and defaults to 0.5.`)),
		),
		s.handleAnnotateNodes,
	)
}

// handleAnnotateNodes is the Action-layer controller for annotate_nodes.
// It parses the annotations + add_related JSON arguments, merges each
// node's namespaced metadata via the shard-locked graph.Store.MergeNodeMeta
// (the only sanctioned Meta-mutation path), adds any requested
// semantically_related edges via the idempotent AddEdge, and returns the
// {annotated, unchanged, not_found, edges_added} summary.
//
// Failure handling is deliberately non-fatal per item: a node id that
// is not in the graph is recorded in not_found and the batch continues
// — one bad id never fails the whole call. Structural data is never
// touched: the only writes are additive namespaced Meta keys and
// semantically_related edges.
func (s *Server) handleAnnotateNodes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g := s.graph
	if g == nil {
		return mcp.NewToolResultError("annotate_nodes: graph is not initialised"), nil
	}
	args := req.GetArguments()

	// --- Resolve the key namespace ---------------------------------
	namespace := stringArg(args, "namespace")
	if namespace == "" {
		namespace = defaultAnnotateNamespace
	}

	// --- Parse the annotations array (required) ---------------------
	rawAnnotations := stringArg(args, "annotations")
	if rawAnnotations == "" {
		return mcp.NewToolResultError("annotate_nodes: 'annotations' is required (a JSON array of {id, meta})"), nil
	}
	var annotations []annotateInput
	if err := json.Unmarshal([]byte(rawAnnotations), &annotations); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("annotate_nodes: 'annotations' is not valid JSON: %v", err)), nil
	}

	// --- Merge each node's namespaced metadata ----------------------
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
		changed, found := g.MergeNodeMeta(a.ID, a.namespacedKV(namespace))
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
				Origin:     namespace + "_annotated",
				Meta:       map[string]any{"similarity": score},
			})
			edgesAdded++
		}
	}

	if s.logger != nil {
		s.logger.Info("annotate_nodes",
			zap.String("namespace", namespace),
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
