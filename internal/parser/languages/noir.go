package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Noir (Aztec) is Rust-flavoured with `fn`, `struct`, `trait`,
// `impl`, and `mod` declarations inside brace-delimited bodies.
// Imports take the form `use dep::name::symbol` where `dep::` is a
// conventional prefix for the package namespace; we keep the full
// path intact for the unresolved key.
var (
	noirFnRe     = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+([A-Za-z_]\w*)`)
	noirStructRe = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)`)
	noirTraitRe  = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)`)
	noirImplRe   = regexp.MustCompile(`(?m)^\s*impl\s+([A-Za-z_]\w*)`)
	noirModRe    = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?mod\s+([A-Za-z_]\w*)`)
	noirUseRe    = regexp.MustCompile(`(?m)^\s*use\s+([\w:]+)`)
)

// NoirExtractor extracts Aztec Noir source using regex.
type NoirExtractor struct{}

func NewNoirExtractor() *NoirExtractor { return &NoirExtractor{} }

func (e *NoirExtractor) Language() string     { return "noir" }
func (e *NoirExtractor) Extensions() []string { return []string{".nr"} }

func (e *NoirExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "noir",
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
			Language: "noir",
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
	captureBlock(noirFnRe, graph.KindFunction)
	captureBlock(noirStructRe, graph.KindType)
	captureBlock(noirTraitRe, graph.KindType)
	captureBlock(noirImplRe, graph.KindType)
	captureBlock(noirModRe, graph.KindType)

	for _, m := range noirUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

var _ parser.Extractor = (*NoirExtractor)(nil)
