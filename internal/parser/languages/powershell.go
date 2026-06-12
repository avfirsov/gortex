package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// PowerShell. Shapes we capture: `function Verb-Noun` and `filter`
// definitions, `class` declarations (PS 5.0+), `Import-Module` and
// dot-source statements. Verb-noun pairs may contain dashes — the name
// regex uses `[\w-]+`.
var (
	psFunctionRe  = regexp.MustCompile(`(?im)^\s*(?:function|filter)\s+(?:(?:global|local|script|private):)?([\w-]+)`)
	psClassRe     = regexp.MustCompile(`(?im)^\s*class\s+(\w+)`)
	psImportRe    = regexp.MustCompile(`(?im)^\s*Import-Module\s+(?:-Name\s+)?['"]?([\w./\\-]+)['"]?`)
	psDotSourceRe = regexp.MustCompile(`(?im)^\s*\.\s+['"]?([\w./\\-]+\.ps[md]?1)['"]?`)
)

// PowerShellExtractor extracts PowerShell source using regex.
type PowerShellExtractor struct{}

func NewPowerShellExtractor() *PowerShellExtractor { return &PowerShellExtractor{} }

func (e *PowerShellExtractor) Language() string { return "powershell" }
func (e *PowerShellExtractor) Extensions() []string {
	return []string{".ps1", ".psm1", ".psd1"}
}

func (e *PowerShellExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "powershell",
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
			Language: "powershell",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range psClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range psFunctionRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}

	for _, m := range psImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range psDotSourceRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*PowerShellExtractor)(nil)
