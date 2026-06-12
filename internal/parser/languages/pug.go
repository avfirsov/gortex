package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Pug (formerly Jade) is indent-delimited. Meaningful symbols are
// `mixin name(args)` (function-like reusable blocks) and `block name`
// (inheritance slots). Cross-template wiring comes from top-of-file
// `extends path` / `include path` directives which we model as
// import edges. Mixin and block bodies are indent-delimited and
// handled via findIndentedBlockEnd.
var (
	pugMixinRe   = regexp.MustCompile(`(?m)^\s*mixin\s+([A-Za-z_][\w-]*)`)
	pugBlockRe   = regexp.MustCompile(`(?m)^\s*block\s+(?:append\s+|prepend\s+)?([A-Za-z_][\w-]*)`)
	pugIncludeRe = regexp.MustCompile(`(?m)^\s*include(?:\s+[A-Za-z:\-]+)?\s+([^\s]+)`)
	pugExtendsRe = regexp.MustCompile(`(?m)^\s*extends\s+([^\s]+)`)
)

// PugExtractor extracts Pug/Jade templates using regex.
type PugExtractor struct{}

func NewPugExtractor() *PugExtractor { return &PugExtractor{} }

func (e *PugExtractor) Language() string     { return "pug" }
func (e *PugExtractor) Extensions() []string { return []string{".pug", ".jade"} }

func (e *PugExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "pug",
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
			Language: "pug",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range pugMixinRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range pugBlockRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findIndentedBlockEnd(lines, line))
	}

	for _, re := range []*regexp.Regexp{pugIncludeRe, pugExtendsRe} {
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

var _ parser.Extractor = (*PugExtractor)(nil)
