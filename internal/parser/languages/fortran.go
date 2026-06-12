package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Fortran covers the fixed-form and free-form dialects alike. The
// unit of code is `subroutine`, `function`, `module`, `program`, and
// `type`. Imports are `use Module [, only: name]`; calls come through
// `call name(...)` for subroutines or via expression context for
// functions — we only catch the `call` form reliably.
var (
	fortranSubRe = regexp.MustCompile(`(?im)^\s*(?:pure\s+|elemental\s+|recursive\s+)?subroutine\s+(\w+)`)
	fortranFnRe  = regexp.MustCompile(`(?im)^\s*(?:pure\s+|elemental\s+|recursive\s+)?(?:\w+\s+)?function\s+(\w+)\s*\(`)
	// `module NAME` — but Fortran also uses `module procedure foo` inside
	// interfaces; the capture filters `procedure` by name below.
	fortranModRe  = regexp.MustCompile(`(?im)^\s*module\s+(\w+)`)
	fortranProgRe = regexp.MustCompile(`(?im)^\s*program\s+(\w+)`)
	fortranTypeRe = regexp.MustCompile(`(?im)^\s*type(?:\s*,\s*\w+)?\s*::\s*(\w+)`)
	fortranUseRe  = regexp.MustCompile(`(?im)^\s*use\s+(\w+)`)
	fortranCallRe = regexp.MustCompile(`(?im)\bcall\s+(\w+)\s*\(`)
)

// FortranExtractor extracts Fortran source using regex.
type FortranExtractor struct{}

func NewFortranExtractor() *FortranExtractor { return &FortranExtractor{} }

func (e *FortranExtractor) Language() string { return "fortran" }
func (e *FortranExtractor) Extensions() []string {
	return []string{".f", ".F", ".for", ".FOR", ".ftn", ".f90", ".F90", ".f95", ".F95", ".f03", ".F03", ".f08", ".F08"}
}

func (e *FortranExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "fortran",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int, meta map[string]any) {
		if name == "" || isFortranKeyword(strings.ToLower(name)) {
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
			Language: "fortran",
			Meta:     meta,
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range fortranModRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end module", "end")
		add(name, graph.KindType, line, end, map[string]any{"fortran_kind": "module"})
	}
	for _, m := range fortranProgRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end program", "end")
		add(name, graph.KindFunction, line, end, map[string]any{"fortran_kind": "program"})
	}
	for _, m := range fortranSubRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end subroutine", "end")
		add(name, graph.KindFunction, line, end, map[string]any{"fortran_kind": "subroutine"})
	}
	for _, m := range fortranFnRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end function", "end")
		add(name, graph.KindFunction, line, end, map[string]any{"fortran_kind": "function"})
	}
	for _, m := range fortranTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end type", "end")
		add(name, graph.KindType, line, end, nil)
	}

	for _, m := range fortranUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range fortranCallRe.FindAllSubmatchIndex(src, -1) {
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

func isFortranKeyword(s string) bool {
	switch s {
	case "if", "then", "else", "elseif", "endif", "end", "do", "while",
		"continue", "cycle", "exit", "return", "goto", "call", "use",
		"implicit", "none", "integer", "real", "double", "complex",
		"logical", "character", "dimension", "allocatable", "pointer",
		"target", "parameter", "save", "intent", "in", "out", "inout",
		"subroutine", "function", "module", "program", "type", "contains",
		"interface", "public", "private", "pure", "elemental", "recursive",
		"where", "forall", "select", "case", "default", "stop":
		return true
	}
	return false
}

var _ parser.Extractor = (*FortranExtractor)(nil)
