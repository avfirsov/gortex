package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// ERB embeds Ruby inside `<% ... %>` tags. The extractor captures
// Ruby `def name` and `class Name` declarations inside those blocks
// as function / type nodes, and Rails-style `render 'partial'`
// directives as import edges. The regexes match both the plain form
// and the symbol-hash form (`render :partial => 'x'`).
var (
	erbDefRe        = regexp.MustCompile(`(?m)^\s*def\s+([A-Za-z_][\w]*[!?=]?)`)
	erbClassRe      = regexp.MustCompile(`(?m)^\s*class\s+([A-Z][\w]*)`)
	erbRenderStrRe  = regexp.MustCompile(`render\s*\(?\s*['"]([^'"]+)['"]`)
	erbRenderHashRe = regexp.MustCompile(`render\s*\(?\s*:partial\s*=>\s*['"]([^'"]+)['"]`)
)

// ERBExtractor extracts Ruby ERB templates using regex.
type ERBExtractor struct{}

func NewERBExtractor() *ERBExtractor { return &ERBExtractor{} }

func (e *ERBExtractor) Language() string { return "erb" }
func (e *ERBExtractor) Extensions() []string {
	return []string{".erb", ".rhtml", ".html.erb", ".js.erb", ".css.erb", ".json.erb"}
}

func (e *ERBExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "erb",
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
			Language: "erb",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range erbDefRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "end"))
	}
	for _, m := range erbClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findKeywordBlockEnd(lines, line, "end"))
	}

	// Imports — hash form first so its string-literal isn't also
	// picked up by the looser plain form. Dedup by (mod, line) so a
	// `render :partial => 'x'` directive emits exactly one edge even
	// though both regexes can match the inner string literal.
	type importKey struct {
		mod  string
		line int
	}
	importSeen := make(map[importKey]bool)
	emitImport := func(mod string, line int) {
		k := importKey{mod: mod, line: line}
		if importSeen[k] {
			return
		}
		importSeen[k] = true
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range erbRenderHashRe.FindAllSubmatchIndex(src, -1) {
		emitImport(string(src[m[2]:m[3]]), lineAt(src, m[0]))
	}
	for _, m := range erbRenderStrRe.FindAllSubmatchIndex(src, -1) {
		emitImport(string(src[m[2]:m[3]]), lineAt(src, m[0]))
	}

	return result, nil
}

var _ parser.Extractor = (*ERBExtractor)(nil)
