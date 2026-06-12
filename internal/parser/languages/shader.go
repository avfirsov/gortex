package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Shader covers GLSL and HLSL under one extractor — the syntaxes are
// close enough (C-like with `struct`, function signatures, `#include`)
// that splitting them buys nothing. Storage qualifiers for uniforms
// and attributes are surfaced as variable definitions so editors can
// jump to them.
var (
	shaderFuncRe    = regexp.MustCompile(`(?m)^\s*(?:[A-Za-z_]\w*\s+)+(\w+)\s*\([^)]*\)\s*(?::\s*\w+\s*)?\{`)
	shaderStructRe  = regexp.MustCompile(`(?m)^\s*(?:cbuffer|tbuffer|struct)\s+(\w+)`)
	shaderUniformRe = regexp.MustCompile(`(?m)^\s*(?:uniform|in|out|attribute|varying|layout\s*\([^)]+\)\s*\w+)\s+(?:\w+\s+)+(\w+)\s*[;\[]`)
	shaderIncludeRe = regexp.MustCompile(`(?m)^\s*#\s*include\s+(?:"([^"]+)"|<([^>]+)>)`)
)

// ShaderExtractor extracts GLSL / HLSL source using regex.
type ShaderExtractor struct{}

func NewShaderExtractor() *ShaderExtractor { return &ShaderExtractor{} }

func (e *ShaderExtractor) Language() string { return "shader" }
func (e *ShaderExtractor) Extensions() []string {
	return []string{
		".glsl", ".vert", ".frag", ".geom", ".comp", ".tesc", ".tese",
		".hlsl", ".fx", ".fxh", ".hlsli", ".vsh", ".psh",
	}
}

func (e *ShaderExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "shader",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isShaderKeyword(name) {
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
			Language: "shader",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range shaderFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range shaderStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range shaderUniformRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}

	for _, m := range shaderIncludeRe.FindAllSubmatchIndex(src, -1) {
		var target string
		if m[2] >= 0 {
			target = string(src[m[2]:m[3]])
		} else if m[4] >= 0 {
			target = string(src[m[4]:m[5]])
		}
		if target == "" {
			continue
		}
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + target,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isShaderKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "do", "switch", "case", "default",
		"break", "continue", "return", "discard", "void", "bool", "int",
		"uint", "float", "double", "vec2", "vec3", "vec4", "mat2", "mat3",
		"mat4", "sampler2D", "samplerCube", "struct", "uniform", "in", "out",
		"inout", "attribute", "varying", "const", "layout", "buffer":
		return true
	}
	return false
}

var _ parser.Extractor = (*ShaderExtractor)(nil)
