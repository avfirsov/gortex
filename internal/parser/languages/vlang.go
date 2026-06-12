package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// V is a Go-like language with `fn`, `struct`, `interface`, and
// `enum`. Imports use `import x`, `import x.y`, or `import x as y`.
var (
	vlangFuncRe   = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+(?:\([^)]*\)\s+)?(\w+)\s*\(`)
	vlangStructRe = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?struct\s+(\w+)\b`)
	vlangIfaceRe  = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?interface\s+(\w+)\b`)
	vlangEnumRe   = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?enum\s+(\w+)\b`)
	vlangTypeRe   = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?type\s+(\w+)\s*=`)
	vlangImportRe = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+)`)
	vlangModuleRe = regexp.MustCompile(`(?m)^\s*module\s+(\w+)`)
	vlangCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// VlangExtractor extracts V source using regex.
type VlangExtractor struct{}

func NewVlangExtractor() *VlangExtractor { return &VlangExtractor{} }

func (e *VlangExtractor) Language() string     { return "v" }
func (e *VlangExtractor) Extensions() []string { return []string{".v", ".vsh"} }

func (e *VlangExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "v",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isVlangKeyword(name) {
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
			Language: "v",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range vlangModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range vlangFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range vlangStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range vlangIfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range vlangEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range vlangTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}

	for _, m := range vlangImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range vlangCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isVlangKeyword(name) {
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

func isVlangKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "match", "in", "is", "or", "and",
		"return", "defer", "go", "spawn", "break", "continue",
		"fn", "struct", "interface", "enum", "type", "const",
		"module", "import", "pub", "mut", "shared", "static",
		"true", "false", "none", "as", "unsafe", "asm", "lock", "rlock":
		return true
	}
	return false
}

var _ parser.Extractor = (*VlangExtractor)(nil)
