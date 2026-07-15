package resolver

import (
	"os"
	"strings"
	"time"
)

const (
	// LSPResolvePassBudgetEnv overrides the total wall-clock budget for the
	// deferred resolve-time LSP batch in ResolveAll. A zero/off/none value
	// preserves the pre-budget unlimited behaviour.
	LSPResolvePassBudgetEnv = "GORTEX_LSP_RESOLVE_PASS_BUDGET"

	defaultLSPResolvePassBudget = 15 * time.Second
)

// LSPHelper drives resolve-time LSP queries from the cross-file
// resolver. The resolver consults it for TS/JS/JSX/TSX edges before
// falling back to AST/name heuristics — letting the type-aware
// compiler (tsserver) win on cases the heuristics lose: barrel
// re-exports, declaration merging, type-narrowed dispatch, JSX
// component-as-callsite.
//
// The interface is defined in the resolver package so the resolver
// has no compile-time dependency on the lsp package — the indexer
// constructs a concrete helper (typically wrapping a *lsp.Provider)
// and injects it via Resolver.SetLSPHelper. Resolver consults the
// helper synchronously during resolveEdge; implementations are
// expected to serialise tsserver-bound calls themselves and apply a
// per-call timeout so a stalled language server can never gate the
// resolve pass.
type LSPHelper interface {
	// SupportsPath reports whether the helper can answer queries for
	// relPath. Implementations match on file extension; the resolver
	// short-circuits when SupportsPath is false (no LSP attempt).
	SupportsPath(relPath string) bool

	// Definition returns the (relativePath, 1-based line) of the
	// declaration of `name` referenced on `oneBasedLine` inside
	// relPath. Returns ok=false when the LSP is unavailable, times
	// out, or has no answer. The caller is responsible for matching
	// the returned location to a graph node.
	Definition(relPath string, oneBasedLine int, name string) (defRelPath string, defOneBasedLine int, ok bool)
}

// SetLSPHelper installs a resolve-time LSP helper. Pass nil to detach.
// Must be called before ResolveAll / ResolveFile — the resolver caches
// no LSP state across passes, so changing helpers between passes is
// safe but mid-pass installation is racy with the parallel resolveEdge
// workers and is not supported.
func (r *Resolver) SetLSPHelper(h LSPHelper) {
	r.lspHelper = h
}

// SetLSPResolvePassBudget overrides the cumulative deferred-LSP budget used
// by ResolveAll. Zero disables the cumulative bound (the helper's per-call
// timeout still applies); negative values are normalised to zero. Like
// SetLSPHelper, it must be configured before a resolve pass starts.
func (r *Resolver) SetLSPResolvePassBudget(budget time.Duration) {
	if budget < 0 {
		budget = 0
	}
	r.lspResolvePassBudget = budget
}

// LSPHelper returns the currently installed helper, or nil.
func (r *Resolver) LSPHelper() LSPHelper {
	return r.lspHelper
}

func lspResolvePassBudgetFromEnv() time.Duration {
	switch raw := strings.TrimSpace(os.Getenv(LSPResolvePassBudgetEnv)); raw {
	case "":
		return defaultLSPResolvePassBudget
	case "0", "off", "none":
		return 0
	default:
		budget, err := time.ParseDuration(raw)
		if err != nil || budget < 0 {
			return defaultLSPResolvePassBudget
		}
		return budget
	}
}
