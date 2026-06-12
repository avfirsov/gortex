package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// ABAP is SAP's procedural-cum-OO language. Keyword-delimited, case-
// insensitive. Forms are subroutines (`FORM name ... ENDFORM`), RFC-
// callable routines are `FUNCTION name ... ENDFUNCTION`, class methods
// are `METHOD name ... ENDMETHOD`, and classes are
// `CLASS name DEFINITION ... ENDCLASS`. Programs/reports sit at the
// file top via `REPORT NAME` or `PROGRAM NAME`, and `INCLUDE NAME`
// pulls in other ABAP sources.
var (
	abapFormRe     = regexp.MustCompile(`(?im)^\s*FORM\s+(\w+)`)
	abapFunctionRe = regexp.MustCompile(`(?im)^\s*FUNCTION\s+(\w+)`)
	abapMethodRe   = regexp.MustCompile(`(?im)^\s*METHOD\s+(\w+)`)
	abapClassRe    = regexp.MustCompile(`(?im)^\s*CLASS\s+(\w+)\s+DEFINITION`)
	abapReportRe   = regexp.MustCompile(`(?im)^\s*(?:REPORT|PROGRAM)\s+(\w+)`)
	abapIncludeRe  = regexp.MustCompile(`(?im)^\s*INCLUDE\s+(\w+)`)
	abapCallRe     = regexp.MustCompile(`(?i)\b(?:PERFORM|CALL\s+FUNCTION)\s+['"]?(\w+)['"]?`)
)

// ABAPExtractor extracts SAP ABAP source using regex.
type ABAPExtractor struct{}

func NewABAPExtractor() *ABAPExtractor { return &ABAPExtractor{} }

func (e *ABAPExtractor) Language() string     { return "abap" }
func (e *ABAPExtractor) Extensions() []string { return []string{".abap"} }

func (e *ABAPExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "abap",
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
			Language: "abap",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range abapFormRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "endform")
		add(name, graph.KindFunction, line, end)
	}
	for _, m := range abapFunctionRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "endfunction")
		add(name, graph.KindFunction, line, end)
	}
	for _, m := range abapMethodRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "endmethod")
		add(name, graph.KindMethod, line, end)
	}
	for _, m := range abapClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "endclass")
		add(name, graph.KindType, line, end)
	}
	for _, m := range abapReportRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}

	for _, m := range abapIncludeRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range abapCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
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

var _ parser.Extractor = (*ABAPExtractor)(nil)
