package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// VimScript. Functions use abbreviated forms (`fu`, `func`, `function`),
// may include `!` for override, and scope prefixes like `s:`, `g:`,
// `<SID>`. `let` defines variables; `source` pulls in files; `command`
// defines user commands.
var (
	vimFunctionRe = regexp.MustCompile(`(?im)^\s*fu(?:n|nc|nction)?!?\s+(?:s:|g:|b:|l:|<[sS][iI][dD]>)?([\w#]+)`)
	vimLetRe      = regexp.MustCompile(`(?im)^\s*let\s+(?:s:|g:|b:|l:|w:|t:)?(\w+)`)
	vimSourceRe   = regexp.MustCompile(`(?im)^\s*so(?:urce|u)?\s+(\S+)`)
	vimCommandRe  = regexp.MustCompile(`(?im)^\s*com(?:m|mand)?!?\s+(?:-\S+\s+)*(\w+)`)
)

// VimExtractor extracts VimScript source using regex.
type VimExtractor struct{}

func NewVimExtractor() *VimExtractor { return &VimExtractor{} }

func (e *VimExtractor) Language() string { return "vim" }
func (e *VimExtractor) Extensions() []string {
	return []string{".vim", ".vimrc", ".gvimrc", ".nvim"}
}

func (e *VimExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "vim",
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
			Language: "vim",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range vimFunctionRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "endfunction", "endfun", "endf"))
	}
	for _, m := range vimLetRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}
	for _, m := range vimCommandRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, line)
	}

	for _, m := range vimSourceRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*VimExtractor)(nil)
