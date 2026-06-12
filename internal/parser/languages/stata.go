package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Stata. User-defined programs are
//
//	program define NAME
//	...
//	end
//
// A `capture program drop NAME` line is bookkeeping rather than a real
// definition — we capture the name as a variable marker. `local NAME`
// and `global NAME` are scalar/macro variables. Data load is `use "X"`
// and script inclusion is `do "X"` or `include "X"`.
var (
	stataProgDefRe  = regexp.MustCompile(`(?m)^\s*(?:capture\s+)?program\s+(?:define\s+)?(\w+)`)
	stataProgDropRe = regexp.MustCompile(`(?m)^\s*capture\s+program\s+drop\s+(\w+)`)
	stataLocalRe    = regexp.MustCompile(`(?m)^\s*local\s+(\w+)`)
	stataGlobalRe   = regexp.MustCompile(`(?m)^\s*global\s+(\w+)`)
	stataUseRe      = regexp.MustCompile(`(?m)^\s*use\s+["']?([^"'\s,]+)`)
	stataDoRe       = regexp.MustCompile(`(?m)^\s*do\s+["']?([^"'\s,]+)`)
	stataIncludeRe  = regexp.MustCompile(`(?m)^\s*include\s+["']?([^"'\s,]+)`)
)

// StataExtractor extracts Stata source using regex.
type StataExtractor struct{}

func NewStataExtractor() *StataExtractor { return &StataExtractor{} }

func (e *StataExtractor) Language() string     { return "stata" }
func (e *StataExtractor) Extensions() []string { return []string{".do", ".ado"} }

func (e *StataExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "stata",
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
			Language: "stata",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	// `capture program drop NAME` first — so those lines are not mis-
	// attributed as real definitions.
	dropLines := make(map[int]bool)
	for _, m := range stataProgDropRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		dropLines[line] = true
		add(name, graph.KindVariable, line, line)
	}
	for _, m := range stataProgDefRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		if dropLines[line] {
			continue
		}
		end := findKeywordBlockEnd(lines, line, "end")
		add(name, graph.KindFunction, line, end)
	}
	for _, m := range stataLocalRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}
	for _, m := range stataGlobalRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}

	emitImport := func(mod string, line int) {
		if mod == "" {
			return
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range stataUseRe.FindAllSubmatchIndex(src, -1) {
		emitImport(string(src[m[2]:m[3]]), lineAt(src, m[0]))
	}
	for _, m := range stataDoRe.FindAllSubmatchIndex(src, -1) {
		emitImport(string(src[m[2]:m[3]]), lineAt(src, m[0]))
	}
	for _, m := range stataIncludeRe.FindAllSubmatchIndex(src, -1) {
		emitImport(string(src[m[2]:m[3]]), lineAt(src, m[0]))
	}

	return result, nil
}

var _ parser.Extractor = (*StataExtractor)(nil)
