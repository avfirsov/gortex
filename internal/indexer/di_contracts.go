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
	// Spring @Bean linkage runs first and produces new EdgeCalls edges
	// that the later Contract-emission pass needs to consider. Ordering
	// also keeps bean extraction independent of the contract-side —
	// a repo that only uses Spring still gets usable bean links even
	// if no @Inject / useClass contracts exist anywhere.
	idx.linkSpringBeans()

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
	reg.AddAllScoped(discovered, idx.repoPrefix, idx.workspaceID, idx.projectID)
}

// linkSpringBeans emits EdgeCalls from every class that has an
// incoming-method node whose signature mentions a @Bean return type
// back to the bean method. Uses method signatures because the Java
// extractor already stores them on constructor nodes — no second
// parse pass needed. Kept tight by requiring an exact type-name
// token match inside the signature string.
func (idx *Indexer) linkSpringBeans() {
	type beanRef struct {
		methodID string
		typeName string
		filePath string
		line     int
	}
	var beans []beanRef
	for _, e := range idx.graph.AllEdges() {
		if e.Kind != graph.EdgeProvides || e.Meta == nil {
			continue
		}
		if b, _ := e.Meta["binding"].(string); b != "bean" {
			continue
		}
		rt, _ := e.Meta["provides_for"].(string)
		if rt == "" {
			continue
		}
		beans = append(beans, beanRef{methodID: e.To, typeName: rt, filePath: e.FilePath, line: e.Line})
	}
	if len(beans) == 0 {
		return
	}

	// For each bean, walk Java constructor nodes whose params_src
	// (captured at extraction time) mentions the return type. Dedupe
	// by (consumer_class, bean_method) so an overloaded constructor
	// only links once.
	linked := make(map[string]struct{})
	for _, b := range beans {
		for _, n := range idx.graph.AllNodes() {
			if n.Kind != graph.KindMethod || n.Language != "java" {
				continue
			}
			if n.ID == b.methodID {
				continue
			}
			params, _ := n.Meta["params_src"].(string)
			if params == "" {
				continue
			}
			if !signatureReferencesType(params, b.typeName) {
				continue
			}
			cls := enclosingClassID(n)
			if cls == "" || cls == b.methodID {
				continue
			}
			key := cls + "->" + b.methodID
			if _, dup := linked[key]; dup {
				continue
			}
			linked[key] = struct{}{}
			idx.graph.AddEdge(&graph.Edge{
				From:     cls,
				To:       b.methodID,
				Kind:     graph.EdgeCalls,
				FilePath: b.filePath,
				Line:     b.line,
				Meta: map[string]any{
					"via":     "spring.Bean",
					"bean_of": b.typeName,
				},
			})
		}
	}
}

// signatureReferencesType returns true when sig contains typeName as a
// whole identifier (e.g. "Clock", "UserService"). Conservative match —
// substring-but-word-boundary to avoid `Clock` matching `ClockFactory`.
func signatureReferencesType(sig, typeName string) bool {
	i := 0
	for i < len(sig) {
		j := indexOf(sig[i:], typeName)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(typeName)
		leftOK := start == 0 || !isJavaIdentChar(sig[start-1])
		rightOK := end == len(sig) || !isJavaIdentChar(sig[end])
		if leftOK && rightOK {
			return true
		}
		i = end
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func isJavaIdentChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

// enclosingClassID derives the class-level node ID from a method node
// using its Meta["receiver"] (what the Java extractor stores). Returns
// "" if we can't derive one (free functions, static methods on pkg
// objects, etc. — not relevant for Spring anyway).
func enclosingClassID(n *graph.Node) string {
	recv, _ := n.Meta["receiver"].(string)
	if recv == "" {
		return ""
	}
	return n.FilePath + "::" + recv
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
		// / "useExisting" / "bean". useClass and Spring's bean both
		// name their abstract via provides_for; the token forms use
		// the token name itself.
		binding, _ := e.Meta["binding"].(string)
		switch binding {
		case "useClass", "bean":
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
			// For useClass / bean, record the concrete target so the
			// orphan list in the contracts tool links straight to
			// either the concrete class (useClass) or the factory
			// method (bean). Token-form providers point at the token
			// directly, no extra info needed.
			if binding == "useClass" || binding == "bean" {
				meta[binding] = target
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

