package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// SourceReader returns the on-disk source bytes for a caller node, or
// ok=false if unavailable. The contracts package is language-agnostic
// and doesn't know repo roots; the caller (indexer.MultiIndexer) builds
// a closure that maps a graph node to the file on disk by consulting
// repo metadata.
type SourceReader func(n *graph.Node) ([]byte, bool)

// InlineWrappers identifies HTTP-client wrapper functions (generic
// helpers that forward a path argument to fetch/http.Get/etc.) and
// emits per-caller consumer contracts with the caller's specific path.
// Without this, a codebase that routes every endpoint through a single
// request(path, ...) helper produces one useless parametric contract
// per wrapper and zero matches against real provider routes.
//
// Algorithm (BFS propagation across the wrapper chain):
//  1. Seed: every existing consumer HTTP contract whose normalized path
//     is pathologically parametric ("/{word}") is a wrapper.
//  2. For each wrapper, walk graph.GetInEdges(symbol) with Kind=EdgeCalls
//     — the functions that call this wrapper. For each, re-read the
//     caller's source at the call-site line and extract the first arg.
//     - Literal path → emit a new consumer contract for the caller.
//     - Bare identifier matching the caller's own parameter name → the
//     caller is itself a wrapper; enqueue it for the next pass.
//     - Anything else (runtime expression) → skip silently.
//  3. Repeat until no new wrappers are found, bounded by a safety cap.
//
// Returns the set of contracts added (so callers can persist them into
// their per-repo registries — the transient merged registry MultiIndexer
// hands in is rebuilt on every ReconcileContractEdges call, so mutations
// to it don't survive between invocations).
func InlineWrappers(reg *Registry, g graph.Store, read SourceReader) []Contract {
	if reg == nil || g == nil || read == nil {
		return nil
	}

	wrappers := seedWrappers(reg)
	seen := make(map[string]bool, len(wrappers))
	for _, w := range wrappers {
		seen[w.SymbolID] = true
	}

	var added []Contract
	// Safety cap against pathological chains.
	const maxPasses = 8

	for pass := 0; pass < maxPasses && len(wrappers) > 0; pass++ {
		var next []wrapperInfo
		for _, w := range wrappers {
			for _, edge := range g.GetInEdges(w.SymbolID) {
				if edge.Kind != graph.EdgeCalls {
					continue
				}
				caller := g.GetNode(edge.From)
				if caller == nil {
					continue
				}
				wrapperNode := g.GetNode(w.SymbolID)
				if wrapperNode == nil {
					continue
				}
				src, ok := read(caller)
				if !ok {
					continue
				}
				arg := extractFirstCallArg(src, edge.Line, wrapperNode.Name, caller.Language)
				switch arg.Kind {
				case argLiteral:
					method := arg.Method
					if method == "" {
						method = "GET"
					}
					path, origNames := NormalizeHTTPPathWithParams(arg.Value)
					meta := map[string]any{
						"method":    method,
						"path":      path,
						"framework": "inlined-wrapper",
						"wrapper":   w.SymbolID,
					}
					if len(origNames) > 0 {
						meta["path_param_names"] = origNames
					}
					c := Contract{
						ID:         fmt.Sprintf("http::%s::%s", method, path),
						Type:       ContractHTTP,
						Role:       RoleConsumer,
						SymbolID:   caller.ID,
						FilePath:   caller.FilePath,
						Line:       edge.Line,
						RepoPrefix: caller.RepoPrefix,
						// Workspace/project boundary slugs flow from the
						// caller's graph node — stamped at index time.
						// Without this carry-over the inlined contract
						// gets the default workspace = repoPrefix and the
						// matcher can't pair it with a same-workspace
						// provider.
						WorkspaceID: caller.WorkspaceID,
						ProjectID:   caller.ProjectID,
						Meta:        meta,
						Confidence:  0.8,
					}
					// Run schema enrichment on the caller's body so
					// the inlined contract carries request_type /
					// response_type / query_params just like a
					// regex-detected consumer contract would. Without
					// this the dashboard shows "not declared on this
					// side" for every wrapper-routed call site.
					enrichInlinedWrapperContract(&c, g, caller, src)
					reg.Add(c)
					commitInlinedContractToGraph(g, c)
					added = append(added, c)
				case argBareParam:
					if !seen[caller.ID] {
						seen[caller.ID] = true
						next = append(next, wrapperInfo{SymbolID: caller.ID})
					}
				}
			}
		}
		wrappers = next
	}
	return added
}

