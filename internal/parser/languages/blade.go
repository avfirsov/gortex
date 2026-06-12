package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Blade is Laravel's templating engine. Directives start with `@` and
// their arguments are parenthesised string literals. The extractor
// models `@section`, `@yield`, `@component`, `@include` as function
// nodes, and `@extends` as an import edge so cross-template inheritance
// shows up in the graph.
var (
	bladeSectionRe   = regexp.MustCompile(`(?m)@section\s*\(\s*['"]([^'"]+)['"]`)
	bladeYieldRe     = regexp.MustCompile(`(?m)@yield\s*\(\s*['"]([^'"]+)['"]`)
	bladeComponentRe = regexp.MustCompile(`(?m)@component\s*\(\s*['"]([^'"]+)['"]`)
	bladeIncludeRe   = regexp.MustCompile(`(?m)@include\s*\(\s*['"]([^'"]+)['"]`)
	bladeExtendsRe   = regexp.MustCompile(`(?m)@extends\s*\(\s*['"]([^'"]+)['"]`)
)

// BladeExtractor extracts Laravel Blade templates using regex.
type BladeExtractor struct{}

func NewBladeExtractor() *BladeExtractor { return &BladeExtractor{} }

func (e *BladeExtractor) Language() string     { return "blade" }
func (e *BladeExtractor) Extensions() []string { return []string{".blade", ".blade.php"} }

func (e *BladeExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "blade",
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
			Language: "blade",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, re := range []*regexp.Regexp{bladeSectionRe, bladeYieldRe, bladeComponentRe, bladeIncludeRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			name := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			add(name, graph.KindFunction, line, line)
		}
	}

	for _, m := range bladeExtendsRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*BladeExtractor)(nil)
