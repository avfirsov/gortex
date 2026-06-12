package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// AL is Microsoft's Business Central / Dynamics 365 language. The unit
// of code is the "object" — `table 50000 MyTable` / `page Id Name` /
// `codeunit` / `report` / `query` / `xmlport` / `enum` — each owning
// a body of procedures, triggers, and fields. We model the object
// itself as a type node and its procedures / triggers as methods.
var (
	alObjectRe = regexp.MustCompile(`(?mi)^\s*(table|page|codeunit|report|query|xmlport|enum|enumextension|pageextension|tableextension|profile|permissionset|controladdin|interface)\s+\d*\s*"?([A-Za-z][\w ]*)"?`)
	alProcRe   = regexp.MustCompile(`(?mi)^\s*(local\s+|internal\s+|protected\s+)?procedure\s+"?([A-Za-z][\w ]*)"?\s*\(`)
	alTrigRe   = regexp.MustCompile(`(?mi)^\s*trigger\s+(On[A-Za-z]\w*)\s*\(`)
	alVarRe    = regexp.MustCompile(`(?mi)^\s*"?([A-Za-z][\w ]*)"?\s*:\s*(Record|Text|Integer|Decimal|Boolean|Date|Time|DateTime|Code|Option|Enum)\b`)
	alUsing    = regexp.MustCompile(`(?mi)^\s*using\s+([A-Za-z][\w.]*)`)
	alCallRe   = regexp.MustCompile(`\b([A-Za-z_][\w]*)\s*\(`)
)

// ALExtractor extracts Microsoft AL source files using regex.
type ALExtractor struct{}

func NewALExtractor() *ALExtractor { return &ALExtractor{} }

func (e *ALExtractor) Language() string     { return "al" }
func (e *ALExtractor) Extensions() []string { return []string{".al", ".dal"} }

func (e *ALExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "al",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	for _, m := range alObjectRe.FindAllSubmatchIndex(src, -1) {
		keyword := strings.ToLower(string(src[m[2]:m[3]]))
		name := strings.TrimSpace(string(src[m[4]:m[5]]))
		if name == "" {
			continue
		}
		kind := graph.KindType
		if keyword == "interface" {
			kind = graph.KindInterface
		}
		line := lineAt(src, m[0])
		endLine := findBlockEnd(lines, line)
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: line, EndLine: endLine,
			Language: "al",
			Meta:     map[string]any{"al_kind": keyword},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, re := range []*regexp.Regexp{alProcRe, alTrigRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			nameIdx := m[len(m)-2:]
			name := strings.TrimSpace(string(src[nameIdx[0]:nameIdx[1]]))
			line := lineAt(src, m[0])
			endLine := findBlockEnd(lines, line)
			id := filePath + "::" + name
			if seen[id] {
				continue
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindMethod, Name: name,
				FilePath: filePath, StartLine: line, EndLine: endLine,
				Language: "al",
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
				FilePath: filePath, Line: line,
			})
		}
	}

	for _, m := range alVarRe.FindAllSubmatchIndex(src, -1) {
		name := strings.TrimSpace(string(src[m[2]:m[3]]))
		if isALKeyword(strings.ToLower(name)) || name == "" {
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
			Language: "al",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, m := range alUsing.FindAllSubmatchIndex(src, -1) {
		ns := strings.TrimSpace(string(src[m[2]:m[3]]))
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + ns,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range alCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isALKeyword(strings.ToLower(name)) {
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

func isALKeyword(s string) bool {
	switch s {
	case "if", "then", "else", "case", "of", "begin", "end", "for", "while",
		"repeat", "until", "do", "var", "procedure", "trigger", "local",
		"internal", "protected", "return", "exit", "with", "using", "true",
		"false", "and", "or", "not", "mod", "div", "in", "is", "record",
		"text", "integer", "decimal", "boolean", "date", "time", "datetime",
		"code", "option", "enum":
		return true
	}
	return false
}

var _ parser.Extractor = (*ALExtractor)(nil)
