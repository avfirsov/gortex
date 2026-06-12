package languages

import (
	"regexp"

	odinforest "github.com/alexaandru/go-sitter-forest/odin"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Odin migration: forest's walker (per-language map for
// `procedure_declaration` / `struct_declaration` /
// `package_declaration`) catches definitions. `import` and
// `foreign import` edges stay regex — they have grammar nodes but
// tags.scm doesn't tag them as graph imports.
var (
	odinImportRe        = regexp.MustCompile(`(?m)^\s*import\s+(?:(\w+)\s+)?"([^"]+)"`)
	odinForeignImportRe = regexp.MustCompile(`(?m)^\s*foreign\s+import\s+\w+\s+"([^"]+)"`)
)

type OdinExtractor struct {
	forest *forest.Extractor
}

func NewOdinExtractor() *OdinExtractor {
	return &OdinExtractor{
		forest: forest.New("odin", []string{".odin"}, odinforest.GetLanguage, odinforest.GetQuery),
	}
}

func (e *OdinExtractor) Language() string     { return "odin" }
func (e *OdinExtractor) Extensions() []string { return []string{".odin"} }

func (e *OdinExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range odinImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[4]:m[5]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range odinForeignImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*OdinExtractor)(nil)
