package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// cMacroCallRe matches a call-like invocation `name(` inside a macro's
// replacement list. The captured identifier is the (possibly hidden)
// callee — write_log in `#define LOG(m) write_log(m)`.
var cMacroCallRe = regexp.MustCompile(`([A-Za-z_]\w*)\s*\(`)

// cKeywordsInMacroBody are C/C++ keywords that can syntactically precede
// a `(` in a replacement list but are never call targets.
var cKeywordsInMacroBody = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "return": true,
	"sizeof": true, "alignof": true, "_Alignof": true, "defined": true,
	"do": true, "else": true, "case": true, "static_cast": true,
	"reinterpret_cast": true, "const_cast": true, "dynamic_cast": true,
	"typeof": true, "decltype": true, "catch": true,
}

// emitCMacro emits a KindMacro node for a preproc_def / preproc_function_def
// node and, for function-like macros, the EdgeCalls its replacement list
// hides. defNode is the whole preproc_(function_)def node; isFunc selects
// the function-like shape (parameters + call recovery). lang is "c" or "cpp".
//
// The replacement list is a raw preproc_arg token (tree-sitter does not
// parse it), so call recovery scans its text for `name(` invocations,
// excluding the macro's own parameters and C/C++ keywords. A call site
// like `SQ(2)` parses as an ordinary call_expression and already resolves
// against the macro by name, so caller -> macro -> body-call forms a
// two-hop path through the expansion.
func emitCMacro(defNode *sitter.Node, isFunc bool, filePath, fileID, lang string, src []byte, result *parser.ExtractionResult, seen map[string]bool) {
	if defNode == nil {
		return
	}
	var name, replacement string
	var params []string
	for i := 0; i < int(defNode.ChildCount()); i++ {
		c := defNode.Child(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "identifier":
			if name == "" {
				name = c.Content(src)
			}
		case "preproc_params":
			for j := 0; j < int(c.NamedChildCount()); j++ {
				p := c.NamedChild(j)
				if p != nil && p.Type() == "identifier" {
					params = append(params, p.Content(src))
				}
			}
		case "preproc_arg":
			replacement = strings.TrimSpace(c.Content(src))
		}
	}
	if name == "" {
		return
	}
	id := filePath + "::" + name
	if seen[id] {
		return
	}
	seen[id] = true

	line := int(defNode.StartPoint().Row) + 1
	macroKind := "object"
	if isFunc {
		macroKind = "function"
	}
	meta := map[string]any{"macro_kind": macroKind}
	if len(params) > 0 {
		meta["params"] = params
	}
	if replacement != "" {
		const maxLen = 200
		r := replacement
		if len(r) > maxLen {
			r = r[:maxLen]
		}
		meta["replacement"] = r
	}
	result.Nodes = append(result.Nodes, &graph.Node{
		ID: id, Kind: graph.KindMacro, Name: name,
		FilePath: filePath, StartLine: line, EndLine: int(defNode.EndPoint().Row) + 1,
		Language: lang, Meta: meta,
	})
	result.Edges = append(result.Edges, &graph.Edge{
		From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: line,
	})

	// Recover macro-hidden calls from the replacement list.
	if replacement == "" {
		return
	}
	paramSet := make(map[string]bool, len(params))
	for _, p := range params {
		paramSet[p] = true
	}
	callSeen := make(map[string]bool)
	for _, m := range cMacroCallRe.FindAllStringSubmatch(replacement, -1) {
		callee := m[1]
		if paramSet[callee] || cKeywordsInMacroBody[callee] || callSeen[callee] {
			continue
		}
		callSeen[callee] = true
		result.Edges = append(result.Edges, &graph.Edge{
			From: id, To: "unresolved::" + callee,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
			Origin: graph.OriginASTInferred,
		})
	}
}
