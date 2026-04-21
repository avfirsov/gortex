package indexer

import (
	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

// extractDIContracts walks the graph for DI-tagged EdgeProvides and
// EdgeConsumes edges (emitted by the TypeScript extractor for @Module
// providers and @Inject consumers) and materialises them as Contract
// records in reg. The contract ID shape `di::<token>` is the same on
// both sides so the standard matcher reports orphans (tokens provided
// but not consumed, or consumed but never provided).
//
// This runs as a post-pass after per-file contract extraction because
// DI edges already live in the graph at that point — no source re-parse
// required. Safe to call repeatedly; AddAll de-duplicates by contract
// ID + symbol ID.
func (idx *Indexer) extractDIContracts(reg *contracts.Registry) {
	if reg == nil {
		return
	}
	var discovered []contracts.Contract
	for _, e := range idx.graph.AllEdges() {
		c, ok := diContractFromEdge(e)
		if !ok {
			continue
		}
		discovered = append(discovered, c)
	}
	if len(discovered) == 0 {
		return
	}
	reg.AddAll(discovered, idx.repoPrefix)
}

// diContractFromEdge maps one EdgeProvides / EdgeConsumes edge to a
// Contract when its Meta identifies it as a DI binding. Returns
// (Contract, false) for non-DI edges (HTTP/gRPC contracts already use
// these same edge kinds, so we must not treat every Provides edge as
// a DI record).
func diContractFromEdge(e *graph.Edge) (contracts.Contract, bool) {
	var zero contracts.Contract
	if e == nil || e.Meta == nil {
		return zero, false
	}
	var token string
	var role contracts.Role
	var meta map[string]any

	switch e.Kind {
	case graph.EdgeProvides:
		// Providers carry binding: "useClass" / "useValue" / "useFactory"
		// / "useExisting". useClass's provided-for field names the
		// abstract token; the others use the token name itself.
		binding, _ := e.Meta["binding"].(string)
		switch binding {
		case "useClass":
			if s, _ := e.Meta["provides_for"].(string); s != "" {
				token = s
			}
		case "useValue", "useFactory", "useExisting":
			if s, _ := e.Meta["di_token"].(string); s != "" {
				token = s
			}
		default:
			return zero, false
		}
		role = contracts.RoleProvider
		meta = map[string]any{"binding": binding}
		if target := e.To; target != "" {
			// For useClass, record the concrete class ID so callers of
			// the contracts tool can jump straight to it from the
			// orphan list. For token-form providers the target IS the
			// token, so this adds no new info — skip to avoid noise.
			if binding == "useClass" {
				meta["useClass"] = target
			}
		}
	case graph.EdgeConsumes:
		if v, _ := e.Meta["via"].(string); v != "@Inject" {
			return zero, false
		}
		token, _ = e.Meta["di_token"].(string)
		role = contracts.RoleConsumer
		meta = map[string]any{"via": "@Inject"}
	default:
		return zero, false
	}

	if token == "" {
		return zero, false
	}
	return contracts.Contract{
		ID:       "di::" + token,
		Type:     contracts.ContractDI,
		Role:     role,
		SymbolID: e.From,
		FilePath: e.FilePath,
		Line:     e.Line,
		Meta:     meta,
		// Confidence mirrors the edge's originating extractor — these
		// are static `@Module` / `@Inject` decorators, high-confidence
		// by construction. Lower values would belong to future
		// inferred DI (e.g. if we ever infer bindings from tsconfig
		// paths).
		Confidence: 0.9,
	}, true
}

