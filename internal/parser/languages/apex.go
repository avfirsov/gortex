package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Apex is Salesforce's Java-ish language. Brace-delimited with the
// usual OO primitives — class, interface, enum — plus the Salesforce-
// specific `trigger NAME on Object (events...) { ... }`. Methods are
// standard `modifier* ReturnType NAME(args) { ... }`. Apex has no
// user-visible import statement (namespaces resolve via System/sObject
// metadata), so we emit nothing on the import edge.
var (
	apexClassRe     = regexp.MustCompile(`(?m)^\s*(?:global\s+|public\s+|private\s+|protected\s+|virtual\s+|abstract\s+|with\s+sharing\s+|without\s+sharing\s+|inherited\s+sharing\s+)*class\s+(\w+)`)
	apexInterfaceRe = regexp.MustCompile(`(?m)^\s*(?:global\s+|public\s+|private\s+)*interface\s+(\w+)`)
	apexEnumRe      = regexp.MustCompile(`(?m)^\s*(?:global\s+|public\s+|private\s+)*enum\s+(\w+)`)
	apexTriggerRe   = regexp.MustCompile(`(?m)^\s*trigger\s+(\w+)\s+on\s+\w+`)
	// Method: modifiers + return type + NAME(args) {. We exclude keywords
	// that would otherwise match (if, for, while, switch, return, new).
	apexMethodRe = regexp.MustCompile(`(?m)^\s*(?:global\s+|public\s+|private\s+|protected\s+|static\s+|virtual\s+|override\s+|abstract\s+|final\s+|webservice\s+|testmethod\s+|transient\s+)+(?:[\w<>,.\[\]\s]+?)\s+(\w+)\s*\([^)]*\)\s*\{`)
	apexCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// ApexExtractor extracts Salesforce Apex source using regex.
type ApexExtractor struct{}

func NewApexExtractor() *ApexExtractor { return &ApexExtractor{} }

func (e *ApexExtractor) Language() string     { return "apex" }
func (e *ApexExtractor) Extensions() []string { return []string{".cls", ".trigger", ".apex"} }

func (e *ApexExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "apex",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isApexKeyword(strings.ToLower(name)) {
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
			Language: "apex",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range apexClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range apexInterfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindInterface, line, findBlockEnd(lines, line))
	}
	for _, m := range apexEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range apexTriggerRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range apexMethodRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isApexKeyword(strings.ToLower(name)) {
			continue
		}
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, findBlockEnd(lines, line))
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range apexCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isApexKeyword(strings.ToLower(name)) {
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

func isApexKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "do", "switch", "when", "break",
		"continue", "return", "new", "this", "super", "null", "true",
		"false", "try", "catch", "finally", "throw", "class", "interface",
		"enum", "trigger", "extends", "implements", "public", "private",
		"protected", "global", "static", "virtual", "abstract", "override",
		"final", "transient", "testmethod", "webservice", "with", "without",
		"inherited", "sharing", "on", "void", "instanceof":
		return true
	}
	return false
}

var _ parser.Extractor = (*ApexExtractor)(nil)
