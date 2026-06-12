package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Mathematica / Wolfram Language. Functions are defined by pattern
// assignment: `name[x_, y_] := body` (SetDelayed) or `name[x_] = body`
// (Set). We also recognise the FullForm `SetDelayed[name, ...]`. Package
// loading uses `Get["Pkg`"]`, `Needs["Pkg`"]` or the shorthand
// `<< Pkg\“. Bodies are expressions, not blocks, so we emit the
// definition as a single-line node.
var (
	wolframFuncRe = regexp.MustCompile(`(?m)^\s*([A-Za-z$][\w$]*)\s*\[[^\]]*\]\s*:?=`)
	// FullForm: SetDelayed[name, body]
	wolframSetDelRe = regexp.MustCompile(`\bSetDelayed\[\s*([A-Za-z$][\w$]*)\b`)
	// Get / Needs take a string context ending in backtick:
	//   Get["Foo`"]   Needs["Foo`Sub`"]   << Foo`
	wolframGetRe   = regexp.MustCompile(`(?m)(?:Get|Needs)\[\s*"([^"]+)"`)
	wolframShortRe = regexp.MustCompile(`(?m)^\s*<<\s*([\w` + "`" + `.]+)`)
	wolframCallRe  = regexp.MustCompile(`\b([A-Za-z$][\w$]*)\s*\[`)
)

// MathematicaExtractor extracts Wolfram/Mathematica source using regex.
type MathematicaExtractor struct{}

func NewMathematicaExtractor() *MathematicaExtractor { return &MathematicaExtractor{} }

func (e *MathematicaExtractor) Language() string     { return "mathematica" }
func (e *MathematicaExtractor) Extensions() []string { return []string{".wl", ".wls", ".nb"} }

func (e *MathematicaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "mathematica",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isWolframBuiltin(name) {
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
			Language: "mathematica",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range wolframFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, line)
	}
	for _, m := range wolframSetDelRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, line)
	}

	for _, m := range wolframGetRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range wolframShortRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range wolframCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isWolframBuiltin(name) {
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

func isWolframBuiltin(s string) bool {
	switch s {
	case "If", "Which", "Switch", "Do", "For", "While", "Module",
		"Block", "With", "Return", "Break", "Continue", "Null", "True",
		"False", "Get", "Needs", "SetDelayed", "Set", "Function", "List",
		"Rule", "RuleDelayed", "Pattern", "Blank", "BlankSequence":
		return true
	}
	return false
}

var _ parser.Extractor = (*MathematicaExtractor)(nil)
