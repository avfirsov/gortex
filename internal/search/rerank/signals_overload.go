package rerank

import (
	"unicode"

	"github.com/zzet/gortex/internal/graph"
)

// OverloadProminenceSignal breaks ties among same-named candidates. When
// a query is ambiguous — several distinct symbols answer to one
// identifier (Go interface methods like BindBody across every binding,
// or a `decode` on each decoder class) — no ranker can know which
// overload the caller meant, but the likelier ones can be floated toward
// the top-5 by intrinsic prominence: an exported, non-test, callable
// definition is a better default answer than an unexported field or a
// test helper of the same name.
//
// The signal fires ONLY on a genuine same-name collision in the current
// batch (nameGroupCount > 1); a candidate whose name is unique
// contributes 0, so a non-ambiguous query is never perturbed. Every
// input — exported shape, test path, node kind — is an AST-tier fact
// available at index time with no enrichment, so the ordering is
// identical whether or not semantic enrichment has run.
type OverloadProminenceSignal struct{}

func (OverloadProminenceSignal) Name() string { return SignalOverload }

func (OverloadProminenceSignal) Contribute(_ string, c *Candidate, ctx *Context) float64 {
	if c == nil || c.Node == nil || ctx == nil {
		return 0
	}
	// Fire only on an identifier query — an exact-name lookup where
	// overload disambiguation is the whole problem. On a concept query
	// the batch's incidental same-name pairs are noise, and a blanket
	// prominence boost there would fight the semantic channel that is
	// doing the real work; scoping to the symbol class keeps the two
	// out of each other's way.
	if ctx.QueryClass != QueryClassSymbol {
		return 0
	}
	nm := lowerName(c.Node.Name)
	if nm == "" || ctx.nameGroupCount[nm] <= 1 {
		return 0
	}
	var s float64
	if isLikelyExported(c.Node) {
		s += 0.5
	}
	if !isTestPath(c.Node.FilePath) {
		s += 0.3
	}
	switch c.Node.Kind {
	case graph.KindFunction, graph.KindMethod, graph.KindType, graph.KindInterface:
		s += 0.2
	}
	if s > 1 {
		s = 1
	}
	return s
}

// lowerName lowercases a symbol name without pulling in strings for a
// one-liner on the hot path. ASCII-fast, rune-correct for the rest.
func lowerName(s string) string {
	hasUpper := false
	for _, r := range s {
		if r >= 'A' && r <= 'Z' || unicode.IsUpper(r) {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}

// isLikelyExported reports whether a symbol is part of its module's
// public surface, using only the name shape and language — no
// enrichment. A leading underscore marks a private member in Python / JS
// conventions; Go (and the other case-visibility languages) key
// export on an uppercase initial; everything else is treated as public
// unless underscore-prefixed.
func isLikelyExported(n *graph.Node) bool {
	if n == nil || n.Name == "" {
		return false
	}
	first := []rune(n.Name)[0]
	if first == '_' {
		return false
	}
	switch n.Language {
	case "go", "java", "kotlin", "scala", "csharp":
		return unicode.IsUpper(first)
	}
	return true
}
