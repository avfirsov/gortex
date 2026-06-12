package languages

import (
	"regexp"

	hareforest "github.com/alexaandru/go-sitter-forest/hare"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Hare migration: forest's walker handles `fn` (function_declaration)
// and `type X = struct/union/enum` (type_declaration) via the
// generic suffix matcher. `use X;` imports stay regex.
var hareUseRe = regexp.MustCompile(`(?m)^\s*use\s+([\w:]+)\s*;`)

type HareExtractor struct {
	forest *forest.Extractor
}

func NewHareExtractor() *HareExtractor {
	return &HareExtractor{
		forest: forest.New("hare", []string{".ha"}, hareforest.GetLanguage, hareforest.GetQuery),
	}
}

func (e *HareExtractor) Language() string     { return "hare" }
func (e *HareExtractor) Extensions() []string { return []string{".ha"} }

func (e *HareExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range hareUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*HareExtractor)(nil)
