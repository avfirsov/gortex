package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// CoffeeScript is indent-delimited and surfaces functions as
// `name = (args) ->` (thin arrow) or `name = (args) =>` (bound/fat
// arrow). `class Name [extends Parent]` defines a class. Top-level
// `name = value` at column 0 is a plain variable binding. Module
// loading uses CommonJS-style `require 'X'` or `require "X"`.
var (
	coffeeFuncRe   = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*(?:\([^)]*\)\s*)?[-=]>`)
	coffeeClassRe  = regexp.MustCompile(`(?m)^\s*class\s+(\w+)`)
	coffeeVarRe    = regexp.MustCompile(`(?m)^(\w+)\s*=\s*[^=\->]`)
	coffeeImportRe = regexp.MustCompile(`(?m)^\s*\w*\s*=?\s*require\s*\(?\s*['"]([^'"]+)['"]`)
	coffeeCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// CoffeeScriptExtractor extracts CoffeeScript source using regex.
type CoffeeScriptExtractor struct{}

func NewCoffeeScriptExtractor() *CoffeeScriptExtractor { return &CoffeeScriptExtractor{} }

func (e *CoffeeScriptExtractor) Language() string     { return "coffeescript" }
func (e *CoffeeScriptExtractor) Extensions() []string { return []string{".coffee"} }

func (e *CoffeeScriptExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "coffeescript",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isCoffeeKeyword(name) {
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
			Language: "coffeescript",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	// Track which lines are functions so we don't re-emit them as bare
	// variables via the fall-through `name = value` pattern.
	funcLines := make(map[int]bool)
	for _, m := range coffeeFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		funcLines[line] = true
		add(name, graph.KindFunction, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range coffeeClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		funcLines[line] = true
		add(name, graph.KindType, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range coffeeVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		if funcLines[line] {
			continue
		}
		add(name, graph.KindVariable, line, line)
	}

	for _, m := range coffeeImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range coffeeCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isCoffeeKeyword(name) {
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

func isCoffeeKeyword(s string) bool {
	switch s {
	case "if", "else", "unless", "then", "for", "while", "until", "loop",
		"switch", "when", "break", "continue", "return", "class", "extends",
		"new", "this", "super", "null", "undefined", "true", "false",
		"and", "or", "not", "is", "isnt", "in", "of", "by", "do", "yes", "no",
		"on", "off", "try", "catch", "finally", "throw", "require":
		return true
	}
	return false
}

var _ parser.Extractor = (*CoffeeScriptExtractor)(nil)
