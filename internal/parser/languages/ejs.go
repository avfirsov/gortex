package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// EJS embeds JavaScript between `<% ... %>` tags. The extractor scans
// for `include(...)` directives (both the modern `<%- include('x') %>`
// form and the legacy `<% include x %>` form) as imports, and for
// JavaScript `function foo()` / `const foo = (...) =>` declarations
// inside the embedded blocks as function nodes.
var (
	ejsIncludeCallRe = regexp.MustCompile(`(?m)<%-?\s*include\s*\(\s*['"]([^'"]+)['"]`)
	ejsIncludeBareRe = regexp.MustCompile(`(?m)<%\s*include\s+([^\s%]+)\s*%>`)
	ejsFuncRe        = regexp.MustCompile(`(?m)function\s+([A-Za-z_$][\w$]*)\s*\(`)
	ejsArrowRe       = regexp.MustCompile(`(?m)(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?\([^)]*\)\s*=>`)
)

// EJSExtractor extracts Embedded JavaScript templates using regex.
type EJSExtractor struct{}

func NewEJSExtractor() *EJSExtractor { return &EJSExtractor{} }

func (e *EJSExtractor) Language() string     { return "ejs" }
func (e *EJSExtractor) Extensions() []string { return []string{".ejs"} }

func (e *EJSExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "ejs",
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
			Language: "ejs",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, re := range []*regexp.Regexp{ejsFuncRe, ejsArrowRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			name := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			add(name, graph.KindFunction, line, findBlockEnd(lines, line))
		}
	}

	for _, re := range []*regexp.Regexp{ejsIncludeCallRe, ejsIncludeBareRe} {
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

var _ parser.Extractor = (*EJSExtractor)(nil)