// wrapperInfo is the minimal record carried through BFS passes.
type wrapperInfo struct {
	SymbolID string
}

// enrichInlinedWrapperContract runs the schema enrichment pipeline
// against the caller's body so inlined wrapper consumer contracts
// carry the same request_type / response_type / query_params facts
// that natively-detected consumer contracts do.
//
// Mirrors the enrichment chain HTTPExtractor.extract runs after
// matching a regex pattern: lines + fileNodes + lang + tree feed
// EnrichHTTPContractWithTree, which dispatches to the per-language
// schema_enrich_*.go detectors and (for Go) the AST overlay.
func enrichInlinedWrapperContract(c *Contract, g graph.Store, caller *graph.Node, src []byte) {
	if c == nil || caller == nil || len(src) == 0 {
		return
	}
	lang := caller.Language
	if lang == "" {
		return
	}
	lines := strings.Split(string(src), "\n")
	fileNodes := g.GetFileNodes(caller.FilePath)
	tree := ParseTreeForLang(lang, src)
	defer tree.Release()
	EnrichHTTPContractWithTree(c, lines, fileNodes, lang, tree)
}

// seedWrappers finds the initial set of wrappers: consumer HTTP
// contracts whose normalized path is a single parameter placeholder
// like "/{path}" or "/{url}". Those shapes come from HTTPExtractor
// detecting fetch(`${API_URL}${path}`) — the classic signature of a
// fully-parametric wrapper URL.
func seedWrappers(reg *Registry) []wrapperInfo {
	var out []wrapperInfo
	for _, c := range reg.All() {
		if c.Type != ContractHTTP || c.Role != RoleConsumer || c.SymbolID == "" {
			continue
		}
		path, _ := c.Meta["path"].(string)
		if !isWrapperPath(path) {
			continue
		}
		out = append(out, wrapperInfo{SymbolID: c.SymbolID})
	}
	return out
}

// wrapperPathRE matches a normalized path that consists solely of one
// placeholder segment — the signature of a fully-parametric wrapper URL.
var wrapperPathRE = regexp.MustCompile(`^/\{?[a-zA-Z][a-zA-Z0-9_]*\}?$`)

func isWrapperPath(path string) bool {
	return wrapperPathRE.MatchString(path)
}

// commitInlinedContractToGraph adds the contract as a graph node (if not
// already present) and a symbol → contract EdgeConsumes edge (also
// idempotent). Mirrors the commitContracts logic in the indexer but
// runs at wrapper-inline time so late-emitted contracts appear in
// contracts list output and in the matcher's graph view. Idempotency
// matters because ReconcileContractEdges runs on every repo change —
// without it each track/index would duplicate edges.
func commitInlinedContractToGraph(g graph.Store, c Contract) {
	if g == nil {
		return
	}
	if g.GetNode(c.ID) == nil {
		g.AddNode(&graph.Node{
			ID:          c.ID,
			Kind:        graph.KindContract,
			Name:        c.ID,
			FilePath:    c.FilePath,
			Language:    "contract",
			RepoPrefix:  c.RepoPrefix,
			WorkspaceID: c.EffectiveWorkspace(),
			ProjectID:   c.EffectiveProject(),
			Meta: map[string]any{
				"type":          string(c.Type),
				"role":          string(c.Role),
				"symbol_id":     c.SymbolID,
				"line":          c.Line,
				"confidence":    c.Confidence,
				"contract_meta": c.Meta,
			},
		})
	}
	if c.SymbolID == "" {
		return
	}
	for _, existing := range g.GetOutEdges(c.SymbolID) {
		if existing.Kind == graph.EdgeConsumes && existing.To == c.ID {
			return
		}
	}
	g.AddEdge(&graph.Edge{
		From:     c.SymbolID,
		To:       c.ID,
		Kind:     graph.EdgeConsumes,
		FilePath: c.FilePath,
		Line:     c.Line,
	})
}
