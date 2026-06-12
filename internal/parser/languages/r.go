package languages

import (
	"regexp"

	rforest "github.com/alexaandru/go-sitter-forest/r"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// R extractor uses forest's tree-sitter grammar (with bundled
// tags.scm) for definitions and call edges, then layers regex passes
// for the R-specific idioms tags.scm doesn't categorize: `library()`
// / `require()` / `source()` calls become EdgeImports rather than
// EdgeCalls. Top-level assignments are also rescued by regex —
// tags.scm captures functions but doesn't always tag plain value
// bindings as variables.
var (
	rLibraryRe   = regexp.MustCompile(`(?m)\blibrary\(\s*"?'?(\w+)"?'?\s*\)`)
	rRequireRe   = regexp.MustCompile(`(?m)\brequire\(\s*"?'?(\w+)"?'?\s*\)`)
	rSourceRe    = regexp.MustCompile(`(?m)\bsource\(\s*["']([^"']+)["']\s*\)`)
	rVarAssignRe = regexp.MustCompile(`(?m)^(\w[\w.]*)\s*(?:<-|=)\s*(?:[^f]|f[^u]|fu[^n])`)
)

// RExtractor extracts R source via forest + regex idiom layer.
type RExtractor struct {
	forest *forest.Extractor
}

func NewRExtractor() *RExtractor {
	return &RExtractor{
		forest: forest.New("r", []string{".R", ".r", ".Rmd"}, rforest.GetLanguage, rforest.GetQuery),
	}
}

func (e *RExtractor) Language() string     { return "r" }
func (e *RExtractor) Extensions() []string { return []string{".R", ".r", ".Rmd"} }

func (e *RExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	for _, n := range res.Nodes {
		seen[n.ID] = true
	}

	// Idiom imports: library(X) / require(X) / source("X.R").
	for _, re := range []*regexp.Regexp{rLibraryRe, rRequireRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			mod := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			res.Edges = append(res.Edges, &graph.Edge{
				From: filePath, To: "unresolved::import::" + mod,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}
	for _, m := range rSourceRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	// Top-level value bindings (`name <- value` / `name = value`)
	// that aren't function assignments — tags.scm typically only
	// captures the function-binding shape.
	for _, m := range rVarAssignRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isRKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		res.Nodes = append(res.Nodes, &graph.Node{
			ID: id, Kind: graph.KindVariable, Name: name,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: "r",
		})
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	// Databricks source-format `.R` / `.r` notebooks: emit cell-level
	// nodes alongside the regular R symbol nodes. No-op for ordinary
	// R scripts.
	MaybeEnrichDatabricks(filePath, filePath, src, res)

	return res, nil
}

func isRKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "repeat", "in", "next", "break",
		"return", "function", "TRUE", "FALSE", "NULL", "NA", "Inf", "NaN",
		"library", "require", "source":
		return true
	}
	return false
}

var _ parser.Extractor = (*RExtractor)(nil)
