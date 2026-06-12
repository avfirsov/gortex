package languages

import (
	"regexp"

	dforest "github.com/alexaandru/go-sitter-forest/d"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// D migration: forest's walker handles class_declaration /
// struct_declaration / module_declaration via the generic suffix
// matcher. D's `import x.y.z;` idiom stays regex.
var dImportRe = regexp.MustCompile(`(?m)^\s*(?:static\s+|public\s+|private\s+)?import\s+([\w.]+)`)

type DExtractor struct {
	forest *forest.Extractor
}

func NewDExtractor() *DExtractor {
	return &DExtractor{
		forest: forest.New("d", []string{".d", ".di"}, dforest.GetLanguage, dforest.GetQuery),
	}
}

func (e *DExtractor) Language() string     { return "d" }
func (e *DExtractor) Extensions() []string { return []string{".d", ".di"} }

func (e *DExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range dImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*DExtractor)(nil)
