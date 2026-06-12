package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Liquid (Shopify, Jekyll) uses `{% ... %}` for control flow. The
// extractor captures:
//   - `{% assign X = ... %}`  → variable nodes
//   - `{% capture NAME %}...{% endcapture %}` → function nodes
//   - `{% include 'x' %}` / `{% render 'x' %}` → import edges
var (
	liquidAssignRe  = regexp.MustCompile(`(?m)\{%\s*assign\s+([A-Za-z_][\w]*)\s*=`)
	liquidCaptureRe = regexp.MustCompile(`(?m)\{%\s*capture\s+([A-Za-z_][\w]*)\s*%\}`)
	liquidIncludeRe = regexp.MustCompile(`(?m)\{%\s*include\s+['"]([^'"]+)['"]`)
	liquidRenderRe  = regexp.MustCompile(`(?m)\{%\s*render\s+['"]([^'"]+)['"]`)
)

// LiquidExtractor extracts Shopify/Jekyll Liquid templates.
type LiquidExtractor struct{}

func NewLiquidExtractor() *LiquidExtractor { return &LiquidExtractor{} }

func (e *LiquidExtractor) Language() string     { return "liquid" }
func (e *LiquidExtractor) Extensions() []string { return []string{".liquid"} }

func (e *LiquidExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "liquid",
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
			Language: "liquid",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range liquidAssignRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}
	for _, m := range liquidCaptureRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "{% endcapture"))
	}

	for _, re := range []*regexp.Regexp{liquidIncludeRe, liquidRenderRe} {
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

var _ parser.Extractor = (*LiquidExtractor)(nil)
