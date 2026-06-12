package languages

import (
	"regexp"

	rescriptforest "github.com/alexaandru/go-sitter-forest/rescript"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// ReScript migration: forest's walker (per-language map for
// `let_declaration` / `module_declaration` / `type_declaration`)
// catches definitions. `open` / `include` imports stay regex.
var rescriptOpenRe = regexp.MustCompile(`(?m)^\s*(?:open|include)\s+([\w.]+)`)

type ReScriptExtractor struct {
	forest *forest.Extractor
}

func NewReScriptExtractor() *ReScriptExtractor {
	return &ReScriptExtractor{
		forest: forest.New("rescript", []string{".res", ".resi"}, rescriptforest.GetLanguage, rescriptforest.GetQuery),
	}
}

func (e *ReScriptExtractor) Language() string     { return "rescript" }
func (e *ReScriptExtractor) Extensions() []string { return []string{".res", ".resi"} }

func (e *ReScriptExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range rescriptOpenRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*ReScriptExtractor)(nil)
