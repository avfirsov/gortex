package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Raku (formerly Perl 6) has first-class OO (`class`, `role`), rich
// dispatch (`multi`, `proto`, `only`), and `sub` / `method` for
// callables. `unit` forms declare a file-level module. Imports use
// `use`, `need`, or `import`.
var (
	rakuSubRe    = regexp.MustCompile(`(?m)^\s*(?:multi\s+|proto\s+|only\s+)?sub\s+(\w[\w-]*)`)
	rakuMethodRe = regexp.MustCompile(`(?m)^\s*(?:multi\s+|proto\s+|only\s+)?method\s+(\w[\w-]*)`)
	rakuClassRe  = regexp.MustCompile(`(?m)^\s*(?:unit\s+)?class\s+([\w:]+)`)
	rakuRoleRe   = regexp.MustCompile(`(?m)^\s*(?:unit\s+)?role\s+([\w:]+)`)
	rakuModuleRe = regexp.MustCompile(`(?m)^\s*(?:unit\s+)?module\s+([\w:]+)`)
	rakuUseRe    = regexp.MustCompile(`(?m)^\s*(?:use|need|import)\s+([\w:]+)`)
)

// RakuExtractor extracts Raku source using regex.
type RakuExtractor struct{}

func NewRakuExtractor() *RakuExtractor { return &RakuExtractor{} }

func (e *RakuExtractor) Language() string { return "raku" }
func (e *RakuExtractor) Extensions() []string {
	return []string{".raku", ".rakumod", ".rakutest", ".p6", ".pm6"}
}

func (e *RakuExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "raku",
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
			Language: "raku",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range rakuModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, len(lines))
	}
	for _, m := range rakuClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range rakuRoleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindInterface, line, findBlockEnd(lines, line))
	}
	for _, m := range rakuSubRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range rakuMethodRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, findBlockEnd(lines, line))
	}

	for _, m := range rakuUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*RakuExtractor)(nil)
