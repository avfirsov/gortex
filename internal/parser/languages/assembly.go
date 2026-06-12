package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Assembly covers multiple dialects from one extractor: NASM, MASM,
// GAS / AT&T, WLA-DX, CA65, and ARM UAL. The set of concepts each
// dialect expresses is small enough to share: labels are the unit of
// code, `call` / `jsr` / `bl` / `jmp` / `bsr` are inter-label edges,
// `.global` / `.globl` / `global` declare exports, `.extern` /
// `extern` / `.import` declare imports, and NASM `%include` plus GAS
// `.include` plus WLA-DX `.INCLUDE` carry file dependencies.
//
// Each label is modelled as a function node so the rest of the
// graph-query surface (get_callers, find_usages, etc.) works without
// a dedicated asm-aware query path.
var (
	asmLabelRe = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_.][\w.$@]*)\s*:(?:$|[^:=])`)
	// NASM/MASM directives start with bare keyword; GAS prefixes with `.`.
	asmGlobalRe  = regexp.MustCompile(`(?mi)^[ \t]*(?:\.)?(global|globl|public)\s+([A-Za-z_][\w.$@]*)`)
	asmExternRe  = regexp.MustCompile(`(?mi)^[ \t]*(?:\.)?(extern|externdef|import)\s+([A-Za-z_][\w.$@]*)`)
	asmIncludeRe = regexp.MustCompile(`(?mi)^[ \t]*(?:%include|\.include|\.INCLUDE)\s+["<]?([^">\n]+?)[">]?\s*$`)
	// Call-like mnemonics across x86 (call), 6502 (jsr), ARM (bl/blx),
	// 68k (bsr/jsr), MIPS (jal/jalr), RISC-V (jal/jalr), SPARC (call).
	asmCallRe = regexp.MustCompile(`(?mi)^[ \t]*(?:[A-Za-z_][\w]*:)?\s*(call|calll|callq|callw|jsr|bl|blx|bsr|jal|jalr|jmp)\s+([A-Za-z_.][\w.$@]*)`)
)

// AssemblyExtractor extracts common assembly dialects using regex.
type AssemblyExtractor struct{}

func NewAssemblyExtractor() *AssemblyExtractor { return &AssemblyExtractor{} }

func (e *AssemblyExtractor) Language() string { return "assembly" }
func (e *AssemblyExtractor) Extensions() []string {
	return []string{".asm", ".s", ".S", ".nasm", ".masm", ".inc", ".a65"}
}

func (e *AssemblyExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "assembly",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	// Labels → functions. Next-label proximity gives a rough end-line.
	var labels []labelHit
	for _, m := range asmLabelRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isAsmDirective(strings.ToLower(name)) {
			continue
		}
		line := lineAt(src, m[0])
		labels = append(labels, labelHit{name: name, line: line})
	}
	for i, lh := range labels {
		endLine := len(lines)
		if i+1 < len(labels) {
			endLine = labels[i+1].line - 1
		}
		id := filePath + "::" + lh.name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: lh.name,
			FilePath: filePath, StartLine: lh.line, EndLine: endLine,
			Language: "assembly",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: lh.line,
		})
	}

	for _, m := range asmGlobalRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[4]:m[5]])
		id := filePath + "::" + name
		if n := findNode(result.Nodes, id); n != nil {
			if n.Meta == nil {
				n.Meta = map[string]any{}
			}
			n.Meta["global"] = true
		}
	}

	for _, m := range asmExternRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[4]:m[5]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::" + name,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	for _, m := range asmIncludeRe.FindAllSubmatchIndex(src, -1) {
		file := strings.TrimSpace(string(src[m[2]:m[3]]))
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + file,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range asmCallRe.FindAllSubmatchIndex(src, -1) {
		target := string(src[m[4]:m[5]])
		line := lineAt(src, m[0])
		callerID := findEnclosingFunc(funcRanges, line)
		if callerID == "" || strings.HasSuffix(callerID, "::"+target) {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + target,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

type labelHit struct {
	name string
	line int
}

func findNode(nodes []*graph.Node, id string) *graph.Node {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// isAsmDirective identifies tokens that look like labels but are
// actually segment / section directives masquerading as `name:`
// syntax in some toolchains. False positives here are cheap; the
// directive gets filtered, the label miss is recovered on the next
// pass through the graph.
func isAsmDirective(s string) bool {
	switch s {
	case ".text", ".data", ".bss", ".rodata", ".section", ".global",
		".globl", ".extern", ".include", ".equ", ".org", ".byte",
		".word", ".long", ".quad", ".ascii", ".asciz", ".string",
		"section", "segment", "ends", "end", "proc", "endp":
		return true
	}
	return false
}

var _ parser.Extractor = (*AssemblyExtractor)(nil)
