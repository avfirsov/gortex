package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Pascal / Object Pascal / Delphi. Keywords are case-insensitive. The
// meaningful shapes are `unit` / `program` file markers, `procedure`
// and `function` (including class-qualified forms like
// `procedure TFoo.Bar`), `uses` imports, and class / record / interface
// declarations of the form `TFoo = class(TBase)`.
var (
	pascalProcRe    = regexp.MustCompile(`(?im)^\s*(?:class\s+)?(?:procedure|function|constructor|destructor)\s+(?:(\w+)\.)?(\w+)`)
	pascalUnitRe    = regexp.MustCompile(`(?im)^\s*unit\s+([\w.]+)`)
	pascalProgramRe = regexp.MustCompile(`(?im)^\s*program\s+(\w+)`)
	pascalUsesRe    = regexp.MustCompile(`(?im)^\s*uses\s+([^;]+);`)
	pascalTypeDefRe = regexp.MustCompile(`(?im)^\s*(\w+)\s*=\s*(class|record|interface|object)\b`)
	pascalUsesSplit = regexp.MustCompile(`[\s,]+`)
)

// PascalExtractor extracts Pascal / Delphi source using regex.
type PascalExtractor struct{}

func NewPascalExtractor() *PascalExtractor { return &PascalExtractor{} }

func (e *PascalExtractor) Language() string { return "pascal" }
func (e *PascalExtractor) Extensions() []string {
	return []string{".pas", ".pp", ".dpr", ".dpk", ".inc", ".lpr", ".lfm"}
}

func (e *PascalExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "pascal",
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
			Language: "pascal",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range pascalUnitRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, len(lines))
	}
	for _, m := range pascalProgramRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, len(lines))
	}
	for _, m := range pascalTypeDefRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		kind := string(src[m[4]:m[5]])
		line := lineAt(src, m[0])
		nodeKind := graph.KindType
		if strings.EqualFold(kind, "interface") {
			nodeKind = graph.KindInterface
		}
		add(name, nodeKind, line, findKeywordBlockEnd(lines, line, "end;", "end"))
	}
	for _, m := range pascalProcRe.FindAllSubmatchIndex(src, -1) {
		cls := ""
		if m[2] >= 0 {
			cls = string(src[m[2]:m[3]])
		}
		name := string(src[m[4]:m[5]])
		if cls != "" {
			name = cls + "." + name
		}
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, findKeywordBlockEnd(lines, line, "end;", "end"))
	}

	for _, m := range pascalUsesRe.FindAllSubmatchIndex(src, -1) {
		clause := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		for _, tok := range pascalUsesSplit.Split(clause, -1) {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: "unresolved::import::" + tok,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return result, nil
}

var _ parser.Extractor = (*PascalExtractor)(nil)
