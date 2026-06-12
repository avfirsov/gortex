package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Solidity is the dominant smart-contract language for the EVM. Its
// top-level constructs are `contract`, `library`, `interface`,
// `abstract contract`; each holding functions, modifiers, events,
// state variables, and struct / enum definitions. Imports resolve
// to file paths (`./X.sol`) or named symbols
// (`import {IERC20} from "./IERC20.sol"`).
var (
	solContractRe = regexp.MustCompile(`(?m)^\s*(abstract\s+contract|contract|library|interface)\s+(\w+)`)
	solFuncRe     = regexp.MustCompile(`(?m)^\s*function\s+(\w+)\s*\(`)
	solModifierRe = regexp.MustCompile(`(?m)^\s*modifier\s+(\w+)\s*\(?`)
	solEventRe    = regexp.MustCompile(`(?m)^\s*event\s+(\w+)\s*\(`)
	solStructRe   = regexp.MustCompile(`(?m)^\s*struct\s+(\w+)\s*\{`)
	solEnumRe     = regexp.MustCompile(`(?m)^\s*enum\s+(\w+)\s*\{`)
	solImportRe   = regexp.MustCompile(`(?m)^\s*import\s+(?:\{[^}]*\}\s+from\s+)?["']([^"']+)["']`)
	solVarRe      = regexp.MustCompile(`(?m)^\s+(?:public|private|internal|external)?\s*(\w+(?:\[\])?)\s+(?:public\s+|private\s+|internal\s+|constant\s+|immutable\s+)*(\w+)\s*(?:=|;)`)
	solCallRe     = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
)

// SolidityExtractor extracts Solidity source using regex.
type SolidityExtractor struct{}

func NewSolidityExtractor() *SolidityExtractor { return &SolidityExtractor{} }

func (e *SolidityExtractor) Language() string     { return "solidity" }
func (e *SolidityExtractor) Extensions() []string { return []string{".sol"} }

func (e *SolidityExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "solidity",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int, meta map[string]any) {
		if name == "" || isSolidityKeyword(name) {
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
			Language: "solidity",
			Meta:     meta,
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range solContractRe.FindAllSubmatchIndex(src, -1) {
		keyword := strings.TrimSpace(string(src[m[2]:m[3]]))
		name := string(src[m[4]:m[5]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		kind := graph.KindType
		if strings.Contains(keyword, "interface") {
			kind = graph.KindInterface
		}
		add(name, kind, line, end, map[string]any{"sol_kind": keyword})
	}

	for _, m := range solFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		add(name, graph.KindMethod, line, end, nil)
	}
	for _, m := range solModifierRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		add(name, graph.KindMethod, line, end, map[string]any{"sol_kind": "modifier"})
	}
	for _, m := range solEventRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, line, map[string]any{"sol_kind": "event"})
	}
	for _, m := range solStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		add(name, graph.KindType, line, end, map[string]any{"sol_kind": "struct"})
	}
	for _, m := range solEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		add(name, graph.KindType, line, end, map[string]any{"sol_kind": "enum"})
	}
	for _, m := range solVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[4]:m[5]])
		if isSolidityType(strings.ToLower(name)) {
			continue
		}
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line, nil)
	}

	for _, m := range solImportRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range solCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isSolidityKeyword(name) || isSolidityType(strings.ToLower(name)) {
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

func isSolidityKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "do", "break", "continue", "return",
		"function", "modifier", "event", "struct", "enum", "contract",
		"library", "interface", "abstract", "pragma", "import", "using",
		"new", "delete", "emit", "payable", "view", "pure", "public",
		"private", "internal", "external", "memory", "storage", "calldata",
		"require", "assert", "revert", "assembly", "true", "false", "null":
		return true
	}
	return false
}

func isSolidityType(s string) bool {
	if strings.HasPrefix(s, "uint") || strings.HasPrefix(s, "int") ||
		strings.HasPrefix(s, "bytes") {
		return true
	}
	switch s {
	case "address", "bool", "string", "bytes", "byte", "mapping":
		return true
	}
	return false
}

var _ parser.Extractor = (*SolidityExtractor)(nil)
