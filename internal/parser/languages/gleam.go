package languages

import (
	"regexp"

	gleamforest "github.com/alexaandru/go-sitter-forest/gleam"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Gleam migration: forest's tags.scm captures `pub fn` / `fn`
// functions and `pub type` / `type` definitions. Imports use
// idiom-specific syntax (`import x/y.{A, B} as alias`) that
// tree-sitter parses but tags.scm doesn't tag — we layer a regex
// pass on top to emit EdgeImports for them.
var gleamImportRe = regexp.MustCompile(`(?m)^\s*import\s+([\w/]+)`)

// GleamExtractor parses Gleam source via forest + regex idiom layer.
type GleamExtractor struct {
	forest *forest.Extractor
}

func NewGleamExtractor() *GleamExtractor {
	return &GleamExtractor{
		forest: forest.New("gleam", []string{".gleam"}, gleamforest.GetLanguage, gleamforest.GetQuery),
	}
}

func (e *GleamExtractor) Language() string     { return "gleam" }
func (e *GleamExtractor) Extensions() []string { return []string{".gleam"} }

func (e *GleamExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range gleamImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*GleamExtractor)(nil)
