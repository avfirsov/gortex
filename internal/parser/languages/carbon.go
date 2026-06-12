package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Carbon (Google's C++ successor) uses `fn NAME(args) -> ret { ... }`
// for functions, `class`/`interface`/`adapter`/`choice` for types,
// and `import X` / `import X library "Y"` for modules.
var (
	carbonFuncRe    = regexp.MustCompile(`(?m)^\s*fn\s+(\w+)\s*\(`)
	carbonClassRe   = regexp.MustCompile(`(?m)^\s*class\s+(\w+)\b`)
	carbonIfaceRe   = regexp.MustCompile(`(?m)^\s*interface\s+(\w+)\b`)
	carbonAdapterRe = regexp.MustCompile(`(?m)^\s*adapter\s+(\w+)\b`)
	carbonChoiceRe  = regexp.MustCompile(`(?m)^\s*choice\s+(\w+)\b`)
	carbonImportRe  = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+)`)
	carbonPackageRe = regexp.MustCompile(`(?m)^\s*package\s+(\w+)`)
	carbonNsRe      = regexp.MustCompile(`(?m)^\s*namespace\s+(\w+)`)
	carbonCallRe    = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// CarbonExtractor extracts Carbon source using regex.
type CarbonExtractor struct{}

func NewCarbonExtractor() *CarbonExtractor { return &CarbonExtractor{} }

func (e *CarbonExtractor) Language() string     { return "carbon" }
func (e *CarbonExtractor) Extensions() []string { return []string{".carbon"} }

func (e *CarbonExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "carbon",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isCarbonKeyword(name) {
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
			Language: "carbon",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range carbonPackageRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range carbonNsRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range carbonFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range carbonClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range carbonIfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range carbonAdapterRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range carbonChoiceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}

	for _, m := range carbonImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range carbonCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isCarbonKeyword(name) {
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

func isCarbonKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "match", "case", "break", "continue",
		"return", "returned",
		"fn", "class", "interface", "adapter", "choice", "impl",
		"package", "namespace", "import", "library", "api",
		"var", "let", "const", "auto", "abstract", "base", "extends",
		"true", "false", "self", "Self", "virtual", "override", "final",
		"as", "in", "is", "not", "and", "or":
		return true
	}
	return false
}

var _ parser.Extractor = (*CarbonExtractor)(nil)
