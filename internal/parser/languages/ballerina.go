package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Ballerina mixes Java-style blocks with service / resource
// declarations. `function NAME(...)` is a function; `service NAME
// on ...` is a top-level service we also model as a function node;
// `type NAME record { ... }`, `type NAME T;`, and `class NAME`
// define types; `import ORG/PKG[ as ALIAS];` is the import.
var (
	ballerinaFunctionRe = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|isolated\s+|remote\s+|transactional\s+)*function\s+([A-Za-z_]\w*)`)
	ballerinaServiceRe  = regexp.MustCompile(`(?m)^\s*(?:public\s+|isolated\s+)*service\s+([A-Za-z_/][\w./-]*)\s+on\b`)
	ballerinaTypeRe     = regexp.MustCompile(`(?m)^\s*(?:public\s+)?type\s+([A-Za-z_]\w*)\b`)
	ballerinaClassRe    = regexp.MustCompile(`(?m)^\s*(?:public\s+|isolated\s+|readonly\s+|service\s+|client\s+|distinct\s+)*class\s+([A-Za-z_]\w*)`)
	ballerinaImportRe   = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_][\w./-]*)`)
)

// BallerinaExtractor extracts Ballerina source using regex.
type BallerinaExtractor struct{}

func NewBallerinaExtractor() *BallerinaExtractor { return &BallerinaExtractor{} }

func (e *BallerinaExtractor) Language() string     { return "ballerina" }
func (e *BallerinaExtractor) Extensions() []string { return []string{".bal"} }

func (e *BallerinaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "ballerina",
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
			Language: "ballerina",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range ballerinaFunctionRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range ballerinaServiceRe.FindAllSubmatchIndex(src, -1) {
		name := strings.Trim(string(src[m[2]:m[3]]), "/")
		if name == "" {
			name = "service"
		}
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range ballerinaTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		// `type X record { ... }` is brace-delimited; `type X int;`
		// is single-line. findBlockEnd handles both: when no `{`
		// appears on the header line it falls back to the start
		// line, which is the desired single-line behaviour.
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range ballerinaClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}

	for _, m := range ballerinaImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*BallerinaExtractor)(nil)
