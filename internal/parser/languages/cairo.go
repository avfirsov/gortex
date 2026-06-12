package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Cairo (StarkNet) is Rust-flavoured: brace bodies, `fn` / `struct`
// / `enum` / `trait` / `mod` keywords, and `use X::Y` imports. The
// `#[external]` / `#[view]` annotations can precede a `fn` but do
// not change how we capture it; the regex for `fn` tolerates
// leading whitespace only, so attributes on a prior line are simply
// ignored.
var (
	cairoFnRe     = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+([A-Za-z_]\w*)`)
	cairoStructRe = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)`)
	cairoEnumRe   = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)`)
	cairoTraitRe  = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)`)
	cairoModRe    = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?mod\s+([A-Za-z_]\w*)`)
	cairoUseRe    = regexp.MustCompile(`(?m)^\s*use\s+([\w:]+)`)
)

// CairoExtractor extracts StarkNet Cairo source using regex.
type CairoExtractor struct{}

func NewCairoExtractor() *CairoExtractor { return &CairoExtractor{} }

func (e *CairoExtractor) Language() string     { return "cairo" }
func (e *CairoExtractor) Extensions() []string { return []string{".cairo"} }

func (e *CairoExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "cairo",
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
			Language: "cairo",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	captureBlock := func(re *regexp.Regexp, kind graph.NodeKind) {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			name := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			add(name, kind, line, findBlockEnd(lines, line))
		}
	}
	captureBlock(cairoFnRe, graph.KindFunction)
	captureBlock(cairoStructRe, graph.KindType)
	captureBlock(cairoEnumRe, graph.KindType)
	captureBlock(cairoTraitRe, graph.KindType)
	captureBlock(cairoModRe, graph.KindType)

	for _, m := range cairoUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*CairoExtractor)(nil)
