package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Handlebars (and Mustache) wraps everything in `{{ ... }}`. Block
// helpers open with `{{#name ...}}` and close with `{{/name}}`; their
// names become function nodes. `{{> partial}}` references get modelled
// as imports. Bare `{{helper ...}}` invocations inside an enclosing
// block helper produce call edges so get_callers still works across
// partials; standalone interpolations (outside a block) are ignored.
var (
	hbsBlockOpenRe  = regexp.MustCompile(`\{\{#([A-Za-z_][\w-]*)\b`)
	hbsBlockCloseRe = regexp.MustCompile(`\{\{/([A-Za-z_][\w-]*)\s*\}\}`)
	hbsPartialRe    = regexp.MustCompile(`\{\{>\s*([A-Za-z_][\w/.-]*)`)
	hbsHelperRe     = regexp.MustCompile(`\{\{([A-Za-z_][\w-]*)\b`)
)

// HandlebarsExtractor extracts Handlebars / Mustache templates.
type HandlebarsExtractor struct{}

func NewHandlebarsExtractor() *HandlebarsExtractor { return &HandlebarsExtractor{} }

func (e *HandlebarsExtractor) Language() string { return "handlebars" }
func (e *HandlebarsExtractor) Extensions() []string {
	return []string{".hbs", ".handlebars", ".mustache"}
}

func (e *HandlebarsExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "handlebars",
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
			Language: "handlebars",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	// Match open and close block tags together so we can pair them by
	// name into spans for enclosing-call attribution.
	type span struct {
		name      string
		startLine int
		endLine   int
	}
	var spans []span
	// Stack of currently-open blocks by position.
	type openBlock struct {
		name string
		line int
	}
	var stack []openBlock

	// Walk all block tokens (open + close) in source order.
	type tok struct {
		pos  int
		open bool
		name string
		line int
	}
	var toks []tok
	for _, m := range hbsBlockOpenRe.FindAllSubmatchIndex(src, -1) {
		toks = append(toks, tok{pos: m[0], open: true, name: string(src[m[2]:m[3]]), line: lineAt(src, m[0])})
	}
	for _, m := range hbsBlockCloseRe.FindAllSubmatchIndex(src, -1) {
		toks = append(toks, tok{pos: m[0], open: false, name: string(src[m[2]:m[3]]), line: lineAt(src, m[0])})
	}
	// Sort by position so push/pop ordering matches source.
	for i := 1; i < len(toks); i++ {
		for j := i; j > 0 && toks[j-1].pos > toks[j].pos; j-- {
			toks[j-1], toks[j] = toks[j], toks[j-1]
		}
	}
	for _, t := range toks {
		if t.open {
			stack = append(stack, openBlock{name: t.name, line: t.line})
			continue
		}
		// Close: pop the nearest matching open.
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].name == t.name {
				spans = append(spans, span{name: t.name, startLine: stack[i].line, endLine: t.line})
				stack = stack[:i]
				break
			}
		}
	}
	// Any unclosed blocks still on the stack close at EOF.
	for _, o := range stack {
		spans = append(spans, span{name: o.name, startLine: o.line, endLine: len(lines)})
	}
	for _, s := range spans {
		add(s.name, graph.KindFunction, s.startLine, s.endLine)
	}

	for _, m := range hbsPartialRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	// Helper call attribution — only when a helper appears inside an
	// enclosing block span.
	findEnclosing := func(line int) string {
		// Innermost wins — pick the smallest span that contains line.
		bestSize := -1
		bestName := ""
		for _, s := range spans {
			if line <= s.startLine || line > s.endLine {
				continue
			}
			size := s.endLine - s.startLine
			if bestSize == -1 || size < bestSize {
				bestSize = size
				bestName = s.name
			}
		}
		if bestName == "" {
			return ""
		}
		return filePath + "::" + bestName
	}
	for _, m := range hbsHelperRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isHbsKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		callerID := findEnclosing(line)
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

func isHbsKeyword(s string) bool {
	switch s {
	case "if", "else", "unless", "each", "with", "lookup", "log":
		return true
	}
	return false
}

var _ parser.Extractor = (*HandlebarsExtractor)(nil)
