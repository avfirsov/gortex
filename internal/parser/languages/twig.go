package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Twig is Symfony's templating language and shares a tag vocabulary
// with Jinja: `{% block %}`, `{% macro %}`, `{% extends %}`,
// `{% include %}`, `{% import %}`. The extractor follows the same
// shape as the Jinja extractor but gets its own language tag for
// repo metrics / language filtering.
var (
	twigBlockRe   = regexp.MustCompile(`(?m)\{%\s*block\s+([A-Za-z_][\w]*)`)
	twigMacroRe   = regexp.MustCompile(`(?m)\{%\s*macro\s+([A-Za-z_][\w]*)\s*\(`)
	twigExtendsRe = regexp.MustCompile(`(?m)\{%\s*extends\s+['"]([^'"]+)['"]`)
	twigIncludeRe = regexp.MustCompile(`(?m)\{%\s*include\s+['"]([^'"]+)['"]`)
	twigImportRe  = regexp.MustCompile(`(?m)\{%\s*import\s+['"]([^'"]+)['"]`)
)

// TwigExtractor extracts Symfony Twig templates using regex.
type TwigExtractor struct{}

func NewTwigExtractor() *TwigExtractor { return &TwigExtractor{} }

func (e *TwigExtractor) Language() string     { return "twig" }
func (e *TwigExtractor) Extensions() []string { return []string{".twig"} }

func (e *TwigExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "twig",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" {
			return
		}
		id := filePath + "::" + name
		if seen[id] {
			return
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: start, EndLine: end,
			Language: "twig",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range twigBlockRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "{% endblock"))
	}
	for _, m := range twigMacroRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "{% endmacro"))
	}

	for _, re := range []*regexp.Regexp{twigExtendsRe, twigIncludeRe, twigImportRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			mod := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: "unresolved::import::" + mod,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return result, nil
}

var _ parser.Extractor = (*TwigExtractor)(nil)
