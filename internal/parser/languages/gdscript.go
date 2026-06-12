package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// GDScript is Godot's Python-flavored scripting language. The
// extractor handles the common surface: `func`, `class_name`,
// `extends`, `signal`, `var`, `const`, `enum`, inner `class`, and
// `preload(...)` / `load(...)` imports. Indentation defines scope, so
// `findBlockEnd` gives a reasonable function range.
var (
	gdFuncRe      = regexp.MustCompile(`(?m)^[ \t]*(?:static\s+)?func\s+(\w+)\s*\(`)
	gdClassNameRe = regexp.MustCompile(`(?m)^\s*class_name\s+(\w+)`)
	gdInnerClass  = regexp.MustCompile(`(?m)^[ \t]*class\s+(\w+)`)
	gdExtendsRe   = regexp.MustCompile(`(?m)^\s*extends\s+([\w.]+)`)
	gdVarRe       = regexp.MustCompile(`(?m)^[ \t]*(?:@\w+\s+)?(?:static\s+)?var\s+(\w+)`)
	gdConstRe     = regexp.MustCompile(`(?m)^[ \t]*const\s+(\w+)`)
	gdEnumRe      = regexp.MustCompile(`(?m)^[ \t]*enum\s+(\w+)`)
	gdSignalRe    = regexp.MustCompile(`(?m)^[ \t]*signal\s+(\w+)`)
	gdPreloadRe   = regexp.MustCompile(`\b(?:preload|load)\s*\(\s*["']([^"']+)["']\s*\)`)
	gdCallRe      = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
)

// GDScriptExtractor extracts Godot GDScript source using regex.
type GDScriptExtractor struct{}

func NewGDScriptExtractor() *GDScriptExtractor { return &GDScriptExtractor{} }

func (e *GDScriptExtractor) Language() string     { return "gdscript" }
func (e *GDScriptExtractor) Extensions() []string { return []string{".gd"} }

func (e *GDScriptExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "gdscript",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int, meta map[string]any) {
		if name == "" || isGDKeyword(name) {
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
			Language: "gdscript",
			Meta:     meta,
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range gdClassNameRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line, map[string]any{"gd_kind": "class_name"})
	}
	for _, m := range gdInnerClass.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findIndentedBlockEnd(lines, line)
		add(name, graph.KindType, line, end, map[string]any{"gd_kind": "inner_class"})
	}
	for _, m := range gdFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findIndentedBlockEnd(lines, line)
		kind := graph.KindFunction
		if strings.HasPrefix(name, "_") && name != "_init" && name != "_ready" && name != "_process" && name != "_physics_process" {
			// Private-looking; still a function.
			kind = graph.KindFunction
		}
		add(name, kind, line, end, nil)
	}
	for _, m := range gdVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line, nil)
	}
	for _, m := range gdConstRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line, map[string]any{"const": true})
	}
	for _, m := range gdEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line, map[string]any{"gd_kind": "enum"})
	}
	for _, m := range gdSignalRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindMethod, line, line, map[string]any{"gd_kind": "signal"})
	}

	for _, m := range gdExtendsRe.FindAllSubmatchIndex(src, -1) {
		parent := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::" + parent,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range gdPreloadRe.FindAllSubmatchIndex(src, -1) {
		path := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + path,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range gdCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isGDKeyword(name) {
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

func isGDKeyword(s string) bool {
	switch s {
	case "func", "var", "const", "class", "class_name", "extends", "enum",
		"signal", "static", "if", "elif", "else", "for", "while", "break",
		"continue", "return", "match", "pass", "in", "and", "or", "not",
		"is", "as", "self", "true", "false", "null", "void", "int", "float",
		"bool", "String", "Array", "Dictionary", "Vector2", "Vector3",
		"preload", "load":
		return true
	}
	return false
}

var _ parser.Extractor = (*GDScriptExtractor)(nil)
