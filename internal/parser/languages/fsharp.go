package languages

import (
	"regexp"

	fsharpforest "github.com/alexaandru/go-sitter-forest/fsharp"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// F# migration: forest's walker (per-language map for
// `function_or_value_defn` / `named_module` / `record_type_defn`)
// catches let/module/type. `open` imports and `member` method
// declarations stay regex.
var (
	fsharpMemberRe = regexp.MustCompile(`(?m)^[ \t]*(?:static\s+)?member\s+(?:this\.|_\.|[\w.]+\.)?(\w+)`)
	fsharpOpenRe   = regexp.MustCompile(`(?m)^[ \t]*open\s+([\w.]+)`)
)

type FSharpExtractor struct {
	forest *forest.Extractor
}

func NewFSharpExtractor() *FSharpExtractor {
	return &FSharpExtractor{
		forest: forest.New("fsharp", []string{".fs", ".fsi", ".fsx"}, fsharpforest.GetLanguage, fsharpforest.GetQuery),
	}
}

func (e *FSharpExtractor) Language() string     { return "fsharp" }
func (e *FSharpExtractor) Extensions() []string { return []string{".fs", ".fsi", ".fsx"} }

func (e *FSharpExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	for _, n := range res.Nodes {
		seen[n.ID] = true
	}

	for _, m := range fsharpMemberRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		line := lineAt(src, m[0])
		res.Nodes = append(res.Nodes, &graph.Node{
			ID: id, Kind: graph.KindMethod, Name: name,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: "fsharp",
		})
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, m := range fsharpOpenRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*FSharpExtractor)(nil)
