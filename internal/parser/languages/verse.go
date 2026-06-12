package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Verse is Epic's Fortnite/UEFN language. Syntax is heavily influenced
// by OCaml and Haskell but uses `:=` for definition and `<type>` for
// return-type suffixes. Extraction covers the four symbol shapes that
// appear in practice: named functions, classes, enums, and top-level
// bindings; plus `using` module imports.
var (
	// Verse function: `Name[<spec>...](args)[<spec>...]:Type = body` or
	// `:= body`. Specifier blocks can appear before or after the args;
	// `OnBegin<override>()<suspends>:void =` is common in UEFN code.
	verseFuncRe = regexp.MustCompile(`(?m)^[ \t]*(\w[\w]*)\s*(?:<[^>]+>\s*)*\([^)]*\)\s*(?:<[^>]+>\s*)*:[^\n=]+\s*:?=`)
	// Class / enum / struct / interface: `Name := class(...)`, etc.
	verseClassRe = regexp.MustCompile(`(?m)^[ \t]*(\w[\w]*)\s*:=\s*(class|struct|enum|interface)\b`)
	// Top-level binding: `Name : Type = value` or `Name := value`.
	// Class / function lines also match this pattern, but they run
	// first and the `seen` map dedups them out.
	verseVarRe = regexp.MustCompile(`(?m)^[ \t]*(\w[\w]*)\s*(?::\s*\S[^=]*?)?\s*:?=\s*\S`)
	// `using { /Path.To/Module }`. Verse module paths start with `/`.
	verseUsing = regexp.MustCompile(`(?m)^\s*using\s*\{\s*([^}\s]+)\s*\}`)
	// Any bare identifier immediately followed by `(` is a candidate
	// call site; callers filter against keywords and the enclosing
	// function's own name.
	verseCallRe = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
)

// VerseExtractor extracts Verse source files using regex.
type VerseExtractor struct{}

func NewVerseExtractor() *VerseExtractor { return &VerseExtractor{} }

func (e *VerseExtractor) Language() string     { return "verse" }
func (e *VerseExtractor) Extensions() []string { return []string{".verse"} }

func (e *VerseExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "verse",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	for _, m := range verseFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isVerseKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		endLine := findIndentedBlockEnd(lines, line)
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: name,
			FilePath: filePath, StartLine: line, EndLine: endLine,
			Language: "verse",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, m := range verseClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		kind := graph.KindType
		if string(src[m[4]:m[5]]) == "interface" {
			kind = graph.KindInterface
		}
		line := lineAt(src, m[0])
		endLine := findIndentedBlockEnd(lines, line)
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: line, EndLine: endLine,
			Language: "verse",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, m := range verseVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isVerseKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindVariable, Name: name,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: "verse",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, m := range verseUsing.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range verseCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isVerseKeyword(name) {
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

func isVerseKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "loop", "break", "return", "yield",
		"class", "struct", "enum", "interface", "module", "using", "spawn",
		"true", "false", "not", "and", "or", "option", "type", "var", "set",
		"race", "sync", "rush", "branch":
		return true
	}
	return false
}

var _ parser.Extractor = (*VerseExtractor)(nil)
