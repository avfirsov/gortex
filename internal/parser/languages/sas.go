package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// SAS — a mix of data-step and macro language. Case-insensitive
// keywords. The building blocks:
//   - DATA <name>;  ... RUN;                (dataset — variable)
//   - PROC <name> ...;  ... RUN;            (procedure call — function)
//   - %MACRO <name>(args);  ... %MEND <name>; (macro — function)
//
// Imports arrive through `%INCLUDE 'file';` and `LIBNAME ref '/path';`.
var (
	sasDataRe    = regexp.MustCompile(`(?im)^\s*data\s+([\w.]+)`)
	sasProcRe    = regexp.MustCompile(`(?im)^\s*proc\s+(\w+)`)
	sasMacroRe   = regexp.MustCompile(`(?im)^\s*%macro\s+(\w+)`)
	sasIncludeRe = regexp.MustCompile(`(?im)^\s*%include\s+['"]?([^'";\s]+)`)
	sasLibnameRe = regexp.MustCompile(`(?im)^\s*libname\s+(\w+)`)
	sasCallRe    = regexp.MustCompile(`%(\w+)\s*\(`)
)

// SASExtractor extracts SAS source using regex.
type SASExtractor struct{}

func NewSASExtractor() *SASExtractor { return &SASExtractor{} }

func (e *SASExtractor) Language() string     { return "sas" }
func (e *SASExtractor) Extensions() []string { return []string{".sas"} }

func (e *SASExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "sas",
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
			Language: "sas",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range sasDataRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "run;", "run ;")
		add(name, graph.KindVariable, line, end)
	}
	for _, m := range sasProcRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "run;", "run ;", "quit;")
		add(name, graph.KindFunction, line, end)
	}
	for _, m := range sasMacroRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "%mend")
		add(name, graph.KindFunction, line, end)
	}

	for _, m := range sasIncludeRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range sasLibnameRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range sasCallRe.FindAllSubmatchIndex(src, -1) {
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

var _ parser.Extractor = (*SASExtractor)(nil)
