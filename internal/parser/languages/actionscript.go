package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// ActionScript 3 is brace-delimited, Java-flavoured, with explicit
// `package X.Y { ... }` wrapping and colon-typed declarations.
var (
	asPackageRe   = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)?\s*\{`)
	asClassRe     = regexp.MustCompile(`(?m)^\s*(?:public\s+|internal\s+|final\s+|dynamic\s+)*class\s+(\w+)`)
	asInterfaceRe = regexp.MustCompile(`(?m)^\s*(?:public\s+|internal\s+)*interface\s+(\w+)`)
	asFuncRe      = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+|internal\s+|static\s+|override\s+|final\s+)*function\s+(\w+)\s*\(`)
	asVarRe       = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+|internal\s+|static\s+|const\s+)*var\s+(\w+)\s*:`)
	asConstRe     = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+|internal\s+|static\s+)*const\s+(\w+)\s*:`)
	asImportRe    = regexp.MustCompile(`(?m)^\s*import\s+([\w.\*]+)\s*;`)
)

// ActionScriptExtractor extracts ActionScript 3 source using regex.
type ActionScriptExtractor struct{}

func NewActionScriptExtractor() *ActionScriptExtractor { return &ActionScriptExtractor{} }

func (e *ActionScriptExtractor) Language() string     { return "actionscript" }
func (e *ActionScriptExtractor) Extensions() []string { return []string{".as"} }

func (e *ActionScriptExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "actionscript",
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
			Language: "actionscript",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range asClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range asInterfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindInterface, line, findBlockEnd(lines, line))
	}
	for _, m := range asFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range asVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}
	for _, m := range asConstRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}
	// Track the package just as context; don't emit a symbol.
	_ = asPackageRe

	for _, m := range asImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*ActionScriptExtractor)(nil)
