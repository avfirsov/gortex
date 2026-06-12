package languages

import (
	"regexp"

	valaforest "github.com/alexaandru/go-sitter-forest/vala"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Vala migration: forest's walker handles
// `class_declaration` / `interface_declaration` /
// `method_declaration` / `namespace_declaration` via the generic
// suffix matcher. `using X;` imports stay regex.
var valaUsingRe = regexp.MustCompile(`(?m)^\s*using\s+([\w.]+)\s*;`)

type ValaExtractor struct {
	forest *forest.Extractor
}

func NewValaExtractor() *ValaExtractor {
	return &ValaExtractor{
		forest: forest.New("vala", []string{".vala", ".vapi"}, valaforest.GetLanguage, valaforest.GetQuery),
	}
}

func (e *ValaExtractor) Language() string     { return "vala" }
func (e *ValaExtractor) Extensions() []string { return []string{".vala", ".vapi"} }

func (e *ValaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range valaUsingRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*ValaExtractor)(nil)
