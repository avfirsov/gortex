package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Racket. Definitions use `define` (value or function), `define-struct`,
// `define-syntax`. Modules are `module name lang ...` (or file-level
// `#lang` declarations, which we skip). Imports use `require` with
// either a string path or a collection name.
var (
	racketDefineFnRe     = regexp.MustCompile(`\(define\s*\(\s*([\w:*/+<>?!=.-]+)`)
	racketDefineValRe    = regexp.MustCompile(`\(define\s+([\w:*/+<>?!=.-]+)\s`)
	racketDefineStructRe = regexp.MustCompile(`\(define-struct\s+([\w:*/+<>?!=.-]+)`)
	racketDefineSyntaxRe = regexp.MustCompile(`\(define-syntax\s+\(?([\w:*/+<>?!=.-]+)`)
	racketModuleRe       = regexp.MustCompile(`\(module\s+([\w:*/+<>?!=.-]+)`)
	racketRequireRe      = regexp.MustCompile(`\(require\s+(?:"([^"]+)"|([\w/.-]+))`)
)

// RacketExtractor extracts Racket source using regex.
type RacketExtractor struct{}

func NewRacketExtractor() *RacketExtractor { return &RacketExtractor{} }

func (e *RacketExtractor) Language() string { return "racket" }
func (e *RacketExtractor) Extensions() []string {
	return []string{".rkt", ".rktl", ".rktd", ".scrbl"}
}

func (e *RacketExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "racket",
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
			Language: "racket",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range racketModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range racketDefineStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range racketDefineSyntaxRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range racketDefineFnRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range racketDefineValRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}

	for _, m := range racketRequireRe.FindAllSubmatchIndex(src, -1) {
		var mod string
		if m[2] >= 0 {
			mod = string(src[m[2]:m[3]])
		} else if m[4] >= 0 {
			mod = string(src[m[4]:m[5]])
		}
		if mod == "" {
			continue
		}
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*RacketExtractor)(nil)
