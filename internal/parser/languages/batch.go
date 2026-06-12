package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Batch (.bat / .cmd) structures code around labels. `:NAME` is a
// jump target — we model it as a function node whose range extends
// to the line before the next label. `call :NAME` and `goto NAME`
// are intra-file call edges; `call FILE.BAT` (no leading colon) is
// a cross-file import.
var (
	batchLabelRe     = regexp.MustCompile(`(?m)^\s*:([A-Za-z_][\w-]*)\s*$`)
	batchCallLabelRe = regexp.MustCompile(`(?mi)^\s*call\s+:([A-Za-z_][\w-]*)`)
	batchCallFileRe  = regexp.MustCompile(`(?mi)^\s*call\s+([A-Za-z_][\w.\\/-]*\.(?:bat|cmd))`)
	batchGotoRe      = regexp.MustCompile(`(?mi)^\s*goto\s+:?([A-Za-z_][\w-]*)`)
)

// BatchExtractor extracts Windows batch-file source using regex.
type BatchExtractor struct{}

func NewBatchExtractor() *BatchExtractor { return &BatchExtractor{} }

func (e *BatchExtractor) Language() string     { return "batch" }
func (e *BatchExtractor) Extensions() []string { return []string{".bat", ".cmd"} }

func (e *BatchExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "batch",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	type labelHit struct {
		name string
		line int
	}
	var labels []labelHit
	for _, m := range batchLabelRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		labels = append(labels, labelHit{name: name, line: line})
	}
	for i, lh := range labels {
		endLine := len(lines)
		if i+1 < len(labels) {
			endLine = labels[i+1].line - 1
			if endLine < lh.line {
				endLine = lh.line
			}
		}
		id := filePath + "::" + lh.name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: lh.name,
			FilePath: filePath, StartLine: lh.line, EndLine: endLine,
			Language: "batch",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: lh.line,
		})
	}

	for _, m := range batchCallFileRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	emitCall := func(target string, line int) {
		callerID := findEnclosingFunc(funcRanges, line)
		if callerID == "" || strings.HasSuffix(callerID, "::"+target) {
			return
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + target,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}
	for _, m := range batchCallLabelRe.FindAllSubmatchIndex(src, -1) {
		emitCall(string(src[m[2]:m[3]]), lineAt(src, m[0]))
	}
	for _, m := range batchGotoRe.FindAllSubmatchIndex(src, -1) {
		target := string(src[m[2]:m[3]])
		if strings.EqualFold(target, "eof") {
			continue
		}
		emitCall(target, lineAt(src, m[0]))
	}

	return result, nil
}

var _ parser.Extractor = (*BatchExtractor)(nil)
