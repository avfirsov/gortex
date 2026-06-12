package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Julia has an explicit `end`-terminated block structure for functions
// and modules plus a short one-line form `name(args) = body`. We cover
// both forms, struct / abstract type / primitive type declarations,
// macros, and the three import statements (`import`, `using`,
// `include(...)`).
var (
	juliaFuncRe      = regexp.MustCompile(`(?m)^\s*function\s+(\w+)`)
	juliaShortFuncRe = regexp.MustCompile(`(?m)^\s*(\w+)\s*\([^)]*\)\s*=[^=]`)
	juliaStructRe    = regexp.MustCompile(`(?m)^\s*(?:mutable\s+)?struct\s+(\w+)`)
	juliaAbstractRe  = regexp.MustCompile(`(?m)^\s*(?:abstract|primitive)\s+type\s+(\w+)`)
	juliaModuleRe    = regexp.MustCompile(`(?m)^\s*(?:bare)?module\s+(\w+)`)
	juliaMacroRe     = regexp.MustCompile(`(?m)^\s*macro\s+(\w+)`)
	juliaImportRe    = regexp.MustCompile(`(?m)^\s*(?:import|using)\s+([\w.]+)`)
	juliaIncludeRe   = regexp.MustCompile(`\binclude\s*\(\s*"([^"]+)"\s*\)`)
	juliaCallRe      = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
)

// JuliaExtractor extracts Julia source using regex.
type JuliaExtractor struct{}

func NewJuliaExtractor() *JuliaExtractor { return &JuliaExtractor{} }

func (e *JuliaExtractor) Language() string     { return "julia" }
func (e *JuliaExtractor) Extensions() []string { return []string{".jl"} }

func (e *JuliaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "julia",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isJuliaKeyword(name) {
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
			Language: "julia",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range juliaModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range juliaStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range juliaAbstractRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range juliaFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range juliaMacroRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range juliaShortFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, line)
	}

	for _, m := range juliaImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range juliaIncludeRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range juliaCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isJuliaKeyword(name) || name == "include" {
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

func isJuliaKeyword(s string) bool {
	switch s {
	case "if", "else", "elseif", "end", "for", "while", "do", "break", "continue",
		"return", "function", "macro", "module", "baremodule", "struct", "mutable",
		"abstract", "primitive", "type", "import", "using", "export", "let",
		"local", "global", "const", "begin", "try", "catch", "finally", "throw",
		"where", "in", "isa", "true", "false", "nothing", "missing":
		return true
	}
	return false
}

var _ parser.Extractor = (*JuliaExtractor)(nil)
