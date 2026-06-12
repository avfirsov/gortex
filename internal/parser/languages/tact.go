package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Tact (TON) is a smart-contract DSL with brace-delimited bodies.
// `contract` / `trait` / `message` / `struct` define types;
// `fun` (possibly with `get` / receiver prefixes), `receive(...)`,
// and `init(...)` are methods / functions. `import "file";`
// declares dependencies.
var (
	tactContractRe = regexp.MustCompile(`(?m)^\s*contract\s+([A-Za-z_]\w*)`)
	tactTraitRe    = regexp.MustCompile(`(?m)^\s*trait\s+([A-Za-z_]\w*)`)
	tactMessageRe  = regexp.MustCompile(`(?m)^\s*message(?:\s*\([^)]*\))?\s+([A-Za-z_]\w*)`)
	tactStructRe   = regexp.MustCompile(`(?m)^\s*struct\s+([A-Za-z_]\w*)`)
	tactFunRe      = regexp.MustCompile(`(?m)^\s*(?:get\s+|extends\s+|virtual\s+|override\s+|abstract\s+)*fun\s+([A-Za-z_]\w*)`)
	tactReceiveRe  = regexp.MustCompile(`(?m)^(\s*)receive\s*\(`)
	tactInitRe     = regexp.MustCompile(`(?m)^(\s*)init\s*\(`)
	tactImportRe   = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)
)

// TactExtractor extracts TON Tact source using regex.
type TactExtractor struct{}

func NewTactExtractor() *TactExtractor { return &TactExtractor{} }

func (e *TactExtractor) Language() string     { return "tact" }
func (e *TactExtractor) Extensions() []string { return []string{".tact"} }

func (e *TactExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "tact",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	receiveIdx := 0
	initIdx := 0
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
			Language: "tact",
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
	captureBlock(tactContractRe, graph.KindType)
	captureBlock(tactTraitRe, graph.KindType)
	captureBlock(tactMessageRe, graph.KindType)
	captureBlock(tactStructRe, graph.KindType)
	captureBlock(tactFunRe, graph.KindFunction)

	// receive(...) and init(...) have no distinguishing name — we
	// synthesise positional identifiers so multiple receivers
	// (common in Tact contracts) do not collide.
	for _, m := range tactReceiveRe.FindAllSubmatchIndex(src, -1) {
		receiveIdx++
		line := lineAt(src, m[0])
		name := "receive"
		if receiveIdx > 1 {
			name = "receive$" + itoa(receiveIdx)
		}
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range tactInitRe.FindAllSubmatchIndex(src, -1) {
		initIdx++
		line := lineAt(src, m[0])
		name := "init"
		if initIdx > 1 {
			name = "init$" + itoa(initIdx)
		}
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}

	for _, m := range tactImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

// itoa avoids pulling in strconv for a single tiny use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

var _ parser.Extractor = (*TactExtractor)(nil)
