package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Perl: `sub Name { ... }` for functions, `package Name;` for modules,
// `use Name` / `require Name` for imports. Calls come in many shapes
// (`&name`, `name()`, `Name->method`); we only chase the `name(`
// variety to keep noise down.
var (
	perlSubRe     = regexp.MustCompile(`(?m)^\s*sub\s+(\w+)`)
	perlPackageRe = regexp.MustCompile(`(?m)^\s*package\s+([\w:]+)`)
	perlUseRe     = regexp.MustCompile(`(?m)^\s*(?:use|require)\s+([\w:]+)`)
	perlCallRe    = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
)

// PerlExtractor extracts Perl source using regex.
type PerlExtractor struct{}

func NewPerlExtractor() *PerlExtractor { return &PerlExtractor{} }

func (e *PerlExtractor) Language() string     { return "perl" }
func (e *PerlExtractor) Extensions() []string { return []string{".pl", ".pm", ".t", ".pod"} }

func (e *PerlExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "perl",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isPerlKeyword(name) {
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
			Language: "perl",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range perlPackageRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, len(lines))
	}
	for _, m := range perlSubRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}

	for _, m := range perlUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range perlCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isPerlKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		callerID := findEnclosingFunc(funcRanges, line)
		if callerID == "" || strings.HasSuffix(callerID, "::"+name) {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isPerlKeyword(s string) bool {
	switch s {
	case "if", "elsif", "else", "unless", "while", "until", "for", "foreach",
		"do", "last", "next", "redo", "return", "my", "our", "local", "state",
		"sub", "package", "use", "no", "require", "defined", "undef", "and",
		"or", "not", "xor", "eq", "ne", "lt", "gt", "le", "ge", "cmp":
		return true
	}
	return false
}

var _ parser.Extractor = (*PerlExtractor)(nil)
