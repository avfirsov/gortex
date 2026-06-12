package languages

import (
	"regexp"
	"strings"

	jinjaforest "github.com/alexaandru/go-sitter-forest/jinja"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Jinja2 migration: forest's walker (per-language map for
// `macro_statement`) catches `{% macro %}` definitions. Block
// definitions and the family of import tags (`extends` / `include`
// / `import` / `from … import`) stay regex.
var (
	jinjaBlockRe      = regexp.MustCompile(`(?m)\{%\s*block\s+([A-Za-z_][\w]*)`)
	jinjaExtendsRe    = regexp.MustCompile(`(?m)\{%\s*extends\s+['"]([^'"]+)['"]`)
	jinjaIncludeRe    = regexp.MustCompile(`(?m)\{%\s*include\s+['"]([^'"]+)['"]`)
	jinjaImportRe     = regexp.MustCompile(`(?m)\{%\s*import\s+['"]([^'"]+)['"]`)
	jinjaFromImportRe = regexp.MustCompile(`(?m)\{%\s*from\s+['"]([^'"]+)['"]\s+import`)
)

type JinjaExtractor struct {
	forest *forest.Extractor
}

func NewJinjaExtractor() *JinjaExtractor {
	return &JinjaExtractor{
		forest: forest.New("jinja", []string{".jinja", ".jinja2", ".j2"}, jinjaforest.GetLanguage, jinjaforest.GetQuery),
	}
}

func (e *JinjaExtractor) Language() string     { return "jinja" }
func (e *JinjaExtractor) Extensions() []string { return []string{".jinja", ".jinja2", ".j2"} }

func (e *JinjaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(src), "\n")
	_ = lines

	seen := make(map[string]bool)
	for _, n := range res.Nodes {
		seen[n.ID] = true
	}

	for _, m := range jinjaBlockRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		line := lineAt(src, m[0])
		res.Nodes = append(res.Nodes, &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: name,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: "jinja",
		})
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, re := range []*regexp.Regexp{jinjaExtendsRe, jinjaIncludeRe, jinjaImportRe, jinjaFromImportRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			mod := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			res.Edges = append(res.Edges, &graph.Edge{
				From: filePath, To: "unresolved::import::" + mod,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return res, nil
}

var _ parser.Extractor = (*JinjaExtractor)(nil)
