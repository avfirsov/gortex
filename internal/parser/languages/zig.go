package languages

import (
	"regexp"

	zigforest "github.com/alexaandru/go-sitter-forest/zig"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Zig migration: forest's walker handles `fn` (function_declaration),
// `struct` / `enum` / `union` (struct_declaration), and `var` /
// `const` (variable_declaration). Zig's `@import("X")` builtin is
// kept as a regex pass to emit EdgeImports. We also stamp
// Meta["type_kind"] on struct/enum/union nodes that the regex
// recognises by their `const Name = struct {…}` shape.
var (
	zigImportRe = regexp.MustCompile(`@import\("([^"]+)"\)`)
	zigStructRe = regexp.MustCompile(`(?m)^[ \t]*(?:pub\s+)?const\s+(\w+)\s*=\s*(struct|enum|union)\s*\{`)
)

type ZigExtractor struct {
	forest *forest.Extractor
}

func NewZigExtractor() *ZigExtractor {
	return &ZigExtractor{
		forest: forest.New("zig", []string{".zig"}, zigforest.GetLanguage, zigforest.GetQuery),
	}
}

func (e *ZigExtractor) Language() string     { return "zig" }
func (e *ZigExtractor) Extensions() []string { return []string{".zig"} }

func (e *ZigExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}

	for _, m := range zigStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		kind := string(src[m[4]:m[5]])
		for _, n := range res.Nodes {
			if n.Name == name && (n.Kind == graph.KindVariable || n.Kind == graph.KindConstant) {
				n.Kind = graph.KindType
				if n.Meta == nil {
					n.Meta = map[string]any{}
				}
				n.Meta["type_kind"] = kind
			}
		}
	}

	for _, m := range zigImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*ZigExtractor)(nil)
