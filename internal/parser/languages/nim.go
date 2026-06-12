package languages

import (
	"regexp"
	"strings"

	nimforest "github.com/alexaandru/go-sitter-forest/nim"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Nim migration: walker (with per-language map for
// `proc_declaration`, `type_declaration`, `object_declaration`)
// covers the structural shape. Imports use a directive idiom
// (`import` / `include` / `from`) tags.scm doesn't tag, so they
// stay as regex.
var nimImportRe = regexp.MustCompile(`(?m)^\s*(?:import|include|from)\s+([\w./]+)`)

type NimExtractor struct {
	forest *forest.Extractor
}

func NewNimExtractor() *NimExtractor {
	return &NimExtractor{
		forest: forest.New("nim", []string{".nim", ".nims", ".nimble"}, nimforest.GetLanguage, nimforest.GetQuery),
	}
}

func (e *NimExtractor) Language() string     { return "nim" }
func (e *NimExtractor) Extensions() []string { return []string{".nim", ".nims", ".nimble"} }

func (e *NimExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}

	// Strip Nim's `*` export marker from forest-emitted names.
	for _, n := range res.Nodes {
		if n.Kind == graph.KindFunction || n.Kind == graph.KindType {
			n.Name = strings.TrimSuffix(n.Name, "*")
		}
	}

	for _, m := range nimImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*NimExtractor)(nil)
