package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Tcl is a command-dispatch language: everything is a command. The
// high-signal forms for extraction are `proc`, `namespace eval`,
// `package require`, and `source`. Namespaces and proc names can be
// qualified with `::`.
var (
	tclProcRe      = regexp.MustCompile(`(?m)^\s*proc\s+((?:::)?\w+(?:::\w+)*)`)
	tclNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+eval\s+((?:::)?\w+(?:::\w+)*)`)
	tclPackageRe   = regexp.MustCompile(`(?m)^\s*package\s+require\s+([\w:.-]+)`)
	tclSourceRe    = regexp.MustCompile(`(?m)^\s*source\s+(\S+)`)
)

// TclExtractor extracts Tcl source using regex.
type TclExtractor struct{}

func NewTclExtractor() *TclExtractor { return &TclExtractor{} }

func (e *TclExtractor) Language() string     { return "tcl" }
func (e *TclExtractor) Extensions() []string { return []string{".tcl", ".tk", ".itcl"} }

func (e *TclExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "tcl",
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
			Language: "tcl",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range tclProcRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range tclNamespaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}

	for _, m := range tclPackageRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range tclSourceRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*TclExtractor)(nil)
