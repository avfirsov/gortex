package languages

import (
	"regexp"

	crystalforest "github.com/alexaandru/go-sitter-forest/crystal"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Crystal migration: walker (with per-language map for `class_def`,
// `module_def`, `struct_def`, `method_def`) covers the structural
// shape. `require "path"` import edges are the only Crystal idiom
// the walker can't categorise.
var crystalRequireRe = regexp.MustCompile(`(?m)^\s*require\s+"([^"]+)"`)

type CrystalExtractor struct {
	forest *forest.Extractor
}

func NewCrystalExtractor() *CrystalExtractor {
	return &CrystalExtractor{
		forest: forest.New("crystal", []string{".cr"}, crystalforest.GetLanguage, crystalforest.GetQuery),
	}
}

func (e *CrystalExtractor) Language() string     { return "crystal" }
func (e *CrystalExtractor) Extensions() []string { return []string{".cr"} }

func (e *CrystalExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range crystalRequireRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*CrystalExtractor)(nil)
