package languages

import (
	"regexp"

	matlabforest "github.com/alexaandru/go-sitter-forest/matlab"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// MATLAB migration: forest ships tags.scm AND the walker handles
// class_definition / function_definition via the generic suffix
// matcher. MATLAB's `import pkg.X` idiom stays regex.
var matlabImportRe = regexp.MustCompile(`(?m)^\s*import\s+(\w+(?:\.\w+)*)`)

type MatlabExtractor struct {
	forest *forest.Extractor
}

func NewMatlabExtractor() *MatlabExtractor {
	return &MatlabExtractor{
		forest: forest.New("matlab", []string{".mlx"}, matlabforest.GetLanguage, matlabforest.GetQuery),
	}
}

func (e *MatlabExtractor) Language() string     { return "matlab" }
func (e *MatlabExtractor) Extensions() []string { return []string{".mlx"} }

func (e *MatlabExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range matlabImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*MatlabExtractor)(nil)
