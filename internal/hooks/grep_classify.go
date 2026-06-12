package hooks

import "regexp"

// GrepPatternClass classifies a Grep pattern for the symbol-redirect probe.
type GrepPatternClass int

const (
	// GrepPatternSkip means the pattern is not symbol-shaped; don't probe.
	GrepPatternSkip GrepPatternClass = iota
	// GrepPatternSymbol means the pattern looks like a bare identifier; probe search_symbols.
	GrepPatternSymbol
)

// symbolShaped matches a single identifier-shaped token. Dots and slashes are
// permitted so qualified names like "pkg.Handler" or "pkg/Type" still qualify.
var symbolShaped = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_./]*$`)

// classifyGrepPattern decides whether a pattern should trigger the
// search_symbols probe. Non-symbol patterns (regex metachars, quoted strings,
// numeric literals, multi-word prose, too short) fall through to vanilla Grep.
func classifyGrepPattern(pattern string) GrepPatternClass {
	if len(pattern) <= 2 {
		return GrepPatternSkip
	}
	if !symbolShaped.MatchString(pattern) {
		return GrepPatternSkip
	}
	return GrepPatternSymbol
}
