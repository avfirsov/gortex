package astquery

// Package-level helpers shared by the per-language rule files
// (rules_sast_python.go, rules_sast_go.go, etc.). The split keeps
// each file under a few hundred LOC while still giving every rule a
// CWE / OWASP / category tag.
//
// All rules registered through these helpers default to:
//   - ExcludeTests = true (a SAST finding inside a test fixture is
//     almost always intentional bait)
//   - Category    = CategorySAST
//   - Severity    = severity arg
//
// PostFilter callers should attach the filter after the builder
// returns — keeps the registration call tight.

import (
	"github.com/zzet/gortex/internal/parser"
)

// sastRule is a compact form for the per-language rule tables. The
// `Pat` map carries one tree-sitter S-expression per language; passing
// multiple languages in one entry means the same vulnerability is
// detected with the same name across them (e.g. `eval-use` across
// JS+TS or `xml-without-defusedxml` across the three xml.* submodules).
type sastRule struct {
	Name        string
	Description string
	Severity    string
	Category    string
	CWE         string
	OWASP       string
	Tags        []string
	References  []string
	Pat         map[string]string
	// PostFilter, when set, runs after every match and must return
	// true to keep the row.
	PostFilter func(parser.QueryResult, []byte) bool
	// ExcludeTests overrides the default true (most SAST rules
	// stay default-true; a few opt out — e.g. hygiene rules that
	// also flag tests).
	ExcludeTests *bool
}

func mustRegisterSAST(rules ...sastRule) {
	for _, r := range rules {
		registerSAST(r)
	}
}

func registerSAST(r sastRule) {
	cat := r.Category
	if cat == "" {
		cat = CategorySAST
	}
	// ExcludeTests defaults to true for every SAST + hygiene rule
	// (debug-leftover / SAST findings inside a test fixture are
	// almost always intentional bait or test scaffolding). A rule
	// that should also flag tests sets r.ExcludeTests = new(bool)
	// (zero-value false).
	excludeTests := true
	if r.ExcludeTests != nil {
		excludeTests = *r.ExcludeTests
	}
	d := &Detector{
		Name:         r.Name,
		Description:  r.Description,
		Severity:     r.Severity,
		Category:     cat,
		CWE:          r.CWE,
		OWASP:        r.OWASP,
		Tags:         append([]string(nil), r.Tags...),
		References:   append([]string(nil), r.References...),
		Languages:    r.Pat,
		ExcludeTests: excludeTests,
		PostFilter:   r.PostFilter,
	}
	RegisterDetector(d)
}
